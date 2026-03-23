//go:build live

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/confirmbuilder"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
	"github.com/trngle-xyz/cli/internal/wallet/participant"
)

func TestLiveOperatorTakerTraceFlow(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TRNGLE_LIVE_TEST")) != "1" {
		t.Skip("set TRNGLE_LIVE_TEST=1 to run live operator+taker trace flow")
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

	quoteParty := strings.TrimSpace(os.Getenv("TRNGLE_LIVE_QUOTE_PARTY"))
	if quoteParty == "" {
		quoteParty = resolveQuotePartyID(cfg, strings.TrimSpace(cfg.WalletPartyID))
	}
	quoteClient := core.NewQuoteAPIClient(operatorURL)

	quoteResp, err := quoteClient.RequestQuote(context.Background(), fromAsset, toAsset, amount, quoteParty)
	if err != nil {
		t.Fatalf("request quote: %v", err)
	}
	if quoteResp == nil || strings.TrimSpace(quoteResp.ID) == "" {
		t.Fatalf("request quote returned empty quote")
	}
	t.Logf("quote_id=%s from=%s to=%s amount=%s quote_party=%s", quoteResp.ID, fromAsset, toAsset, amount, quoteParty)

	acceptCtx, err := quoteClient.AcceptQuoteContext(context.Background(), quoteResp.ID)
	if err != nil {
		t.Fatalf("accept quote context: %v", err)
	}
	if acceptCtx == nil {
		t.Fatalf("accept returned nil context")
	}
	t.Logf("accept quote_id=%s trade_id=%s trade_cid=%s template=%s taker_party=%s",
		acceptCtx.QuoteID, acceptCtx.TradeID, acceptCtx.TradeCID, acceptCtx.TradeTemplateID, acceptCtx.TakerParty)

	adapter := participant.NewAdapter()
	if err := adapter.Initialize(strings.TrimSpace(cfg.PrivateKeyHex)); err != nil {
		t.Fatalf("participant adapter init: %v", err)
	}
	authParty := strings.TrimSpace(acceptCtx.TakerParty)
	if authParty == "" {
		authParty = strings.TrimSpace(cfg.WalletPartyID)
	}
	if authParty == "" {
		t.Fatalf("missing taker auth party")
	}
	if err := adapter.Authenticate(authParty, ""); err != nil {
		t.Fatalf("participant adapter auth: %v", err)
	}
	if strings.TrimSpace(adapter.APIURL()) == "" || strings.TrimSpace(adapter.AuthToken()) == "" {
		t.Fatalf("participant adapter missing api url or token after auth")
	}

	visible, detail := traceTradeVisibilityByCID(adapter.APIURL(), adapter.AuthToken(), authParty, acceptCtx.TradeTemplateID, acceptCtx.TradeCID)
	t.Logf("pre-confirm trade visibility: visible=%v detail=%s", visible, detail)

	holdings, err := adapter.GetHoldingContracts("")
	if err != nil {
		t.Fatalf("fetch holdings: %v", err)
	}
	holding, err := selectHoldingForLeg(holdings, acceptCtx.TakerLeg.Asset, acceptCtx.TakerLeg.Amount)
	if err != nil {
		t.Fatalf("select holding: %v", err)
	}
	t.Logf("selected holding contract_id=%s amount=%s instrument=%s admin=%s",
		holding.ContractID, holding.Amount, holding.InstrumentID, holding.InstrumentAdmin)

	build, err := confirmbuilder.BuildTakerConfirmPayload(*acceptCtx, *holding)
	if err != nil {
		t.Fatalf("build confirm payload: %v", err)
	}
	if len(build.Payload.Commands) < 2 {
		t.Fatalf("expected allocate+confirm commands, got=%d", len(build.Payload.Commands))
	}

	allocatePayload := build.Payload
	allocatePayload.Commands = []any{build.Payload.Commands[0]}
	allocatePayload.SynchronizerID = ""
	logCommandJSON(t, "allocate command", allocatePayload.Commands[0])

	allocTx, err := adapter.PrepareAndSubmit(allocatePayload)
	if err != nil {
		t.Fatalf("allocate submit failed: %v", err)
	}
	_, allocationID := confirmbuilder.ParseConfirmResult(allocTx.TransactionTree)
	if strings.TrimSpace(allocationID) == "" {
		t.Fatalf("allocate submit succeeded but no allocation id in tx tree")
	}
	t.Logf("allocate submit ok allocation_id=%s", allocationID)

	confirmPayload := build.Payload
	confirmPayload.Commands = []any{build.Payload.Commands[1]}
	confirmPayload.SynchronizerID = ""
	if err := setConfirmAllocationCID(confirmPayload.Commands[0], allocationID, "allocationCid"); err != nil {
		t.Fatalf("set allocationCid in confirm command: %v", err)
	}
	logCommandJSON(t, "confirm command", confirmPayload.Commands[0])

	visible2, detail2 := traceTradeVisibilityByCID(adapter.APIURL(), adapter.AuthToken(), authParty, acceptCtx.TradeTemplateID, acceptCtx.TradeCID)
	t.Logf("before confirm submit trade visibility: visible=%v detail=%s", visible2, detail2)

	confirmTx, err := adapter.PrepareAndSubmit(confirmPayload)
	if err != nil {
		visible3, detail3 := traceTradeVisibilityByCID(adapter.APIURL(), adapter.AuthToken(), authParty, acceptCtx.TradeTemplateID, acceptCtx.TradeCID)
		t.Fatalf("confirm submit failed: %v | trade visibility after fail: visible=%v detail=%s", err, visible3, detail3)
	}

	newTradeCID, _ := confirmbuilder.ParseConfirmResult(confirmTx.TransactionTree)
	if strings.TrimSpace(newTradeCID) == "" {
		newTradeCID = acceptCtx.TradeCID
	}
	t.Logf("confirm submit ok new_trade_cid=%s allocation_id=%s", newTradeCID, allocationID)
}

func traceTradeVisibilityByCID(apiURL, bearerToken, partyID, templateID, tradeCID string) (bool, string) {
	candidates := tradeTemplateCandidates(templateID)
	details := make([]string, 0, len(candidates))
	for _, tpl := range candidates {
		ok, count, err := hasContractIDForTemplate(apiURL, bearerToken, partyID, tpl, tradeCID)
		if err != nil {
			details = append(details, fmt.Sprintf("tpl=%s err=%v", tpl, err))
			continue
		}
		details = append(details, fmt.Sprintf("tpl=%s found=%v count=%d", tpl, ok, count))
		if ok {
			return true, strings.Join(details, " | ")
		}
	}
	return false, strings.Join(details, " | ")
}

func tradeTemplateCandidates(templateID string) []string {
	base := strings.TrimSpace(templateID)
	if base == "" {
		base = "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2"
	}
	candidates := []string{base}
	if strings.HasPrefix(base, "#") {
		candidates = append(candidates, strings.TrimPrefix(base, "#"))
	} else {
		candidates = append(candidates, "#"+base)
	}
	candidates = append(candidates, "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2")
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		uniq = append(uniq, c)
	}
	return uniq
}

func hasContractIDForTemplate(apiURL, bearerToken, partyID, templateID, targetCID string) (bool, int, error) {
	offset, err := getLedgerEnd(apiURL, bearerToken)
	if err != nil {
		return false, 0, err
	}
	entries, err := queryActiveContracts(apiURL, bearerToken, partyID, templateID, offset)
	if err != nil {
		return false, 0, err
	}
	for _, entry := range entries {
		created := createdEventFromActiveEntry(entry)
		if len(created) == 0 {
			continue
		}
		if strings.TrimSpace(readString(created["contractId"])) == strings.TrimSpace(targetCID) {
			return true, len(entries), nil
		}
	}
	return false, len(entries), nil
}

func logCommandJSON(t *testing.T, prefix string, cmd any) {
	t.Helper()
	b, err := json.Marshal(cmd)
	if err != nil {
		t.Logf("%s: <marshal error: %v>", prefix, err)
		return
	}
	t.Logf("%s: %s", prefix, string(b))
}

var _ wallet.WalletAdapter = (*participant.Adapter)(nil)
var _ tradeWallet = (*participant.Adapter)(nil)

func _loadTraceConfig() (config.AppConfig, error) {
	return loadLiveConfig()
}
