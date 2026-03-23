//go:build live

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet/loop"
)

func TestLiveOperatorTakerFlow(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TRNGLE_LIVE_TEST")) != "1" {
		t.Skip("set TRNGLE_LIVE_TEST=1 to run live operator+taker flow")
	}

	operatorURL := strings.TrimSpace(os.Getenv("TRNGLE_OPERATOR_URL"))
	if operatorURL == "" {
		operatorURL = "http://localhost:18080"
	}
	fromAsset := envOr("TRNGLE_LIVE_FROM_ASSET", "CC")
	toAsset := envOr("TRNGLE_LIVE_TO_ASSET", "CBTC")
	amount := envOr("TRNGLE_LIVE_AMOUNT", "0.0001000000")

	cfg, err := loadLiveConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.PrivateKeyHex) == "" {
		t.Fatalf("missing private_key_hex in TUI config")
	}
	if strings.TrimSpace(cfg.WalletPartyID) == "" {
		t.Fatalf("missing wallet_party_id in TUI config")
	}

	loopAPIURL := strings.TrimSpace(os.Getenv("TRNGLE_LOOP_API_URL"))
	if loopAPIURL == "" {
		n := strings.ToLower(strings.TrimSpace(cfg.Network))
		if n == "" {
			n = "mainnet"
		}
		loopAPIURL = loop.NetworkAPIURLs[n]
		if strings.TrimSpace(loopAPIURL) == "" {
			t.Fatalf("no loop api url for network=%s; set TRNGLE_LOOP_API_URL", cfg.Network)
		}
	}

	adapter := loop.NewAdapter()
	if err := adapter.Initialize(cfg.PrivateKeyHex); err != nil {
		t.Fatalf("wallet initialize: %v", err)
	}
	if err := adapter.Authenticate(cfg.WalletPartyID, loopAPIURL); err != nil {
		t.Fatalf("wallet authenticate: %v", err)
	}

	quoteClient := core.NewQuoteAPIClient(operatorURL)

	quoteResp, err := quoteClient.RequestQuote(context.Background(), fromAsset, toAsset, amount, cfg.WalletPartyID)
	if err != nil {
		t.Fatalf("request quote: %v", err)
	}
	if quoteResp == nil || strings.TrimSpace(quoteResp.ID) == "" {
		t.Fatalf("request quote returned empty quote")
	}
	t.Logf("quote_id=%s", quoteResp.ID)

	acceptCtx, err := quoteClient.AcceptQuoteContext(context.Background(), quoteResp.ID)
	if err != nil {
		t.Fatalf("accept quote context: %v", err)
	}
	if acceptCtx == nil || strings.TrimSpace(acceptCtx.TradeID) == "" || strings.TrimSpace(acceptCtx.TradeCID) == "" {
		t.Fatalf("accept returned incomplete trade context: %+v", acceptCtx)
	}
	if strings.TrimSpace(acceptCtx.AcceptContextID) == "" {
		t.Fatalf("accept returned empty accept_context_id")
	}
	t.Logf("accepted quote_id=%s trade_id=%s trade_cid=%s", quoteResp.ID, acceptCtx.TradeID, acceptCtx.TradeCID)

	partyForQuery := strings.TrimSpace(acceptCtx.TakerParty)
	if partyForQuery == "" {
		partyForQuery = strings.TrimSpace(cfg.WalletPartyID)
	}
	acceptView, err := waitForTradeState(adapter.APIURL(), adapter.AuthToken(), partyForQuery, acceptCtx.TradeTemplateID, acceptCtx.TradeID, "PendingTaker", 20*time.Second)
	if err != nil {
		t.Fatalf("verify trade after accept (PendingTaker): %v", err)
	}
	if acceptView.TradeCID != "" && acceptView.TradeCID != acceptCtx.TradeCID {
		t.Fatalf("accept trade_cid mismatch: accept=%s ledger=%s", acceptCtx.TradeCID, acceptView.TradeCID)
	}
	t.Logf("ledger after accept: trade_id=%s state=%s", acceptCtx.TradeID, acceptView.State)

	result := executeAcceptContextSubmit(quoteClient, adapter, &ActiveQuote{
		ID:            quoteResp.ID,
		AcceptContext: acceptCtx,
	}, nil, "mainnet")
	if result.submitErr != nil {
		t.Fatalf("taker submit failed: %v", result.submitErr)
	}
	if result.confirmErr != nil {
		t.Fatalf("operator confirm failed: %v", result.confirmErr)
	}
	t.Logf("taker submit ok: quote_id=%s trade_cid=%s allocation_id=%s", result.quoteID, result.tradeCID, result.allocationID)

	confirmView, err := waitForTradeState(adapter.APIURL(), adapter.AuthToken(), partyForQuery, acceptCtx.TradeTemplateID, acceptCtx.TradeID, "PendingMaker", 45*time.Second)
	if err != nil {
		t.Fatalf("verify trade after taker confirm (PendingMaker): %v", err)
	}
	if strings.TrimSpace(confirmView.TakerAllocationCID) == "" {
		t.Fatalf("expected taker allocation cid to be set in PendingMaker state")
	}
	t.Logf("ledger after confirm: trade_id=%s state=%s taker_allocation_cid=%s", acceptCtx.TradeID, confirmView.State, confirmView.TakerAllocationCID)
}

type liveTradeView struct {
	TradeCID           string
	State              string
	TakerAllocationCID string
}

func waitForTradeState(apiURL, bearerToken, partyID, templateID, tradeID, expectedState string, timeout time.Duration) (liveTradeView, error) {
	deadline := time.Now().Add(timeout)
	var lastState string
	for time.Now().Before(deadline) {
		view, found, err := findTrade(apiURL, bearerToken, partyID, templateID, tradeID)
		if err != nil {
			return liveTradeView{}, err
		}
		if found {
			lastState = view.State
			if strings.EqualFold(view.State, expectedState) {
				return view, nil
			}
		}
		time.Sleep(1200 * time.Millisecond)
	}
	if lastState == "" {
		return liveTradeView{}, fmt.Errorf("trade %s not found for party=%s", tradeID, partyID)
	}
	return liveTradeView{}, fmt.Errorf("trade %s found but state=%s, expected=%s", tradeID, lastState, expectedState)
}

func findTrade(apiURL, bearerToken, partyID, templateID, tradeID string) (liveTradeView, bool, error) {
	offset, err := getLedgerEnd(apiURL, bearerToken)
	if err != nil {
		return liveTradeView{}, false, err
	}
	entries, err := queryActiveContracts(apiURL, bearerToken, partyID, templateID, offset)
	if err != nil {
		return liveTradeView{}, false, err
	}
	for _, entry := range entries {
		created := createdEventFromActiveEntry(entry)
		if len(created) == 0 {
			continue
		}
		args := asMap(created["createArguments"])
		if len(args) == 0 {
			continue
		}
		if readString(args["tradeId"]) != tradeID {
			continue
		}
		return liveTradeView{
			TradeCID:           readString(created["contractId"]),
			State:              stateTag(args["state"]),
			TakerAllocationCID: optionalCID(args["takerAllocationCid"]),
		}, true, nil
	}
	return liveTradeView{}, false, nil
}

func getLedgerEnd(apiURL, bearerToken string) (string, error) {
	raw, err := callLedgerJSON(http.MethodGet, apiURL+"/v2/state/ledger-end", bearerToken, nil)
	if err != nil {
		return "", err
	}
	offset := readString(asMap(raw)["offset"])
	if strings.TrimSpace(offset) == "" {
		if rawMap := asMap(raw); rawMap != nil {
			offset = strings.TrimSpace(fmt.Sprint(rawMap["offset"]))
		}
	}
	if strings.TrimSpace(offset) == "" {
		return "", fmt.Errorf("ledger-end response missing offset")
	}
	return offset, nil
}

func queryActiveContracts(apiURL, bearerToken, partyID, templateID, offset string) ([]any, error) {
	body := map[string]any{
		"filter": map[string]any{
			"filtersByParty": map[string]any{
				partyID: map[string]any{
					"cumulative": []any{
						map[string]any{
							"identifierFilter": map[string]any{
								"TemplateFilter": map[string]any{
									"value": map[string]any{
										"templateId":              templateID,
										"includeCreatedEventBlob": false,
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
	raw, err := callLedgerJSON(http.MethodPost, apiURL+"/v2/state/active-contracts", bearerToken, body)
	if err != nil {
		return nil, err
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("active-contracts response is not an array")
	}
	return arr, nil
}

func callLedgerJSON(method, url, bearerToken string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		enc, _ := json.Marshal(body)
		reader = bytes.NewReader(enc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return nil, fmt.Errorf("ledger request %s %s failed: status=%d body=%s", method, url, resp.StatusCode, msg)
	}

	var decoded any
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("parse ledger response %s %s: %w", method, url, err)
	}
	return decoded, nil
}

func createdEventFromActiveEntry(entry any) map[string]any {
	m := asMap(entry)
	contractEntry := asMap(m["contractEntry"])
	active := asMap(contractEntry["JsActiveContract"])
	return asMap(active["createdEvent"])
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func readString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s := readString(t["party"]); s != "" {
			return s
		}
		if s := readString(t["text"]); s != "" {
			return s
		}
		if s := readString(t["value"]); s != "" {
			return s
		}
		if s := readString(t["contractId"]); s != "" {
			return s
		}
		if len(t) == 1 {
			for _, vv := range t {
				if s := readString(vv); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func stateTag(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if tag := readString(t["tag"]); tag != "" {
			return tag
		}
		if tag := readString(t["constructor"]); tag != "" {
			return tag
		}
		if value := t["value"]; value != nil {
			if s := readString(value); s != "" {
				return s
			}
			vm := asMap(value)
			if len(vm) == 1 {
				for k := range vm {
					return k
				}
			}
		}
		if len(t) == 1 {
			for k := range t {
				return k
			}
		}
	}
	return ""
}

func optionalCID(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if some := t["Some"]; some != nil {
			return readString(some)
		}
		tag := strings.TrimSpace(readString(t["tag"]))
		if strings.EqualFold(tag, "Some") {
			return readString(t["value"])
		}
		if len(t) == 1 {
			for k, vv := range t {
				if strings.EqualFold(k, "Some") {
					return readString(vv)
				}
			}
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func loadLiveConfig() (config.AppConfig, error) {
	if p := strings.TrimSpace(os.Getenv("TRNGLE_CONFIG")); p != "" {
		return config.Load(p)
	}
	defaultPath, err := config.DefaultConfigPath()
	if err != nil {
		return config.AppConfig{}, err
	}
	return config.Load(defaultPath)
}
