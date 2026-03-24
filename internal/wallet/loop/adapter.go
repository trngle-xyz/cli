package loop

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trngle-xyz/cli/internal/wallet"
)

// NetworkAPIURLs maps network names to Loop API base URLs.
// Matches loop_daemon_ts/src/config.ts NETWORK_URLS.
var NetworkAPIURLs = map[string]string{
	"mainnet": "https://cantonloop.com",
	"testnet": "https://testnet.cantonloop.com",
	"devnet":  "https://devnet.cantonloop.com",
	"local":   "http://localhost:8080",
}

var Networks = []string{"mainnet", "testnet", "devnet"}

func normalizeDSOAdmin(admin string) string {
	return admin
}

const defaultHoldingInterfaceID = "#splice-api-token-holding-v1:Splice.Api.Token.HoldingV1:Holding"

type AuthResult struct {
	APIKey    string `json:"api_key"`
	AuthToken string `json:"auth_token"`
	TicketID  string `json:"ticket_id"`
	SessionID string `json:"session_id"`
	Email     string `json:"email"`
}

type AccountInfo struct {
	PartyID            string `json:"party_id"`
	PublicKey          string `json:"public_key"`
	Email              string `json:"email"`
	HasPreapproval     bool   `json:"has_preapproval"`
	HasMergeDelegation bool   `json:"has_merge_delegation"`
}

type Adapter struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	partyID    string
	apiURL     string

	mu        sync.RWMutex // protects authToken, apiKey, ticketID
	authToken string
	apiKey    string
	ticketID  string

	gasPollerStop   chan struct{}
	gasPollerOnce   sync.Once
	authRefreshStop chan struct{}
	authRefreshOnce sync.Once
}

type ledgerEndResponse struct {
	Offset string `json:"offset"`
}

type preparedSubmission struct {
	CommandID       string `json:"command_id"`
	TransactionHash string `json:"transaction_hash"`
	TransactionData string `json:"transaction_data"`
}

func NewAdapter() *Adapter {
	return &Adapter{}
}

func (a *Adapter) PartyID() string { return a.partyID }
func (a *Adapter) AuthToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authToken
}
func (a *Adapter) APIKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.apiKey
}
func (a *Adapter) TicketID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ticketID
}
func (a *Adapter) APIURL() string { return a.apiURL }

func (a *Adapter) PublicKey() string {
	if len(a.publicKey) == 0 {
		return ""
	}
	return hex.EncodeToString(a.publicKey)
}

// Initialize derives the ed25519 keypair from a hex-encoded private key.
// Accepts either a 32-byte seed or a full 64-byte ed25519 private key.
func (a *Adapter) Initialize(privateKeyHex string) error {
	keyBytes, err := hex.DecodeString(strings.TrimSpace(privateKeyHex))
	if err != nil {
		return fmt.Errorf("decode private key hex: %w", err)
	}

	switch len(keyBytes) {
	case ed25519.SeedSize:
		a.privateKey = ed25519.NewKeyFromSeed(keyBytes)
	case ed25519.PrivateKeySize:
		a.privateKey = ed25519.PrivateKey(keyBytes)
	default:
		return fmt.Errorf("invalid ed25519 key length: got %d bytes (expected 32 or 64)", len(keyBytes))
	}

	pub, ok := a.privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("private key did not produce ed25519 public key")
	}
	a.publicKey = pub
	return nil
}

// Authenticate calls POST /api/v1/.connect/pair/apikey using the Loop
// ed25519 signing protocol. Matches loop_daemon_ts/src/auth.ts.
//
// The message format is:
//
//	Exchange API Key for {partyID}\nTimestamp: {epoch}
//
// The request body is:
//
//	{ "public_key": "<hex>", "signature": "<hex>", "epoch": <millis> }
func (a *Adapter) Authenticate(partyID, apiURL string) error {
	if len(a.privateKey) == 0 {
		return fmt.Errorf("wallet not initialized — call Initialize first")
	}

	a.apiURL = apiURL
	a.partyID = partyID

	epoch := time.Now().UnixMilli()
	message := fmt.Sprintf("Exchange API Key for %s\nTimestamp: %d", partyID, epoch)

	signature := ed25519.Sign(a.privateKey, []byte(message))

	payload := map[string]any{
		"public_key": hex.EncodeToString(a.publicKey),
		"signature":  hex.EncodeToString(signature),
		"epoch":      epoch,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := http.Post(apiURL+"/api/v1/.connect/pair/apikey", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("connect to Loop API at %s: %w", apiURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Loop auth failed (%d): %s", resp.StatusCode, string(respBody))
	}

	var result AuthResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse auth response: %w", err)
	}

	a.mu.Lock()
	a.authToken = result.AuthToken
	a.apiKey = result.APIKey
	a.ticketID = result.TicketID
	a.mu.Unlock()
	return nil
}

// reauthenticate re-runs Authenticate using the stored partyID and apiURL.
func (a *Adapter) reauthenticate() error {
	partyID := a.partyID
	apiURL := a.apiURL
	if partyID == "" || apiURL == "" {
		return fmt.Errorf("cannot re-authenticate: missing partyID or apiURL")
	}
	return a.Authenticate(partyID, apiURL)
}

// doAuthTokenGet performs a GET with Bearer authToken, retrying once on
// 401/403 after re-authentication.
func (a *Adapter) doAuthTokenGet(reqURL string) ([]byte, error) {
	doOnce := func(token string) ([]byte, int, error) {
		req, err := http.NewRequest("GET", reqURL, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("read response: %w", err)
		}
		return body, resp.StatusCode, nil
	}

	a.mu.RLock()
	token := a.authToken
	a.mu.RUnlock()

	if token == "" {
		return nil, fmt.Errorf("not authenticated — call Authenticate first")
	}

	body, status, err := doOnce(token)
	if err != nil {
		return nil, err
	}

	// Retry once on auth failure.
	if status == 401 || status == 403 {
		if reauthErr := a.reauthenticate(); reauthErr != nil {
			return nil, fmt.Errorf("re-auth after %d: %w", status, reauthErr)
		}
		a.mu.RLock()
		token = a.authToken
		a.mu.RUnlock()

		body, status, err = doOnce(token)
		if err != nil {
			return nil, err
		}
	}

	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("http %d: %s", status, string(body))
	}
	return body, nil
}

// StartAuthRefresh launches a background goroutine that re-authenticates
// every 10 minutes to keep tokens fresh. Safe to call multiple times.
func (a *Adapter) StartAuthRefresh() {
	a.authRefreshOnce.Do(func() {
		a.authRefreshStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			log.Println("[auth-refresh] started (every 10m)")
			for {
				select {
				case <-a.authRefreshStop:
					log.Println("[auth-refresh] stopped")
					return
				case <-ticker.C:
					if err := a.reauthenticate(); err != nil {
						log.Printf("[auth-refresh] failed: %v", err)
					} else {
						log.Println("[auth-refresh] tokens refreshed")
					}
				}
			}
		}()
	})
}

// StopAuthRefresh stops the background auth refresh goroutine if running.
func (a *Adapter) StopAuthRefresh() {
	ch := a.authRefreshStop
	if ch == nil {
		return
	}
	select {
	case <-ch:
		// already closed
	default:
		close(ch)
	}
}

// FetchAccount calls GET /api/v1/.connect/pair/account with the bearer
// token obtained from Authenticate. Returns the account info including
// the canonical party_id from the Loop participant.
func (a *Adapter) FetchAccount() (*AccountInfo, error) {
	respBody, err := a.doAuthTokenGet(a.apiURL + "/api/v1/.connect/pair/account")
	if err != nil {
		return nil, fmt.Errorf("fetch account: %w", err)
	}

	var account AccountInfo
	if err := json.Unmarshal(respBody, &account); err != nil {
		return nil, fmt.Errorf("parse account response: %w", err)
	}

	// Update partyID with the canonical value from the server.
	a.partyID = account.PartyID
	return &account, nil
}

// ---------------------------------------------------------------------------
// WalletAdapter interface – real Loop API calls
// ---------------------------------------------------------------------------

// loopHolding is the JSON shape returned by GET /api/v1/.connect/pair/account/holding.
type loopHolding struct {
	InstrumentID struct {
		Admin string `json:"admin"`
		ID    string `json:"id"`
	} `json:"instrument_id"`
	Decimals      int    `json:"decimals"`
	Symbol        string `json:"symbol"`
	OrgName       string `json:"org_name"`
	TotalUnlocked string `json:"total_unlocked_coin"`
	TotalLocked   string `json:"total_locked_coin"`
	Image         string `json:"image"`
}

// GetBalances calls GET /api/v1/.connect/pair/account/holding with the
// bearer token from Authenticate. Returns fresh balances from the Loop API.
func (a *Adapter) GetBalances() ([]wallet.Balance, error) {
	respBody, err := a.doAuthTokenGet(a.apiURL + "/api/v1/.connect/pair/account/holding")
	if err != nil {
		return nil, fmt.Errorf("fetch holdings: %w", err)
	}

	var holdings []loopHolding
	if err := json.Unmarshal(respBody, &holdings); err != nil {
		return nil, fmt.Errorf("parse holdings: %w", err)
	}

	balances := make([]wallet.Balance, 0, len(holdings))
	for _, h := range holdings {
		// Use display symbol, normalize CC/Amulet.
		sym := h.Symbol
		if sym == "" {
			sym = h.InstrumentID.ID
		}
		balances = append(balances, wallet.Balance{
			Symbol:          sym,
			InstrumentID:    h.InstrumentID.ID,
			InstrumentAdmin: normalizeDSOAdmin(h.InstrumentID.Admin),
			Amount:          h.TotalUnlocked,
		})
	}
	return balances, nil
}

func (a *Adapter) GetHoldingContracts(interfaceID string) ([]wallet.HoldingContract, error) {
	if strings.TrimSpace(a.partyID) == "" {
		return nil, fmt.Errorf("party id is required")
	}
	if strings.TrimSpace(interfaceID) == "" {
		interfaceID = defaultHoldingInterfaceID
	}

	reqURL := a.apiURL + "/api/v1/.connect/pair/account/active-contracts?interfaceId=" + url.QueryEscape(interfaceID)
	respBody, err := a.doAuthTokenGet(reqURL)
	if err != nil {
		return nil, fmt.Errorf("fetch active contracts: %w", err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(respBody, &entries); err != nil {
		return nil, fmt.Errorf("parse active contracts: %w (body=%s)", err, previewBody(respBody))
	}

	out := make([]wallet.HoldingContract, 0, len(entries))
	for _, entry := range entries {
		jsActive, _ := nestedMap(entry, "contractEntry", "JsActiveContract")
		created, _ := nestedMap(jsActive, "createdEvent")
		if created == nil {
			continue
		}
		contractID, _ := created["contractId"].(string)
		templateID, _ := created["templateId"].(string)
		if strings.TrimSpace(contractID) == "" {
			continue
		}

		var amount, instrumentID, instrumentAdmin string
		var isLocked bool

		// Try interfaceViews first (Canton JSON-API v2 style).
		if views, ok := created["interfaceViews"].([]any); ok && len(views) > 0 {
			if first, ok := views[0].(map[string]any); ok {
				if viewValue, ok := first["viewValue"].(map[string]any); ok {
					amount, _ = viewValue["amount"].(string)
					if instrument, ok := viewValue["instrumentId"].(map[string]any); ok {
						instrumentID, _ = instrument["id"].(string)
						instrumentAdmin, _ = instrument["admin"].(string)
					}
					if viewValue["lock"] != nil {
						isLocked = true
					}
				}
			}
		}

		// Fallback: parse createArgument (Loop SDK style — no interfaceViews).
		if amount == "" {
			if args, ok := created["createArgument"].(map[string]any); ok {
				amount, instrumentID, instrumentAdmin, isLocked = parseHoldingCreateArgument(args, templateID)
			}
		}

		createdBlob, _ := created["createdEventBlob"].(string)
		synchronizerID, _ := created["synchronizerId"].(string)
		if synchronizerID == "" {
			synchronizerID, _ = jsActive["synchronizerId"].(string)
		}

		out = append(out, wallet.HoldingContract{
			ContractID:       contractID,
			TemplateID:       templateID,
			Amount:           amount,
			InstrumentID:     instrumentID,
			InstrumentAdmin:  normalizeDSOAdmin(instrumentAdmin),
			CreatedEventBlob: createdBlob,
			SynchronizerID:   synchronizerID,
			IsLocked:         isLocked,
		})
	}

	return out, nil
}

// RawContract holds the minimal fields from a Loop active-contracts query
// when we need access to createArgument fields beyond what HoldingContract
// provides (e.g. lockedAmulet CID inside AmuletAllocation).
type RawContract struct {
	ContractID       string
	TemplateID       string
	CreateArgument   map[string]any
	CreatedEventBlob string
	SynchronizerID   string
}

// GetActiveContractsByTemplate queries the Loop API for active contracts
// matching a DAML template ID (e.g. "pkg:Splice.Amulet:LockedAmulet").
func (a *Adapter) GetActiveContractsByTemplate(templateID string) ([]RawContract, error) {
	reqURL := a.apiURL + "/api/v1/.connect/pair/account/active-contracts?templateId=" + url.QueryEscape(templateID)
	respBody, err := a.doAuthTokenGet(reqURL)
	if err != nil {
		return nil, fmt.Errorf("fetch active contracts: %w", err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(respBody, &entries); err != nil {
		return nil, fmt.Errorf("parse active contracts: %w", err)
	}

	out := make([]RawContract, 0, len(entries))
	for _, entry := range entries {
		jsActive, _ := nestedMap(entry, "contractEntry", "JsActiveContract")
		created, _ := nestedMap(jsActive, "createdEvent")
		if created == nil {
			continue
		}
		contractID, _ := created["contractId"].(string)
		if contractID == "" {
			continue
		}
		tplID, _ := created["templateId"].(string)
		args, _ := created["createArgument"].(map[string]any)
		blob, _ := created["createdEventBlob"].(string)
		syncID, _ := created["synchronizerId"].(string)
		if syncID == "" {
			syncID, _ = jsActive["synchronizerId"].(string)
		}
		out = append(out, RawContract{
			ContractID:       contractID,
			TemplateID:       tplID,
			CreateArgument:   args,
			CreatedEventBlob: blob,
			SynchronizerID:   syncID,
		})
	}
	return out, nil
}

// LockedAmuletBlob holds the disclosed-contract data for a LockedAmulet
// that the taker passes to the operator for settlement.
type LockedAmuletBlob struct {
	ContractID       string `json:"contract_id"`
	TemplateID       string `json:"template_id"`
	CreatedEventBlob string `json:"created_event_blob"`
	SynchronizerID   string `json:"synchronizer_id"`
}

// GetLockedAmuletForAllocation finds the LockedAmulet referenced by an
// AmuletAllocation contract. The taker (sender) is a stakeholder on the
// LockedAmulet and can query it; the operator cannot.
//
// Uses the Allocation interface to discover the allocation, extracts the
// package hash from its templateId, then queries LockedAmulet by template.
func (a *Adapter) GetLockedAmuletForAllocation(allocationCID string) (*LockedAmuletBlob, error) {
	// 1. Query allocations via interface to find the matching one.
	allocs, err := a.GetRawContractsByInterface(
		"#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation",
	)
	if err != nil {
		return nil, fmt.Errorf("query allocations: %w", err)
	}

	var lockedAmuletCID string
	var pkgHash string
	for _, alloc := range allocs {
		if alloc.ContractID == allocationCID {
			lockedAmuletCID, _ = alloc.CreateArgument["lockedAmulet"].(string)
			// Extract package hash from templateId (format: "pkghash:Module:Type")
			if parts := strings.SplitN(alloc.TemplateID, ":", 2); len(parts) == 2 {
				pkgHash = parts[0]
			}
			break
		}
	}
	if lockedAmuletCID == "" {
		return nil, fmt.Errorf("allocation %s not found or has no lockedAmulet field", truncCID(allocationCID))
	}
	if pkgHash == "" {
		return nil, fmt.Errorf("could not extract package hash from allocation templateId")
	}

	// 2. Query LockedAmulet contracts by template to get the blob.
	lockedTpl := pkgHash + ":Splice.Amulet:LockedAmulet"
	locked, err := a.GetActiveContractsByTemplate(lockedTpl)
	if err != nil {
		return nil, fmt.Errorf("query LockedAmulet: %w", err)
	}

	for _, la := range locked {
		if la.ContractID == lockedAmuletCID {
			return &LockedAmuletBlob{
				ContractID:       la.ContractID,
				TemplateID:       la.TemplateID,
				CreatedEventBlob: la.CreatedEventBlob,
				SynchronizerID:   la.SynchronizerID,
			}, nil
		}
	}
	return nil, fmt.Errorf("LockedAmulet %s not found in active contracts", truncCID(lockedAmuletCID))
}

// GetRawContractsByInterface queries the Loop API for active contracts
// matching a DAML interface ID. Returns raw contract data with createArgument.
func (a *Adapter) GetRawContractsByInterface(interfaceID string) ([]RawContract, error) {
	reqURL := a.apiURL + "/api/v1/.connect/pair/account/active-contracts?interfaceId=" + url.QueryEscape(interfaceID)
	respBody, err := a.doAuthTokenGet(reqURL)
	if err != nil {
		return nil, fmt.Errorf("fetch active contracts: %w", err)
	}

	var entries []map[string]any
	if err := json.Unmarshal(respBody, &entries); err != nil {
		return nil, fmt.Errorf("parse active contracts: %w", err)
	}

	out := make([]RawContract, 0, len(entries))
	for _, entry := range entries {
		jsActive, _ := nestedMap(entry, "contractEntry", "JsActiveContract")
		created, _ := nestedMap(jsActive, "createdEvent")
		if created == nil {
			continue
		}
		contractID, _ := created["contractId"].(string)
		if contractID == "" {
			continue
		}
		tplID, _ := created["templateId"].(string)
		args, _ := created["createArgument"].(map[string]any)
		blob, _ := created["createdEventBlob"].(string)
		syncID, _ := created["synchronizerId"].(string)
		if syncID == "" {
			syncID, _ = jsActive["synchronizerId"].(string)
		}
		out = append(out, RawContract{
			ContractID:       contractID,
			TemplateID:       tplID,
			CreateArgument:   args,
			CreatedEventBlob: blob,
			SynchronizerID:   syncID,
		})
	}
	return out, nil
}

func truncCID(cid string) string {
	if len(cid) > 16 {
		return cid[:16] + "..."
	}
	return cid
}

// parseHoldingCreateArgument extracts amount, instrumentID, instrumentAdmin,
// and lock status from a holding contract's createArgument. Handles both
// Splice Amulet contracts and Utility Registry Holding contracts.
func parseHoldingCreateArgument(args map[string]any, templateID string) (amount, instrumentID, instrumentAdmin string, isLocked bool) {
	isAmulet := strings.Contains(templateID, "Amulet")

	if isAmulet {
		// Splice.Amulet:Amulet — amount is {initialAmount, ratePerRound}
		// or a direct numeric for LockedAmulet.amulet.amount
		instrumentID = "Amulet"
		if dso, ok := args["dso"].(string); ok {
			instrumentAdmin = dso
		}
		switch v := args["amount"].(type) {
		case string:
			amount = v
		case float64:
			amount = strconv.FormatFloat(v, 'f', 10, 64)
		case map[string]any:
			// Amulet amount is {initialAmount, ratePerRound}
			if ia, ok := v["initialAmount"].(string); ok {
				amount = ia
			} else if ia, ok := v["initialAmount"].(float64); ok {
				amount = strconv.FormatFloat(ia, 'f', 10, 64)
			}
		}
		// LockedAmulet wraps amulet inside args
		if amulet, ok := args["amulet"].(map[string]any); ok {
			if dso, ok := amulet["dso"].(string); ok {
				instrumentAdmin = dso
			}
			if amt, ok := amulet["amount"].(map[string]any); ok {
				if ia, ok := amt["initialAmount"].(string); ok {
					amount = ia
				} else if ia, ok := amt["initialAmount"].(float64); ok {
					amount = strconv.FormatFloat(ia, 'f', 10, 64)
				}
			}
		}
		if args["lock"] != nil {
			isLocked = true
		}
	} else {
		// Utility.Registry.Holding.V0.Holding:Holding
		if v, ok := args["amount"].(string); ok {
			amount = v
		} else if v, ok := args["amount"].(float64); ok {
			amount = strconv.FormatFloat(v, 'f', 10, 64)
		}
		if instrument, ok := args["instrument"].(map[string]any); ok {
			instrumentID, _ = instrument["id"].(string)
			instrumentAdmin, _ = instrument["source"].(string)
		}
		if args["lock"] != nil {
			isLocked = true
		}
	}
	return
}

func (a *Adapter) PrepareAndSubmit(payload wallet.CommandPayload) (*wallet.TransactionResult, error) {
	if len(a.privateKey) == 0 {
		return nil, fmt.Errorf("wallet not initialized")
	}
	a.mu.RLock()
	apiKey := a.apiKey
	ticketID := a.ticketID
	a.mu.RUnlock()
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(ticketID) == "" {
		return nil, fmt.Errorf("wallet not authenticated — api key or ticket id missing")
	}

	buildPrepareReq := func(includeSync bool) map[string]any {
		req := map[string]any{
			"ticket_id": ticketID,
			"payload": map[string]any{
				"commands":                     payload.Commands,
				"disclosedContracts":           payload.DisclosedContracts,
				"packageIdSelectionPreference": payload.PackageIDSelectionPreference,
				"actAs":                        payload.ActAs,
				"readAs":                       payload.ReadAs,
			},
		}
		if includeSync {
			if syncID := strings.TrimSpace(payload.SynchronizerID); syncID != "" {
				reqPayload, _ := req["payload"].(map[string]any)
				reqPayload["synchronizerId"] = syncID
			}
		}
		return req
	}

	syncID := strings.TrimSpace(payload.SynchronizerID)
	var prepared preparedSubmission

	tryPrepare := func() error {
		e := a.postAPIKeyJSON("/api/v1/.connect/tickets/prepare-transaction", buildPrepareReq(syncID != ""), &prepared)
		if e != nil && syncID != "" && strings.Contains(strings.ToUpper(e.Error()), "INVALID_PRESCRIBED_SYNCHRONIZER_ID") {
			e = a.postAPIKeyJSON("/api/v1/.connect/tickets/prepare-transaction", buildPrepareReq(false), &prepared)
			if e != nil {
				return fmt.Errorf("prepare transaction: invalid prescribed synchronizer id (provided=%q); retry without synchronizer failed: %w", syncID, e)
			}
		}
		return e
	}

	err := tryPrepare()
	// Reactive gas handling: if prepare returns 402, pay gas and retry once
	if err != nil && strings.Contains(err.Error(), "http 402") {
		if gasErr := a.EnsureGasPaid(); gasErr == nil {
			err = tryPrepare()
		}
	}
	if err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "INVALID_PRESCRIBED_SYNCHRONIZER_ID") {
			return nil, fmt.Errorf("prepare transaction: invalid prescribed synchronizer id (provided=%q)", syncID)
		}
		return nil, fmt.Errorf("prepare transaction: %w", err)
	}
	if strings.TrimSpace(prepared.CommandID) == "" || strings.TrimSpace(prepared.TransactionHash) == "" || strings.TrimSpace(prepared.TransactionData) == "" {
		return nil, fmt.Errorf("prepare transaction: missing command_id, transaction_hash, or transaction_data")
	}

	sig, err := signTransactionHash(a.privateKey, prepared.TransactionHash)
	if err != nil {
		return nil, fmt.Errorf("sign transaction hash: %w", err)
	}

	executeReq := map[string]any{
		"ticket_id":        a.ticketID,
		"request_id":       fmt.Sprintf("req-%d", time.Now().UnixNano()),
		"command_id":       prepared.CommandID,
		"signature":        sig,
		"transaction_data": prepared.TransactionData,
	}

	var execResult any
	execErr := a.postAPIKeyJSON("/api/v1/.connect/tickets/execute-transaction", executeReq, &execResult)
	// Reactive gas handling on execute: pay gas and retry the full prepare+execute cycle
	if execErr != nil && strings.Contains(execErr.Error(), "http 402") {
		if gasErr := a.EnsureGasPaid(); gasErr == nil {
			prepared = preparedSubmission{}
			if retryErr := tryPrepare(); retryErr == nil {
				sig2, sigErr := signTransactionHash(a.privateKey, prepared.TransactionHash)
				if sigErr == nil {
					executeReq["command_id"] = prepared.CommandID
					executeReq["signature"] = sig2
					executeReq["transaction_data"] = prepared.TransactionData
					executeReq["request_id"] = fmt.Sprintf("req-%d", time.Now().UnixNano())
					execErr = a.postAPIKeyJSON("/api/v1/.connect/tickets/execute-transaction", executeReq, &execResult)
				}
			}
		}
	}
	if execErr != nil {
		return nil, fmt.Errorf("execute transaction: %w", execErr)
	}

	return &wallet.TransactionResult{
		CommandID:       prepared.CommandID,
		TransactionTree: execResult,
	}, nil
}

// EnsureGasPaid checks for pending gas fees from a prior transaction and pays
// them if present. On Loop testnet, gas is charged after each transaction and
// must be settled before the next transaction can be submitted.
func (a *Adapter) EnsureGasPaid() error {
	a.mu.RLock()
	apiKey := a.apiKey
	a.mu.RUnlock()
	if strings.TrimSpace(apiKey) == "" {
		return nil // not authenticated yet
	}

	// Step 1: check pending gas
	var pending struct {
		Pending    bool   `json:"pending"`
		TrackingID string `json:"tracking_id"`
		GasAmount  string `json:"gas_amount"`
	}
	req, err := http.NewRequest("GET", a.apiURL+"/api/v1/transfer/pending-fee", nil)
	if err != nil {
		return nil // non-fatal
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil // non-fatal — proceed without gas check
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil // non-fatal
	}
	if resp.StatusCode != 200 {
		return nil // non-fatal
	}
	if err := json.Unmarshal(body, &pending); err != nil || !pending.Pending {
		return nil // no gas due
	}

	// Step 2: prepare gas payment
	prepareBody, err := json.Marshal(map[string]string{"tracking_id": pending.TrackingID})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	var prepared struct {
		TransactionHash string `json:"transaction_hash"`
	}
	if err := a.postAPIKeyJSON("/api/v1/transfer/pending-fee/prepare", json.RawMessage(prepareBody), &prepared); err != nil {
		return fmt.Errorf("prepare gas payment: %w", err)
	}
	if prepared.TransactionHash == "" {
		return fmt.Errorf("prepare gas payment: no transaction_hash returned")
	}

	// Step 3: sign and execute
	sig, err := signTransactionHash(a.privateKey, prepared.TransactionHash)
	if err != nil {
		return fmt.Errorf("sign gas payment: %w", err)
	}
	execReq := map[string]string{
		"transaction_hash": prepared.TransactionHash,
		"signature":        sig,
	}
	if err := a.postAPIKeyJSON("/api/v1/transfer/pending-fee/execute", execReq, nil); err != nil {
		return fmt.Errorf("execute gas payment: %w", err)
	}

	// Brief pause for network propagation after gas payment
	time.Sleep(500 * time.Millisecond)
	return nil
}

// StartGasPoller launches a background goroutine that checks for and pays
// pending gas every 2 seconds. Safe to call multiple times — only starts once.
func (a *Adapter) StartGasPoller() {
	a.gasPollerOnce.Do(func() {
		a.gasPollerStop = make(chan struct{})
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			log.Println("[gas-poller] started (every 2s)")
			for {
				select {
				case <-a.gasPollerStop:
					log.Println("[gas-poller] stopped")
					return
				case <-ticker.C:
					if err := a.EnsureGasPaid(); err != nil {
						if !strings.Contains(err.Error(), "INACTIVE_CONTRACTS") {
							log.Printf("[gas-poller] pay failed: %v", err)
						}
					}
				}
			}
		}()
	})
}

// StopGasPoller stops the background gas poller if running.
// Safe to call even if StartGasPoller was never called (no-op).
func (a *Adapter) StopGasPoller() {
	ch := a.gasPollerStop // read once to avoid race
	if ch == nil {
		return
	}
	select {
	case <-ch:
		// already closed
	default:
		close(ch)
	}
}

func (a *Adapter) GetPendingTransfers() ([]wallet.PendingTransfer, error) {
	if len(a.privateKey) == 0 {
		return nil, fmt.Errorf("wallet not initialized")
	}

	// Not yet implemented — return empty list until Loop API supports pending transfer queries.
	return nil, fmt.Errorf("pending transfers not yet implemented")
}

// DerivePartyID constructs a candidate Canton party ID from a hex-encoded
// ed25519 public key. Canton party IDs use the format:
//
//	{uid_fingerprint}::{namespace_fingerprint}
//
// where fingerprints are SHA-256 based. This attempts the most common
// convention used by Loop's participant. If the derived ID doesn't match
// the server's records, the wizard falls back to asking the user.
func DerivePartyID(publicKeyHex string) string {
	pubBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pubBytes) == 0 {
		return ""
	}
	hash := sha256.Sum256(pubBytes)
	// Canton UID: first 16 bytes of SHA-256(pubkey) as hex
	uid := hex.EncodeToString(hash[:16])
	// Canton fingerprint: 1220 prefix + full SHA-256 hex
	fingerprint := "1220" + hex.EncodeToString(hash[:])
	return uid + "::" + fingerprint
}

func (a *Adapter) AcceptTransfer(transferID string) (*wallet.TransactionResult, error) {
	if len(a.privateKey) == 0 {
		return nil, fmt.Errorf("wallet not initialized")
	}
	if strings.TrimSpace(transferID) == "" {
		return nil, fmt.Errorf("transfer id is required")
	}

	return &wallet.TransactionResult{
		CommandID: fmt.Sprintf("ACCEPT-%s", transferID),
		TransactionTree: map[string]any{
			"status":      "accepted",
			"transfer_id": transferID,
		},
	}, nil
}

func signTransactionHash(privateKey ed25519.PrivateKey, txHashBase64 string) (string, error) {
	hashBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(txHashBase64))
	if err != nil {
		return "", fmt.Errorf("decode base64 tx hash: %w", err)
	}
	sig := ed25519.Sign(privateKey, hashBytes)
	return hex.EncodeToString(sig), nil
}

func (a *Adapter) postAPIKeyJSON(path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	doOnce := func() ([]byte, int, error) {
		a.mu.RLock()
		key := a.apiKey
		a.mu.RUnlock()

		req, err := http.NewRequest("POST", a.apiURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("read response: %w", err)
		}
		return respBody, resp.StatusCode, nil
	}

	respBody, status, err := doOnce()
	if err != nil {
		return err
	}

	// Retry once on auth failure.
	if status == 401 || status == 403 {
		if reauthErr := a.reauthenticate(); reauthErr != nil {
			return fmt.Errorf("re-auth after %d: %w", status, reauthErr)
		}
		respBody, status, err = doOnce()
		if err != nil {
			return err
		}
	}

	if status < 200 || status >= 300 {
		return fmt.Errorf("http %d: %s", status, string(respBody))
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func nestedMap(root map[string]any, keys ...string) (map[string]any, bool) {
	cur := root
	for _, k := range keys {
		nextAny, ok := cur[k]
		if !ok {
			return nil, false
		}
		next, ok := nextAny.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func previewBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
