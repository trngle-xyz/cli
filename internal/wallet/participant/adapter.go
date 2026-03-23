package participant

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trngle-xyz/cli/internal/wallet"
)

const defaultHoldingInterfaceID = "#splice-api-token-holding-v1:Splice.Api.Token.HoldingV1:Holding"

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type Adapter struct {
	partyID   string
	ledgerURL string

	token      string
	tokenExp   time.Time
	httpClient *http.Client
}

func NewAdapter() *Adapter {
	return &Adapter{
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) PartyID() string   { return a.partyID }
func (a *Adapter) PublicKey() string { return "" }
func (a *Adapter) APIURL() string    { return a.ledgerURL }
func (a *Adapter) AuthToken() string { return a.token }

func (a *Adapter) Initialize(_ string) error {
	return nil
}

func (a *Adapter) Authenticate(partyID, apiURL string) error {
	pid := strings.TrimSpace(partyID)
	if pid == "" {
		return fmt.Errorf("party id is required")
	}
	a.partyID = pid

	ledgerURL := strings.TrimSpace(apiURL)
	if ledgerURL == "" {
		ledgerURL = firstNonEmpty(
			envOrDotEnv("TRNGLE_PARTICIPANT_LEDGER_URL"),
			envOrDotEnv("OPERATOR_CANTON_LEDGER_URL"),
			envOrDotEnv("CANTON_LEDGER_URL"),
		)
	}
	if ledgerURL == "" {
		ledgerURL = "http://localhost:7575/api/json-api"
	}
	a.ledgerURL = strings.TrimRight(ledgerURL, "/")

	_, err := a.ensureToken(true)
	return err
}

func (a *Adapter) GetBalances() ([]wallet.Balance, error) {
	holdings, err := a.GetHoldingContracts("")
	if err != nil {
		return nil, err
	}

	type key struct {
		admin string
		id    string
	}
	by := map[key]float64{}
	for _, h := range holdings {
		if h.IsLocked {
			continue
		}
		amt, err := strconv.ParseFloat(strings.TrimSpace(h.Amount), 64)
		if err != nil {
			continue
		}
		k := key{admin: h.InstrumentAdmin, id: h.InstrumentID}
		by[k] += amt
	}

	out := make([]wallet.Balance, 0, len(by))
	for k, v := range by {
		out = append(out, wallet.Balance{
			Symbol:          k.id,
			InstrumentID:    k.id,
			InstrumentAdmin: k.admin,
			Amount:          strconv.FormatFloat(v, 'f', 10, 64),
		})
	}
	return out, nil
}

func (a *Adapter) GetHoldingContracts(interfaceID string) ([]wallet.HoldingContract, error) {
	if strings.TrimSpace(a.partyID) == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	if strings.TrimSpace(a.ledgerURL) == "" {
		return nil, fmt.Errorf("missing ledger url")
	}

	if strings.TrimSpace(interfaceID) == "" {
		interfaceID = defaultHoldingInterfaceID
	}

	token, err := a.ensureToken(false)
	if err != nil {
		return nil, err
	}

	offsetReq, _ := http.NewRequest(http.MethodGet, a.ledgerURL+"/v2/state/ledger-end", nil)
	offsetReq.Header.Set("Authorization", "Bearer "+token)
	offsetResp, err := a.httpClient.Do(offsetReq)
	if err != nil {
		return nil, fmt.Errorf("fetch ledger end: %w", err)
	}
	defer offsetResp.Body.Close()

	offsetBody, _ := io.ReadAll(offsetResp.Body)
	if offsetResp.StatusCode < 200 || offsetResp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch ledger end failed (%d): %s", offsetResp.StatusCode, previewBody(offsetBody))
	}

	var offsetPayload map[string]any
	if err := json.Unmarshal(offsetBody, &offsetPayload); err != nil {
		return nil, fmt.Errorf("parse ledger end from %s/v2/state/ledger-end: %w (body=%s)", a.ledgerURL, err, previewBody(offsetBody))
	}
	offset := scalarToString(offsetPayload["offset"])
	if strings.TrimSpace(offset) == "" {
		return nil, fmt.Errorf("ledger end offset missing")
	}

	payload := map[string]any{
		"filter": map[string]any{
			"filtersByParty": map[string]any{
				a.partyID: map[string]any{
					"cumulative": []any{
						map[string]any{
							"identifierFilter": map[string]any{
								"InterfaceFilter": map[string]any{
									"value": map[string]any{
										"interfaceId":             interfaceID,
										"includeInterfaceView":    true,
										"includeCreatedEventBlob": true,
									},
								},
							},
						},
					},
				},
			},
		},
		"verbose":        true,
		"activeAtOffset": offset,
	}

	body, _ := json.Marshal(payload)
	contractsReq, _ := http.NewRequest(http.MethodPost, a.ledgerURL+"/v2/state/active-contracts", bytes.NewReader(body))
	contractsReq.Header.Set("Authorization", "Bearer "+token)
	contractsReq.Header.Set("Content-Type", "application/json")

	contractsResp, err := a.httpClient.Do(contractsReq)
	if err != nil {
		return nil, fmt.Errorf("fetch active contracts: %w", err)
	}
	defer contractsResp.Body.Close()

	contractsBody, _ := io.ReadAll(contractsResp.Body)
	if contractsResp.StatusCode < 200 || contractsResp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch active contracts failed (%d): %s", contractsResp.StatusCode, previewBody(contractsBody))
	}

	var entries []map[string]any
	if err := json.Unmarshal(contractsBody, &entries); err != nil {
		return nil, fmt.Errorf("parse active contracts from %s/v2/state/active-contracts: %w (body=%s)", a.ledgerURL, err, previewBody(contractsBody))
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
		if views, ok := created["interfaceViews"].([]any); ok && len(views) > 0 {
			if first, ok := views[0].(map[string]any); ok {
				if viewValue, ok := first["viewValue"].(map[string]any); ok {
					amount, _ = viewValue["amount"].(string)
					if instrument, ok := viewValue["instrumentId"].(map[string]any); ok {
						instrumentID, _ = instrument["id"].(string)
						instrumentAdmin, _ = instrument["admin"].(string)
					}
					if lock, ok := viewValue["lock"].(map[string]any); ok && lock != nil {
						if holders, ok := lock["holders"].([]any); ok && len(holders) > 0 {
							isLocked = true
						}
					}
				}
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
			InstrumentAdmin:  instrumentAdmin,
			CreatedEventBlob: createdBlob,
			SynchronizerID:   synchronizerID,
			IsLocked:         isLocked,
		})
	}
	return out, nil
}

func (a *Adapter) PrepareAndSubmit(payload wallet.CommandPayload) (*wallet.TransactionResult, error) {
	if strings.TrimSpace(a.partyID) == "" {
		return nil, fmt.Errorf("not authenticated")
	}
	if strings.TrimSpace(a.ledgerURL) == "" {
		return nil, fmt.Errorf("missing ledger url")
	}

	token, err := a.ensureToken(false)
	if err != nil {
		return nil, err
	}

	req := map[string]any{
		"commandId": "cmd-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"actAs":     payload.ActAs,
		"readAs":    payload.ReadAs,
		"commands":  payload.Commands,
	}
	if len(payload.DisclosedContracts) > 0 {
		req["disclosedContracts"] = payload.DisclosedContracts
	}
	if syncID := strings.TrimSpace(payload.SynchronizerID); syncID != "" {
		req["synchronizerId"] = syncID
	}
	if len(payload.PackageIDSelectionPreference) > 0 {
		req["packageIdSelectionPreference"] = payload.PackageIDSelectionPreference
	}

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest(http.MethodPost, a.ledgerURL+"/v2/commands/submit-and-wait-for-transaction-tree", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("submit transaction: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("submit transaction failed (%d): %s", resp.StatusCode, previewBody(respBody))
	}

	var tree any
	if err := json.Unmarshal(respBody, &tree); err != nil {
		return nil, fmt.Errorf("parse submit response: %w (body=%s)", err, previewBody(respBody))
	}

	return &wallet.TransactionResult{
		CommandID:       req["commandId"].(string),
		TransactionTree: tree,
	}, nil
}

func (a *Adapter) GetPendingTransfers() ([]wallet.PendingTransfer, error) {
	return []wallet.PendingTransfer{}, nil
}

func (a *Adapter) FetchParticipantPartyID() (string, error) {
	if strings.TrimSpace(a.ledgerURL) == "" {
		return "", fmt.Errorf("missing ledger url")
	}
	token, err := a.ensureToken(false)
	if err != nil {
		return "", err
	}

	req, _ := http.NewRequest(http.MethodGet, a.ledgerURL+"/v2/parties/participant-id", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch participant id: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch participant id failed (%d): %s", resp.StatusCode, previewBody(body))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse participant id response: %w (body=%s)", err, previewBody(body))
	}
	partyID := firstNonEmpty(
		scalarToString(payload["participantId"]),
		scalarToString(payload["participant_id"]),
	)
	if strings.TrimSpace(partyID) == "" {
		return "", fmt.Errorf("participant id missing in response")
	}
	return partyID, nil
}

func (a *Adapter) AcceptTransfer(_ string) (*wallet.TransactionResult, error) {
	return nil, fmt.Errorf("accept transfer not implemented for participant adapter")
}

func (a *Adapter) ensureToken(force bool) (string, error) {
	if !force && strings.TrimSpace(a.token) != "" && time.Now().Before(a.tokenExp.Add(-15*time.Second)) {
		return a.token, nil
	}

	clientID := firstNonEmpty(envOrDotEnv("OPERATOR_AUTH0_CLIENT_ID"), envOrDotEnv("AUTH0_CLIENT_ID"))
	clientSecret := firstNonEmpty(envOrDotEnv("OPERATOR_AUTH0_CLIENT_SECRET"), envOrDotEnv("AUTH0_CLIENT_SECRET"))
	audience := firstNonEmpty(envOrDotEnv("OPERATOR_AUTH0_AUDIENCE"), envOrDotEnv("AUTH0_AUDIENCE"))
	domain := firstNonEmpty(envOrDotEnv("OPERATOR_AUTH0_DOMAIN"), envOrDotEnv("AUTH0_DOMAIN"))
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	domain = strings.TrimSuffix(strings.TrimSpace(domain), "/")

	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" || strings.TrimSpace(audience) == "" || strings.TrimSpace(domain) == "" {
		return "", fmt.Errorf("missing Auth0 configuration (need OPERATOR_AUTH0_* or AUTH0_* in env or repo .env files)")
	}

	reqBody, _ := json.Marshal(map[string]any{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"audience":      audience,
		"grant_type":    "client_credentials",
	})
	resp, err := a.httpClient.Post(domain+"/oauth/token", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("fetch auth token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch auth token failed (%d): %s", resp.StatusCode, previewBody(body))
	}

	var tok oauthTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("parse auth token response: %w", err)
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return "", fmt.Errorf("auth token response missing access_token")
	}
	expIn := tok.ExpiresIn
	if expIn <= 0 {
		expIn = 300
	}

	a.token = tok.AccessToken
	a.tokenExp = time.Now().Add(time.Duration(expIn) * time.Second)
	return a.token, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
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
	if len(s) > 180 {
		s = s[:180] + "..."
	}
	return s
}

func scalarToString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

var (
	dotEnvOnce sync.Once
	dotEnvVals map[string]string
)

func envOrDotEnv(key string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	dotEnvOnce.Do(func() {
		dotEnvVals = loadDotEnvValues(dotEnvCandidates())
	})
	if dotEnvVals == nil {
		return ""
	}
	return strings.TrimSpace(dotEnvVals[key])
}

func dotEnvCandidates() []string {
	candidates := make([]string, 0, 10)

	for _, k := range []string{"TRNGLE_ENV_FILE", "OPERATOR_ENV_FILE"} {
		if configured := strings.TrimSpace(os.Getenv(k)); configured != "" {
			candidates = append(candidates, configured)
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, ".env"),
			filepath.Join(cwd, "scripts", ".env"),
			filepath.Join(cwd, "..", ".env"),
			filepath.Join(cwd, "..", "scripts", ".env"),
		)
	}

	if _, file, _, ok := runtime.Caller(0); ok {
		tuiRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		repoRoot := filepath.Clean(filepath.Join(tuiRoot, ".."))
		candidates = append(candidates,
			filepath.Join(tuiRoot, ".env"),
			filepath.Join(repoRoot, ".env"),
			filepath.Join(repoRoot, "scripts", ".env"),
			filepath.Join(repoRoot, "operator", ".env"),
		)
	}

	unique := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		abs := c
		if a, err := filepath.Abs(c); err == nil {
			abs = a
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		unique = append(unique, abs)
	}
	return unique
}

func loadDotEnvValues(candidates []string) map[string]string {
	out := map[string]string{}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		fileVals := parseDotEnvFile(candidate)
		for k, v := range fileVals {
			if _, exists := out[k]; !exists {
				out[k] = v
			}
		}
	}
	return out
}

func parseDotEnvFile(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer f.Close()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			continue
		}
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = strings.TrimSpace(val)
	}
	return out
}
