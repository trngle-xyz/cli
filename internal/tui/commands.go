package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/confirmbuilder"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

type tradeWallet interface {
	wallet.WalletAdapter
	Authenticate(partyID, apiURL string) error
}

var (
	newQuoteClientFn = core.NewQuoteClient // func(baseURL, apiKey string) QuoteClient
	newTradeWalletFn = func() tradeWallet { return loop.NewAdapter() }
)

type walletInitMsg struct {
	address string
}

type gasPollerReadyMsg struct {
	poller *loop.Adapter
}

type apiHealthMsg struct {
	healthy  bool
	endpoint string
	token    string
}

type errMsg struct {
	err error
}

type shutdownResultMsg struct {
	apiAttempted bool
	apiStopped   bool
	err          error
}

// apiStartedMsg is sent after we attempt to start the local API server post-wizard.
type apiStartedMsg struct{}

// StartAPIServerFunc is set by main.go to allow the TUI model to trigger
// the API server start after the setup wizard completes.
var StartAPIServerFunc func(cfg config.AppConfig)

// ShutdownAPIServerFunc is set by main.go so the TUI can perform
// graceful verified shutdown before quitting.
var ShutdownAPIServerFunc func() error

// startGasPollerCmd creates a persistent Loop adapter and starts its background
// gas poller. Returns gasPollerReadyMsg so the Model can store it.
func startGasPollerCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !useLoopWalletProvider(cfg) {
			return gasPollerReadyMsg{} // no poller needed for non-Loop wallets
		}
		privKey := strings.TrimSpace(cfg.PrivateKeyHex)
		if privKey == "" {
			return gasPollerReadyMsg{}
		}
		partyID := strings.TrimSpace(cfg.WalletPartyID)
		if partyID == "" {
			return gasPollerReadyMsg{}
		}
		adapter := loop.NewAdapter()
		if err := adapter.Initialize(privKey); err != nil {
			return gasPollerReadyMsg{}
		}
		apiURL := resolveWalletAPIURL(cfg)
		if err := adapter.Authenticate(partyID, apiURL); err != nil {
			return gasPollerReadyMsg{}
		}
		adapter.StartGasPoller()
		return gasPollerReadyMsg{poller: adapter}
	}
}

// startAPIAfterSetupCmd starts the API server and then checks health.
func startAPIAfterSetupCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if StartAPIServerFunc != nil && cfg.LocalAPI.Enabled {
			StartAPIServerFunc(cfg)
		}
		return apiStartedMsg{}
	}
}

// initializeWalletCmd validates the config has a PK and party ID,
// then returns the stored party ID as the wallet address.
// The actual Loop auth was already done during the setup wizard —
// we don't re-authenticate on every TUI launch.
func initializeWalletCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		// Small delay so the spinner is visible (feels like it's doing something).
		time.Sleep(300 * time.Millisecond)

		if cfg.PrivateKeyHex == "" {
			return errMsg{err: fmt.Errorf("no private key configured — run setup")}
		}
		if cfg.WalletPartyID == "" {
			return errMsg{err: fmt.Errorf("no party ID configured — run setup")}
		}

		return walletInitMsg{
			address: cfg.WalletPartyID,
		}
	}
}

// balancesResultMsg carries the result of an async balance fetch.
type balancesResultMsg struct {
	balances []wallet.Balance
	err      error
}

type quotePrefetchResultMsg struct {
	quote     *core.Quote
	acceptCtx *core.AcceptContext
	err       error
}

type quoteAcceptContextReadyMsg struct {
	quoteID   string
	acceptCtx *core.AcceptContext
	err       error
}

type quoteMergeNeededMsg struct {
	holdings []wallet.HoldingContract // all matching unlocked holdings to merge
	total    float64
	asset    string
}

type quoteMergeCompleteMsg struct {
	single *wallet.HoldingContract
}

type quoteHoldingsValidMsg struct {
	single *wallet.HoldingContract
}

type quoteSubmitResultMsg struct {
	quoteID      string
	tradeCID     string
	allocationID string
	confirmErr   error
	submitErr    error
}

// fetchBalancesCmd authenticates with the configured wallet provider and fetches fresh holdings.
func fetchBalancesCmd(cfg config.AppConfig, preferredPartyID string) tea.Cmd {
	return func() tea.Msg {
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) {
			if v := strings.TrimSpace(preferredPartyID); v != "" {
				authPartyID = v
			} else if v := strings.TrimSpace(cfg.QuotePartyID); v != "" {
				authPartyID = v
			}
		}

		if strings.TrimSpace(authPartyID) == "" {
			return balancesResultMsg{err: fmt.Errorf("wallet party id not configured")}
		}
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return balancesResultMsg{err: fmt.Errorf("wallet private key not configured")}
		}

		adapter := walletAdapterForConfig(cfg)
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return balancesResultMsg{err: fmt.Errorf("key error: %w", err)}
		}

		apiURL := resolveWalletAPIURL(cfg)
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return balancesResultMsg{err: fmt.Errorf("auth failed: %w", err)}
		}

		balances, err := adapter.GetBalances()
		if err != nil {
			return balancesResultMsg{err: err}
		}
		return balancesResultMsg{balances: balances}
	}
}

// checkAPIHealthCmd checks if the local API server is reachable.
// Retries a few times with short delays to allow the background server to bind.
func checkAPIHealthCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !cfg.LocalAPI.Enabled {
			return apiHealthMsg{healthy: false, endpoint: "disabled"}
		}

		endpoint := fmt.Sprintf("http://%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)

		token := ""
		if cfg.LocalAPI.AuthMode == "auto-token" && cfg.LocalAPI.FixedToken != "" {
			token = cfg.LocalAPI.FixedToken
		}

		// Retry up to 10 times (total ~5s) to let the server finish binding.
		healthy := false
		client := &http.Client{Timeout: 1 * time.Second}
		for i := 0; i < 10; i++ {
			resp, err := client.Get(endpoint + "/health")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					healthy = true
					break
				}
			}
			time.Sleep(500 * time.Millisecond)
		}

		return apiHealthMsg{
			healthy:  healthy,
			endpoint: endpoint,
			token:    token,
		}
	}
}

func shutdownAppCmd(cfg config.AppConfig) tea.Cmd {
	return func() tea.Msg {
		if !cfg.LocalAPI.Enabled {
			return shutdownResultMsg{apiAttempted: false, apiStopped: true}
		}

		if ShutdownAPIServerFunc == nil {
			return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: fmt.Errorf("shutdown callback not configured")}
		}

		if err := ShutdownAPIServerFunc(); err != nil {
			return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: err}
		}

		// Verify the API is actually down.
		endpoint := fmt.Sprintf("http://%s:%d", cfg.LocalAPI.Bind, cfg.LocalAPI.Port)
		client := &http.Client{Timeout: 500 * time.Millisecond}
		for i := 0; i < 8; i++ {
			resp, err := client.Get(endpoint + "/health")
			if err != nil {
				return shutdownResultMsg{apiAttempted: true, apiStopped: true}
			}
			resp.Body.Close()
			time.Sleep(150 * time.Millisecond)
		}
		return shutdownResultMsg{apiAttempted: true, apiStopped: false, err: fmt.Errorf("api still responding after shutdown attempt")}
	}
}

// prefetchQuoteCmd requests a quote from the operator.  The accept context
// (trade creation on-chain) is fetched lazily when the user presses Y, so
// no on-chain work happens until the user confirms.
func prefetchQuoteCmd(cfg config.AppConfig, from, to, amount, partyID string) tea.Cmd {
	return func() tea.Msg {
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		quoteParty := resolveQuotePartyID(cfg, partyID)
		quote, err := client.RequestQuote(context.Background(), from, to, amount, quoteParty)
		if err != nil {
			return quotePrefetchResultMsg{err: err}
		}
		return quotePrefetchResultMsg{quote: quote}
	}
}

func resolveQuotePartyID(cfg config.AppConfig, fallback string) string {
	if v := strings.TrimSpace(cfg.QuotePartyID); v != "" {
		return v
	}
	if v := strings.TrimSpace(fallback); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfg.WalletPartyID); v != "" {
		return v
	}
	return ""
}

func resolveWalletAPIURL(cfg config.AppConfig) string {
	if v := strings.TrimSpace(os.Getenv("TRNGLE_LOOP_API_URL")); v != "" {
		return v
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network == "" {
		network = "mainnet"
	}
	if v := strings.TrimSpace(loop.NetworkAPIURLs[network]); v != "" {
		return v
	}
	return loop.NetworkAPIURLs["mainnet"]
}

func walletAdapterForConfig(_ config.AppConfig) tradeWallet {
	return newTradeWalletFn()
}

func useLoopWalletProvider(_ config.AppConfig) bool {
	return true
}

// fetchAcceptContextCmd calls the operator /accept endpoint to create the
// trade on-chain and returns the resulting AcceptContext. No wallet interaction
// happens here — that is deferred to submitOnChainCmd.
func fetchAcceptContextCmd(cfg config.AppConfig, quoteID string) tea.Cmd {
	return func() tea.Msg {
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		acceptCtx, err := client.AcceptQuoteContext(context.Background(), quoteID)
		if err != nil {
			return quoteAcceptContextReadyMsg{quoteID: quoteID, err: fmt.Errorf("accept quote: %w", err)}
		}
		return quoteAcceptContextReadyMsg{quoteID: quoteID, acceptCtx: acceptCtx}
	}
}

// submitOnChainCmd performs wallet auth, holding selection, registry factory
// lookup, and on-chain allocation + taker-confirm submission. It expects
// activeQuote.AcceptContext to already be populated.
func submitOnChainCmd(cfg config.AppConfig, activeQuote *ActiveQuote) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil || activeQuote.AcceptContext == nil {
			quoteID := ""
			if activeQuote != nil {
				quoteID = activeQuote.ID
			}
			return quoteSubmitResultMsg{quoteID: quoteID, submitErr: fmt.Errorf("missing accept context")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) {
			if takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty); takerParty != "" {
				authPartyID = takerParty
			}
		}
		if strings.TrimSpace(authPartyID) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		// Pre-check holdings to detect merge requirement before proceeding.
		allHoldings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		single, mergeSlice, selErr := checkMergeNeeded(allHoldings, activeQuote.AcceptContext.TakerLeg.Asset, activeQuote.AcceptContext.TakerLeg.Amount)
		if selErr != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: selErr}
		}
		if single == nil {
			// No single holding is sufficient — signal merge needed.
			var total float64
			for _, h := range mergeSlice {
				if v, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64); err == nil {
					total += v
				}
			}
			return quoteMergeNeededMsg{
				holdings: mergeSlice,
				total:    total,
				asset:    activeQuote.AcceptContext.TakerLeg.Asset,
			}
		}

		return quoteHoldingsValidMsg{single: single}
	}
}

// executeQuoteSubmitCmd is the tea.Cmd triggered when the user presses Y.
// It first fetches the accept context (creating the trade on-chain via the
// operator), then resolves the allocation factory via the Canton registry,
// and finally submits the allocation + taker-confirm commands.
func executeQuoteSubmitCmd(cfg config.AppConfig, activeQuote *ActiveQuote) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil {
			return quoteSubmitResultMsg{submitErr: fmt.Errorf("missing active quote")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		authPartyID := strings.TrimSpace(cfg.WalletPartyID)
		if !useLoopWalletProvider(cfg) && activeQuote.AcceptContext != nil {
			if takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty); takerParty != "" {
				authPartyID = takerParty
			}
		}
		if strings.TrimSpace(authPartyID) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(authPartyID, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)

		// Fetch accept context now (operator creates the trade on-chain).
		// This is the first on-chain action — triggered by the user pressing Y.
		if activeQuote.AcceptContext == nil {
			acceptCtx, err := client.AcceptQuoteContext(context.Background(), activeQuote.ID)
			if err != nil {
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("accept quote: %w", err)}
			}
			activeQuote.AcceptContext = acceptCtx

			// Now that we have the taker party, re-auth with the correct party.
			if !useLoopWalletProvider(cfg) {
				if takerParty := strings.TrimSpace(acceptCtx.TakerParty); takerParty != "" && !strings.EqualFold(takerParty, authPartyID) {
					_ = adapter.Authenticate(takerParty, apiURL)
				}
			}
		}

		return executeAcceptContextSubmit(client, adapter, activeQuote, nil, cfg.Network)
	}
}

// ── Canton registry factory lookup ────────────────────────────────────────

// Network-specific constants, scan registries, and utility token contract
// data are defined in network_data.go.

type registryFactoryResult struct {
	FactoryID          string
	ExpectedAdmin      string // overrides holding.InstrumentAdmin when set (e.g. after domain migration)
	ChoiceContextData  any
	DisclosedContracts []any
}

// fetchRegistryFactoryFn is a package-level var so tests can stub it out.
var fetchRegistryFactoryFn = defaultFetchRegistryFactory

// fetchFactoryViaPrepareTransfer calls the Loop API's prepareTransfer endpoint
// to discover the correct AllocationFactory for CC/Amulet. On testnet, the scan
// registry returns stale contracts from the old domain, but prepareTransfer
// returns the correct ExternalPartyAmuletRules (which implements both
// TransferFactory and AllocationFactory). See ALLOCATION_FIX.md for details.
func fetchFactoryViaPrepareTransfer(authToken string, holding wallet.HoldingContract, network string) (*registryFactoryResult, error) {
	apiURL, ok := loop.NetworkAPIURLs[strings.ToLower(network)]
	if !ok || authToken == "" {
		return nil, fmt.Errorf("no API URL for network %s or no auth token", network)
	}

	now := time.Now().UTC()
	reqBody := map[string]any{
		"recipient":      holding.InstrumentAdmin, // dummy recipient (we won't execute)
		"amount":         "0.0001",
		"instrument_id":  holding.InstrumentID,
		"requested_at":   now.Format(time.RFC3339),
		"execute_before": now.Add(5 * time.Minute).Format(time.RFC3339),
	}
	// If admin is empty (Amulet), use instrument_id only
	if strings.TrimSpace(holding.InstrumentAdmin) != "" {
		reqBody["instrument"] = map[string]any{
			"instrument_admin": holding.InstrumentAdmin,
			"instrument_id":    holding.InstrumentID,
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("prepareTransfer: marshal: %w", err)
	}
	req, err := http.NewRequest("POST", apiURL+"/api/v1/.connect/pair/transfer", bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prepareTransfer: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prepareTransfer: http %d", resp.StatusCode)
	}

	// Parse the response to extract factory + context + disclosed
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("prepareTransfer: parse: %w", err)
	}

	// The response may be wrapped in { payload: { commands, disclosedContracts } }
	// or directly { commands, disclosedContracts }
	payload, _ := raw["payload"].(map[string]any)
	if payload == nil {
		payload = raw
	}

	commands, _ := payload["commands"].([]any)
	if len(commands) == 0 {
		return nil, fmt.Errorf("prepareTransfer: no commands in response")
	}

	// Extract the ExerciseCommand from the first command
	cmd0, _ := commands[0].(map[string]any)
	exerciseCmd, _ := cmd0["ExerciseCommand"].(map[string]any)
	if exerciseCmd == nil {
		return nil, fmt.Errorf("prepareTransfer: no ExerciseCommand in first command")
	}

	factoryID, _ := exerciseCmd["contractId"].(string)
	if factoryID == "" {
		return nil, fmt.Errorf("prepareTransfer: no contractId")
	}

	// Extract choiceArgument.extraArgs.context
	choiceArg, _ := exerciseCmd["choiceArgument"].(map[string]any)
	extraArgs, _ := choiceArg["extraArgs"].(map[string]any)
	contextData, _ := extraArgs["context"].(map[string]any)

	// Extract expectedAdmin — on testnet after domain migration, prepareTransfer
	// returns the correct admin (f22a) while holdings still reference the old (b143).
	expectedAdmin, _ := choiceArg["expectedAdmin"].(string)

	// Extract disclosed contracts (filtering out the user's holding)
	disclosedRaw, _ := payload["disclosedContracts"].([]any)
	var disclosed []any
	for _, d := range disclosedRaw {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		tid, _ := dm["templateId"].(string)
		// Exclude the user's own holding (Amulet/LockedAmulet) — only keep system contracts
		if strings.Contains(tid, ":Amulet:Amulet") || strings.Contains(tid, ":Amulet:LockedAmulet") {
			continue
		}
		disclosed = append(disclosed, d)
	}

	return &registryFactoryResult{
		FactoryID:          factoryID,
		ExpectedAdmin:      expectedAdmin,
		ChoiceContextData:  contextData,
		DisclosedContracts: disclosed,
	}, nil
}

// defaultFetchRegistryFactory calls the Canton scan registry to resolve the
// AllocationFactory contract, mirroring what the TypeScript SDK does in
// fetchFactory().  Returns the factory CID, choiceContextData, and disclosed
// contracts needed to submit the AllocationFactory_Allocate command.
// The network parameter ("mainnet"/"testnet") determines which admin mapping
// and registry URLs to use.
func defaultFetchRegistryFactory(authToken string, holding wallet.HoldingContract, ctx *core.AcceptContext, transferLegID string, network string) (*registryFactoryResult, error) {
	// For testnet utility tokens, use hardcoded contracts (scan registry returns empty context)
	if hardcoded := buildHardcodedUtilityFactory(holding.InstrumentAdmin, network); hardcoded != nil {
		return hardcoded, nil
	}

	// For CC/Amulet on testnet, use prepareTransfer to get the correct factory
	// (the scan registry returns stale contracts from the old domain).
	if strings.EqualFold(network, "testnet") && !isUtilitiesAdmin(holding.InstrumentAdmin, network) {
		if result, err := fetchFactoryViaPrepareTransfer(authToken, holding, network); err == nil {
			return result, nil
		}
		// Fall through to scan registry if prepareTransfer fails
	}

	now := time.Now().UTC()

	reqBody := map[string]any{
		"choiceArguments": map[string]any{
			"expectedAdmin": holding.InstrumentAdmin,
			"allocation": map[string]any{
				"settlement": map[string]any{
					"executor": ctx.OperatorParty,
					"settlementRef": map[string]any{
						"id":  fmt.Sprintf("settlement-%s", ctx.TradeID),
						"cid": nil,
					},
					"requestedAt":    now.Format(time.RFC3339),
					"allocateBefore": now.Add(5 * time.Minute).Format(time.RFC3339),
					"settleBefore":   now.Add(24 * time.Hour).Format(time.RFC3339),
					"meta":           map[string]any{"values": map[string]any{}},
				},
				"transferLegId": transferLegID,
				"transferLeg": map[string]any{
					"sender":   ctx.TakerParty,
					"receiver": ctx.MakerParty,
					"amount":   ctx.TakerLeg.Amount,
					"instrumentId": map[string]any{
						"admin": holding.InstrumentAdmin,
						"id":    holding.InstrumentID,
					},
					"meta": map[string]any{"values": map[string]any{}},
				},
			},
			"requestedAt":      now.Format(time.RFC3339),
			"inputHoldingCids": []string{holding.ContractID},
			"extraArgs": map[string]any{
				"context": map[string]any{"values": map[string]any{}},
				"meta":    map[string]any{"values": map[string]any{}},
			},
		},
	}

	var registryURLs []string
	if isUtilitiesAdmin(holding.InstrumentAdmin, network) {
		registryURLs = []string{
			fmt.Sprintf("%s/api/token-standard/v0/registrars/%s/registry/allocation-instruction/v1/allocation-factory",
				utilitiesBaseURLForNetwork(network), url.PathEscape(holding.InstrumentAdmin)),
		}
	} else {
		for _, base := range scanRegistriesForNetwork(network) {
			registryURLs = append(registryURLs, base+"/registry/allocation-instruction/v1/allocation-factory")
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("registryFactory: marshal: %w", err)
	}
	httpClient := &http.Client{Timeout: 12 * time.Second}

	for _, registryURL := range registryURLs {
		req, err := http.NewRequest(http.MethodPost, registryURL, bytes.NewReader(enc))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		var out struct {
			FactoryID    string `json:"factoryId"`
			ChoiceContext struct {
				DisclosedContracts []any `json:"disclosedContracts"`
				ChoiceContextData  any   `json:"choiceContextData"`
			} `json:"choiceContext"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			continue
		}
		if strings.TrimSpace(out.FactoryID) == "" {
			continue
		}
		return &registryFactoryResult{
			FactoryID:          out.FactoryID,
			ChoiceContextData:  out.ChoiceContext.ChoiceContextData,
			DisclosedContracts: out.ChoiceContext.DisclosedContracts,
		}, nil
	}

	return nil, fmt.Errorf("allocation factory not found in registry for admin=%s", holding.InstrumentAdmin)
}

// ── TransferFactory (merge / self-transfer) ────────────────────────────────

const transferFactoryTemplateID = "#splice-api-token-transfer-instruction-v1:Splice.Api.Token.TransferInstructionV1:TransferFactory"

type transferFactoryResult struct {
	FactoryID          string
	ChoiceContextData  any
	DisclosedContracts []any
}

// fetchTransferFactory calls the Splice registry to get a TransferFactory
// contract for a self-transfer merge. Pattern mirrors defaultFetchRegistryFactory.
func fetchTransferFactory(authToken string, holdings []wallet.HoldingContract, takerParty string, total float64, network string) (*transferFactoryResult, error) {
	if len(holdings) == 0 {
		return nil, fmt.Errorf("no holdings provided for merge")
	}
	primary := holdings[0]
	now := time.Now().UTC()

	cids := make([]string, len(holdings))
	for i, h := range holdings {
		cids[i] = h.ContractID
	}

	reqBody := map[string]any{
		"choiceArguments": map[string]any{
			"expectedAdmin": primary.InstrumentAdmin,
			"transfer": map[string]any{
				"sender":   takerParty,
				"receiver": takerParty,
				"amount":   strconv.FormatFloat(total, 'f', 10, 64),
				"instrumentId": map[string]any{
					"admin": primary.InstrumentAdmin,
					"id":    primary.InstrumentID,
				},
				"lock":             nil,
				"requestedAt":      now.Format(time.RFC3339),
				"executeBefore":    now.Add(24 * time.Hour).Format(time.RFC3339),
				"meta":             map[string]any{"values": map[string]any{}},
				"inputHoldingCids": cids,
			},
			"extraArgs": map[string]any{
				"context": map[string]any{"values": map[string]any{}},
				"meta":    map[string]any{"values": map[string]any{}},
			},
		},
	}

	var registryURLs []string
	if isUtilitiesAdmin(primary.InstrumentAdmin, network) {
		registryURLs = []string{
			fmt.Sprintf("%s/api/token-standard/v0/registrars/%s/registry/transfer-instruction/v1/transfer-factory",
				utilitiesBaseURLForNetwork(network), url.PathEscape(primary.InstrumentAdmin)),
		}
	} else {
		for _, base := range scanRegistriesForNetwork(network) {
			registryURLs = append(registryURLs, base+"/registry/transfer-instruction/v1/transfer-factory")
		}
	}

	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("transferFactory: marshal: %w", err)
	}
	httpClient := &http.Client{Timeout: 12 * time.Second}
	var lastErr string
	for _, registryURL := range registryURLs {
		req, err := http.NewRequest(http.MethodPost, registryURL, bytes.NewReader(enc))
		if err != nil {
			lastErr = err.Error()
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			bodyStr := string(body)
			if len(bodyStr) > 150 {
				bodyStr = bodyStr[:150]
			}
			lastErr = fmt.Sprintf("registry %d: %s", resp.StatusCode, bodyStr)
			continue
		}
		var out struct {
			FactoryID    string `json:"factoryId"`
			ChoiceContext struct {
				DisclosedContracts []any `json:"disclosedContracts"`
				ChoiceContextData  any   `json:"choiceContextData"`
			} `json:"choiceContext"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			lastErr = fmt.Sprintf("parse: %s", err.Error())
			continue
		}
		if strings.TrimSpace(out.FactoryID) == "" {
			lastErr = "empty factory ID in response"
			continue
		}
		return &transferFactoryResult{
			FactoryID:          out.FactoryID,
			ChoiceContextData:  out.ChoiceContext.ChoiceContextData,
			DisclosedContracts: out.ChoiceContext.DisclosedContracts,
		}, nil
	}
	if lastErr != "" {
		return nil, fmt.Errorf("transfer factory: %s", lastErr)
	}
	return nil, fmt.Errorf("transfer factory not found in registry for admin=%s", primary.InstrumentAdmin)
}

// mergeHoldingsCmd performs a self-transfer to consolidate multiple holdings
// into a single one, then calls executeAcceptContextSubmit with the result.
func mergeHoldingsCmd(cfg config.AppConfig, activeQuote *ActiveQuote, holdings []wallet.HoldingContract) tea.Cmd {
	return func() tea.Msg {
		if activeQuote == nil || activeQuote.AcceptContext == nil {
			return quoteSubmitResultMsg{submitErr: fmt.Errorf("missing accept context for merge")}
		}

		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty)
		if takerParty == "" {
			takerParty = strings.TrimSpace(cfg.WalletPartyID)
		}
		if takerParty == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet party id not configured")}
		}
		if err := adapter.Authenticate(takerParty, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}

		authToken := ""
		if tokenHolder, ok := adapter.(interface{ AuthToken() string }); ok {
			authToken = tokenHolder.AuthToken()
		}

		// Re-fetch holdings fresh — the original CIDs from the merge msg may be
		// stale if the background gas poller consumed a holding in the interim.
		freshHoldings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("re-fetch holdings for merge: %w", err)}
		}
		asset := activeQuote.AcceptContext.TakerLeg.Asset
		amount := activeQuote.AcceptContext.TakerLeg.Amount
		_, mergeSlice, selErr := checkMergeNeeded(freshHoldings, asset, amount)
		if selErr != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge re-check: %w", selErr)}
		}
		// Use the fresh holdings for the merge
		holdings = mergeSlice

		var total float64
		for _, h := range holdings {
			if v, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64); err == nil {
				total += v
			}
		}

		tfResult, err := fetchTransferFactory(authToken, holdings, takerParty, total, cfg.Network)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("transfer factory: %w", err)}
		}

		now := time.Now().UTC()
		cids := make([]string, len(holdings))
		for i, h := range holdings {
			cids[i] = h.ContractID
		}
		primary := holdings[0]
		transferPayload := map[string]any{
			"sender":   takerParty,
			"receiver": takerParty,
			"amount":   strconv.FormatFloat(total, 'f', 10, 64),
			"instrumentId": map[string]any{
				"admin": primary.InstrumentAdmin,
				"id":    primary.InstrumentID,
			},
			"lock":             nil,
			"requestedAt":      now.Format(time.RFC3339),
			"executeBefore":    now.Add(24 * time.Hour).Format(time.RFC3339),
			"meta":             map[string]any{"values": map[string]any{}},
			"inputHoldingCids": cids,
		}
		mergeCmd := map[string]any{
			"ExerciseCommand": map[string]any{
				"templateId": transferFactoryTemplateID,
				"contractId": tfResult.FactoryID,
				"choice":     "TransferFactory_Transfer",
				"choiceArgument": map[string]any{
					"expectedAdmin": primary.InstrumentAdmin,
					"transfer":      transferPayload,
					"extraArgs": map[string]any{
						"context": tfResult.ChoiceContextData,
						"meta":    map[string]any{"values": map[string]any{}},
					},
				},
			},
		}
		mergePayload := wallet.CommandPayload{
			Commands:           []any{mergeCmd},
			DisclosedContracts: tfResult.DisclosedContracts,
			ActAs:              []string{takerParty},
			ReadAs:             []string{takerParty},
		}
		if _, err := adapter.PrepareAndSubmit(mergePayload); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge self-transfer: %w", err)}
		}

		// Re-fetch holdings — the merge transaction is committed but the new
		// holding may not be visible immediately. Poll a few times.
		var single *wallet.HoldingContract
		for poll := 0; poll < 5; poll++ {
			if poll > 0 {
				time.Sleep(2 * time.Second)
			}
			allHoldings, err := adapter.GetHoldingContracts("")
			if err != nil {
				continue
			}
			s, _, selErr := checkMergeNeeded(allHoldings, activeQuote.AcceptContext.TakerLeg.Asset, activeQuote.AcceptContext.TakerLeg.Amount)
			if selErr != nil {
				continue
			}
			if s != nil {
				single = s
				break
			}
		}
		if single == nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("merge completed but still no single holding large enough")}
		}

		return quoteMergeCompleteMsg{single: single}
	}
}

// submitWithHoldingCmd calls executeAcceptContextSubmit with a pre-selected holding.
// Used after a merge to run the allocation step without re-fetching holdings.
func submitWithHoldingCmd(cfg config.AppConfig, activeQuote *ActiveQuote, single *wallet.HoldingContract) tea.Cmd {
	return func() tea.Msg {
		adapter := walletAdapterForConfig(cfg)
		if useLoopWalletProvider(cfg) && strings.TrimSpace(cfg.PrivateKeyHex) == "" {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("wallet private key not configured")}
		}
		if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("key error: %w", err)}
		}
		apiURL := resolveWalletAPIURL(cfg)
		takerParty := strings.TrimSpace(activeQuote.AcceptContext.TakerParty)
		if takerParty == "" {
			takerParty = strings.TrimSpace(cfg.WalletPartyID)
		}
		if err := adapter.Authenticate(takerParty, apiURL); err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("auth failed: %w", err)}
		}
		client := newQuoteClientFn(cfg.TrngleAPIURL, cfg.TrngleAPIKey)
		return executeAcceptContextSubmit(client, adapter, activeQuote, single, cfg.Network)
	}
}

const allocationInterfaceID = "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation"

// pollForNewAllocation queries the Allocation interface to find a new allocation
// that appeared after the allocate command. The Loop SDK returns minimal
// transaction results without a full transaction tree, so we query directly.
// Proven approach: new allocations typically appear within 2-4 seconds.
func pollForNewAllocation(adapter tradeWallet, original *wallet.HoldingContract, before map[string]bool) (string, string) {
	var lastErr error
	var lastCount int
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		allocs, err := adapter.GetHoldingContracts(allocationInterfaceID)
		if err != nil {
			lastErr = err
			continue
		}
		lastCount = len(allocs)
		for _, a := range allocs {
			if before[a.ContractID] {
				continue
			}
			return a.ContractID, ""
		}
	}
	if lastErr != nil {
		return "", fmt.Sprintf("poll failed after 10 attempts, last error: %v", lastErr)
	}
	return "", fmt.Sprintf("poll found %d allocations (%d before), none new after 10 attempts", lastCount, len(before))
}

// executeAcceptContextSubmit builds and submits the allocation + taker-confirm commands.
// If preSelected is non-nil it is used directly, skipping holding fetch and selection.
func executeAcceptContextSubmit(client core.QuoteClient, adapter tradeWallet, activeQuote *ActiveQuote, preSelected *wallet.HoldingContract, network string) quoteSubmitResultMsg {
	if activeQuote.AcceptContext == nil {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("missing accept context")}
	}
	ctx := activeQuote.AcceptContext

	// Pay any pending gas BEFORE fetching holdings. Gas payment consumes CC
	// holdings and creates new ones, so we must settle gas first to ensure
	// the holding CIDs we fetch are still valid when we submit the allocation.
	if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
		_ = gp.EnsureGasPaid()
	}

	var holding *wallet.HoldingContract
	if preSelected != nil {
		// Pre-selected holding may be stale after gas payment — re-fetch.
		holdings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		holding, err = selectHoldingForLeg(holdings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
	} else {
		holdings, err := adapter.GetHoldingContracts("")
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("fetch holdings: %w", err)}
		}
		holding, err = selectHoldingForLeg(holdings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
		if err != nil {
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
	}

	// Compute transfer leg ID (must match what the builder uses).
	transferLegID := fmt.Sprintf("leg-%s-%s-%s", ctx.TradeID, ctx.TakerParty, ctx.MakerParty)
	if ctx.TradeID == "" {
		transferLegID = fmt.Sprintf("leg-%s", ctx.QuoteID)
	}

	// Resolve the AllocationFactory via the Canton registry (mirrors the TypeScript SDK).
	// Get the auth token from the adapter if it exposes one.
	authToken := ""
	if tokenHolder, ok := adapter.(interface{ AuthToken() string }); ok {
		authToken = tokenHolder.AuthToken()
	}

	var factoryCtx *confirmbuilder.FactoryContext
	if result, registryErr := fetchRegistryFactoryFn(authToken, *holding, ctx, transferLegID, network); registryErr == nil {
		factoryCtx = &confirmbuilder.FactoryContext{
			FactoryID:          result.FactoryID,
			ExpectedAdmin:      result.ExpectedAdmin,
			ChoiceContextData:  result.ChoiceContextData,
			DisclosedContracts: result.DisclosedContracts,
		}
	} else if strings.TrimSpace(ctx.FactoryCID) != "" {
		// Registry unavailable — fall back to the operator-provided factory CID.
		// The allocation will proceed without registry-sourced disclosed contracts
		// and choiceContextData, which may or may not work depending on the asset.
		factoryCtx = &confirmbuilder.FactoryContext{
			FactoryID: ctx.FactoryCID,
		}
	} else {
		return quoteSubmitResultMsg{
			quoteID:   activeQuote.ID,
			submitErr: fmt.Errorf("allocation factory unavailable (registry: %v; operator provided none)", registryErr),
		}
	}

	build, err := confirmbuilder.BuildTakerConfirmPayload(*ctx, *holding, factoryCtx)
	if err != nil {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}
	if len(build.Payload.Commands) < 2 {
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("invalid confirm payload: expected allocate + confirm commands")}
	}

	allocatePayload := build.Payload
	allocatePayload.Commands = []any{build.Payload.Commands[0]}
	allocatePayload.SynchronizerID = ""

	// Helper to report errors to the operator for immediate cleanup.
	reportErr := func(phase, allocID string, submitErr error) {
		_ = client.ReportError(context.Background(), activeQuote.ID, core.ErrorReport{
			AcceptContextID: ctx.AcceptContextID,
			Phase:           phase,
			ErrorMessage:    submitErr.Error(),
			AllocationID:    allocID,
		})
	}

	// Allocate with retry: if the first attempt's allocation isn't found (Canton
	// silently rejects stale holding CIDs after gas payment), re-fetch holdings,
	// rebuild the payload, and try again.
	var allocationID string
	for allocAttempt := 0; allocAttempt < 2; allocAttempt++ {
		if allocAttempt > 0 {
			// Gas payment may have consumed the holding — re-fetch everything.
			if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
				_ = gp.EnsureGasPaid()
			}
			time.Sleep(1 * time.Second)
			freshHoldings, fetchErr := adapter.GetHoldingContracts("")
			if fetchErr != nil {
				reportErr("allocation", "", fetchErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: fmt.Errorf("retry fetch holdings: %w", fetchErr)}
			}
			freshHolding, selErr := selectHoldingForLeg(freshHoldings, ctx.TakerLeg.Asset, ctx.TakerLeg.Amount)
			if selErr != nil {
				reportErr("allocation", "", selErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: selErr}
			}
			holding = freshHolding
			retryBuild, buildErr := confirmbuilder.BuildTakerConfirmPayload(*ctx, *holding, factoryCtx)
			if buildErr != nil {
				reportErr("allocation", "", buildErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: buildErr}
			}
			build = retryBuild
			allocatePayload = build.Payload
			allocatePayload.Commands = []any{build.Payload.Commands[0]}
			allocatePayload.SynchronizerID = ""
		}

		// Snapshot allocations before allocate so we can diff to find the new one.
		allocsBefore, _ := adapter.GetHoldingContracts(allocationInterfaceID)
		allocCIDsBefore := make(map[string]bool, len(allocsBefore))
		for _, a := range allocsBefore {
			allocCIDsBefore[a.ContractID] = true
		}

		allocateTx, err := adapter.PrepareAndSubmit(allocatePayload)
		if err != nil {
			reportErr("allocation", "", err)
			return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
		}
		_, allocationID = confirmbuilder.ParseConfirmResult(allocateTx.TransactionTree)

		if strings.TrimSpace(allocationID) == "" {
			var pollDiag string
			allocationID, pollDiag = pollForNewAllocation(adapter, holding, allocCIDsBefore)
			if strings.TrimSpace(allocationID) == "" {
				if allocAttempt == 0 {
					continue // retry with fresh holdings
				}
				parseErr := fmt.Errorf("allocation submit succeeded but allocation CID not found (%s)", pollDiag)
				reportErr("allocation", "", parseErr)
				return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: parseErr}
			}
		}
		break
	}

	// Pay any gas incurred by the allocate transaction before submitting confirm.
	// On Loop testnet, gas is charged after each transaction and blocks the next.
	if gp, ok := adapter.(interface{ EnsureGasPaid() error }); ok {
		_ = gp.EnsureGasPaid()
	}

	confirmPayload := build.Payload
	confirmPayload.Commands = []any{build.Payload.Commands[1]}
	confirmPayload.SynchronizerID = ""
	if err := setConfirmAllocationCID(confirmPayload.Commands[0], allocationID, "allocationCid"); err != nil {
		reportErr("taker_confirm", allocationID, err)
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}

	confirmTx, err := adapter.PrepareAndSubmit(confirmPayload)
	if err != nil && strings.Contains(err.Error(), "AllocationCID") {
		if patchErr := setConfirmAllocationCID(confirmPayload.Commands[0], allocationID, "allocationCID"); patchErr == nil {
			confirmTx, err = adapter.PrepareAndSubmit(confirmPayload)
		}
	}
	if err != nil && isContractNotFoundErr(err) {
		currentTemplateID := extractConfirmTemplateID(confirmPayload.Commands[0])
		for _, candidate := range confirmTemplateCandidates(currentTemplateID) {
			if candidate == currentTemplateID {
				continue
			}
			if !setConfirmTemplateID(confirmPayload.Commands[0], candidate) {
				continue
			}
			confirmTx, err = adapter.PrepareAndSubmit(confirmPayload)
			if err == nil || !isContractNotFoundErr(err) {
				break
			}
		}
	}
	if err != nil {
		reportErr("taker_confirm", allocationID, err)
		return quoteSubmitResultMsg{quoteID: activeQuote.ID, submitErr: err}
	}

	tradeCID, _ := confirmbuilder.ParseConfirmResult(confirmTx.TransactionTree)
	if tradeCID == "" {
		tradeCID = ctx.TradeCID
	}

	// If the taker allocated CC/Amulet, query the LockedAmulet blob and pass
	// it to the operator. The operator cannot see taker LockedAmulets (stakeholders
	// are [DSO, sender]) and needs the blob as a disclosed contract for settlement.
	confirmRequest := map[string]any{
		"accept_context_id": ctx.AcceptContextID,
	}
	if finder, ok := adapter.(interface {
		GetLockedAmuletForAllocation(string) (*loop.LockedAmuletBlob, error)
	}); ok && strings.TrimSpace(allocationID) != "" {
		if blob, err := finder.GetLockedAmuletForAllocation(allocationID); err == nil && blob != nil {
			confirmRequest["locked_amulet"] = map[string]any{
				"contract_id":        blob.ContractID,
				"template_id":        blob.TemplateID,
				"created_event_blob": blob.CreatedEventBlob,
				"synchronizer_id":    blob.SynchronizerID,
			}
		}
		// Non-fatal: CC/Amulet allocations need this, but utility token
		// allocations (CBTC, USDCx) don't have LockedAmulets.
	}
	confirmErr := client.ConfirmQuote(context.Background(), activeQuote.ID, confirmRequest)

	return quoteSubmitResultMsg{
		quoteID:      activeQuote.ID,
		tradeCID:     tradeCID,
		allocationID: allocationID,
		confirmErr:   confirmErr,
		submitErr:    nil,
	}
}

func setConfirmAllocationCID(cmd any, allocationID, key string) error {
	root, ok := cmd.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: expected object")
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: missing ExerciseCommand")
	}
	args, ok := exercise["choiceArgument"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid confirm command payload: missing choiceArgument")
	}
	delete(args, "allocationCid")
	delete(args, "allocationCID")
	args[key] = allocationID
	return nil
}

func isContractNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "contract_not_found") ||
		strings.Contains(msg, "contract not found") ||
		strings.Contains(msg, "package_names_not_found") ||
		strings.Contains(msg, "package names not found")
}

func setConfirmTemplateID(cmd any, templateID string) bool {
	root, ok := cmd.(map[string]any)
	if !ok {
		return false
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return false
	}
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return false
	}
	exercise["templateId"] = templateID
	return true
}

func extractConfirmTemplateID(cmd any) string {
	root, ok := cmd.(map[string]any)
	if !ok {
		return ""
	}
	exercise, ok := root["ExerciseCommand"].(map[string]any)
	if !ok {
		return ""
	}
	templateID, _ := exercise["templateId"].(string)
	return strings.TrimSpace(templateID)
}

func confirmTemplateCandidates(templateID string) []string {
	base := strings.TrimSpace(templateID)
	if base == "" {
		base = "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2"
	}
	noHash := strings.TrimPrefix(base, "#")

	candidates := []string{
		base,
		noHash,
		"#" + noHash,
	}

	if strings.HasSuffix(noHash, ":V2.TradeConfirmV2:TradeConfirmV2") {
		prefix := strings.TrimSuffix(noHash, ":V2.TradeConfirmV2:TradeConfirmV2")
		candidates = append(candidates,
			prefix+":TradeConfirmV2:TradeConfirmV2",
			"#"+prefix+":TradeConfirmV2:TradeConfirmV2",
		)
	}
	if strings.HasSuffix(noHash, ":TradeConfirmV2:TradeConfirmV2") {
		prefix := strings.TrimSuffix(noHash, ":TradeConfirmV2:TradeConfirmV2")
		candidates = append(candidates,
			prefix+":V2.TradeConfirmV2:TradeConfirmV2",
			"#"+prefix+":V2.TradeConfirmV2:TradeConfirmV2",
		)
	}

	if strings.HasPrefix(noHash, "trngle-cip56:") {
		replaced := strings.Replace(noHash, "trngle-cip56:", "trngle-cip56-v2:", 1)
		candidates = append(candidates, replaced, "#"+replaced)
	}
	if strings.HasPrefix(noHash, "trngle-cip56-v2:") {
		replaced := strings.Replace(noHash, "trngle-cip56-v2:", "trngle-cip56:", 1)
		candidates = append(candidates, replaced, "#"+replaced)
	}

	candidates = append(candidates,
		"#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2",
		"#trngle-cip56:TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56:TradeConfirmV2:TradeConfirmV2",
		"#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
		"trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
	)

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// checkMergeNeeded evaluates available holdings for a given asset/amount.
// Returns (single, nil, nil) if one holding covers the required amount.
// Returns (nil, allMatching, nil) if total across unlocked holdings is sufficient but no single one is.
// Returns (nil, nil, err) if total is insufficient.
func checkMergeNeeded(holdings []wallet.HoldingContract, asset, amount string) (*wallet.HoldingContract, []wallet.HoldingContract, error) {
	if len(holdings) == 0 {
		return nil, nil, fmt.Errorf("no holdings available for signing")
	}

	targetAsset := strings.ToUpper(strings.TrimSpace(asset))
	if targetAsset == "CC" {
		targetAsset = "AMULET"
	}

	required, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || required <= 0 {
		return nil, nil, fmt.Errorf("invalid taker amount %q", amount)
	}

	var matching []wallet.HoldingContract
	var total float64
	for _, h := range holdings {
		if h.IsLocked {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(h.InstrumentID)) != targetAsset {
			continue
		}
		avail, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64)
		if err != nil {
			continue
		}
		matching = append(matching, h)
		total += avail
	}

	if len(matching) == 0 {
		return nil, nil, fmt.Errorf("no unlocked %s holdings found", asset)
	}

	// Sort largest first so the single-holding fast path picks the best fit.
	sort.Slice(matching, func(i, j int) bool {
		ai, errI := strconv.ParseFloat(strings.TrimSpace(matching[i].Amount), 64)
		aj, errJ := strconv.ParseFloat(strings.TrimSpace(matching[j].Amount), 64)
		if errI != nil || errJ != nil {
			return errJ != nil // push unparseable amounts to the end
		}
		return ai > aj
	})

	// Fast path: a single holding covers the requirement.
	if avail, err := strconv.ParseFloat(strings.TrimSpace(matching[0].Amount), 64); err == nil && avail >= required {
		h := matching[0]
		return &h, nil, nil
	}

	// Merge path: total is enough but no single holding is.
	if total >= required {
		return nil, matching, nil
	}

	return nil, nil, fmt.Errorf("insufficient %s balance: have %.10f, need %s", asset, total, amount)
}

// selectHoldingForLeg wraps checkMergeNeeded for callers that only want the single-holding path.
// Used by tests and the normal (non-merge) execution path.
func selectHoldingForLeg(holdings []wallet.HoldingContract, asset, amount string) (*wallet.HoldingContract, error) {
	single, _, err := checkMergeNeeded(holdings, asset, amount)
	return single, err
}
