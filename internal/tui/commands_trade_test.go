package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/trngle-xyz/cli/internal/config"
	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/wallet"
)

type fakeQuoteClient struct {
	quote            *core.Quote
	acceptContext    *core.AcceptContext
	acceptErr        error
	confirmErr       error
	requestPartyID   string
	acceptCalled     bool
	acceptCtxCalled  bool
	confirmCalled    bool
	confirmedQuote   string
	confirmedPayload any
}

func (f *fakeQuoteClient) GetAssets(_ context.Context) ([]core.Asset, error) { return nil, nil }

func (f *fakeQuoteClient) RequestQuote(_ context.Context, from, to, amount, partyID string) (*core.Quote, error) {
	f.requestPartyID = partyID
	return f.quote, nil
}

func (f *fakeQuoteClient) AcceptQuote(_ context.Context, quoteID string) (*wallet.CommandPayload, error) {
	f.acceptCalled = true
	return nil, errors.New("legacy accept should not be called")
}

func (f *fakeQuoteClient) AcceptQuoteContext(_ context.Context, quoteID string) (*core.AcceptContext, error) {
	f.acceptCtxCalled = true
	if f.acceptErr != nil {
		return nil, f.acceptErr
	}
	return f.acceptContext, nil
}

func (f *fakeQuoteClient) ConfirmQuote(_ context.Context, quoteID string, txResult any) error {
	f.confirmCalled = true
	f.confirmedQuote = quoteID
	f.confirmedPayload = txResult
	return f.confirmErr
}

func (f *fakeQuoteClient) GetTradeStatus(_ context.Context, tradeID string) (*core.TradeStatus, error) {
	return &core.TradeStatus{TradeID: tradeID, Status: "submitted"}, nil
}

func (f *fakeQuoteClient) ReportError(_ context.Context, quoteID string, report core.ErrorReport) error {
	return nil
}

type fakeTradeWallet struct {
	holdings      []wallet.HoldingContract
	submitResult  *wallet.TransactionResult
	submitResults []*wallet.TransactionResult
	submitErrs    []error
	submitErr     error
	submitCalled  bool
	submitCount   int
	submitted     []wallet.CommandPayload
}

func (f *fakeTradeWallet) PartyID() string   { return "party-a" }
func (f *fakeTradeWallet) PublicKey() string { return "pub" }
func (f *fakeTradeWallet) Initialize(privateKeyHex string) error {
	return nil
}
func (f *fakeTradeWallet) Authenticate(partyID, apiURL string) error { return nil }
func (f *fakeTradeWallet) GetBalances() ([]wallet.Balance, error)    { return nil, nil }
func (f *fakeTradeWallet) GetHoldingContracts(interfaceID string) ([]wallet.HoldingContract, error) {
	return f.holdings, nil
}
func (f *fakeTradeWallet) PrepareAndSubmit(payload wallet.CommandPayload) (*wallet.TransactionResult, error) {
	f.submitCalled = true
	f.submitCount++
	f.submitted = append(f.submitted, payload)
	if len(f.submitErrs) >= f.submitCount {
		if err := f.submitErrs[f.submitCount-1]; err != nil {
			return nil, err
		}
	}
	if f.submitErr != nil {
		return nil, f.submitErr
	}
	if len(f.submitResults) >= f.submitCount {
		if r := f.submitResults[f.submitCount-1]; r != nil {
			return r, nil
		}
	}
	if f.submitResult != nil {
		return f.submitResult, nil
	}
	return &wallet.TransactionResult{CommandID: "cmd-1", TransactionTree: map[string]any{}}, nil
}
func (f *fakeTradeWallet) GetPendingTransfers() ([]wallet.PendingTransfer, error) { return nil, nil }
func (f *fakeTradeWallet) AcceptTransfer(transferID string) (*wallet.TransactionResult, error) {
	return nil, nil
}

func testAcceptContext() *core.AcceptContext {
	now := time.Now().UTC().Add(2 * time.Minute)
	return &core.AcceptContext{
		QuoteID:         "QT-1",
		TradeCID:        "00tradecid",
		TradeID:         "ST-1",
		OperatorParty:   "operator::party",
		TakerParty:      "taker::party",
		MakerParty:      "maker::party",
		TradeTemplateID: "#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
		FactoryCID:      "00factorycid",
		Disclosed:       []any{},
		TakerLeg: core.QuoteLeg{
			Asset:  "CC",
			Amount: "1.0000000000",
		},
		MakerLeg: core.QuoteLeg{
			Asset:  "CBTC",
			Amount: "0.0000200000",
		},
		QuoteExpiresAt:  now,
		ConfirmBefore:   now,
		AcceptContextID: "AC-1",
		Status:          "ready_for_taker_confirm",
	}
}

func testHolding() wallet.HoldingContract {
	return wallet.HoldingContract{
		ContractID:      "00holdingcid",
		SynchronizerID:  "domain::global",
		Amount:          "10.0000000000",
		InstrumentAdmin: "admin::party",
		InstrumentID:    "Amulet",
	}
}

// stubNoRegistry stubs the registry lookup to always fail (no network in tests).
// Returns the cleanup function to restore the original.
func stubNoRegistry(t *testing.T) {
	t.Helper()
	old := fetchRegistryFactoryFn
	fetchRegistryFactoryFn = func(_ string, _ wallet.HoldingContract, _ *core.AcceptContext, _ string, _ string) (*registryFactoryResult, error) {
		return nil, errors.New("registry unavailable in test")
	}
	t.Cleanup(func() { fetchRegistryFactoryFn = old })
}

// TestPrefetchQuoteCmdDoesNotCallAcceptContextEagerly verifies that the
// prefetch only calls RequestQuote — AcceptQuoteContext (on-chain trade
// creation) is deferred until the user presses Y.
func TestPrefetchQuoteCmdDoesNotCallAcceptContextEagerly(t *testing.T) {
	qc := &fakeQuoteClient{
		quote: &core.Quote{
			ID: "QT-1",
		},
		acceptContext: testAcceptContext(),
	}

	oldClient := newQuoteClientFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	t.Cleanup(func() { newQuoteClientFn = oldClient })

	msg := prefetchQuoteCmd(config.AppConfig{TrngleAPIURL: "http://localhost:18080"}, "CC", "CBTC", "1", "party-a")()
	got, ok := msg.(quotePrefetchResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected prefetch error: %v", got.err)
	}
	// Accept context should NOT be fetched during prefetch.
	if got.acceptCtx != nil {
		t.Fatalf("accept context should be nil in prefetch result (fetched lazily on Y)")
	}
	if qc.acceptCtxCalled {
		t.Fatalf("AcceptQuoteContext should not be called during prefetch")
	}
	if qc.acceptCalled {
		t.Fatalf("legacy AcceptQuote should not be called")
	}
}

func TestPrefetchQuoteCmdUsesConfiguredQuotePartyID(t *testing.T) {
	qc := &fakeQuoteClient{
		quote:         &core.Quote{ID: "QT-1"},
		acceptContext: testAcceptContext(),
	}

	oldClient := newQuoteClientFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	t.Cleanup(func() { newQuoteClientFn = oldClient })

	msg := prefetchQuoteCmd(config.AppConfig{
		TrngleAPIURL: "http://localhost:18080",
		QuotePartyID: "mock-taker",
	}, "CC", "CBTC", "1", "loop-wallet-party")()
	got, ok := msg.(quotePrefetchResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected prefetch error: %v", got.err)
	}
	if qc.requestPartyID != "mock-taker" {
		t.Fatalf("expected quote request party_id=mock-taker, got=%q", qc.requestPartyID)
	}
}

// TestExecuteQuoteSubmitCmdFetchesAcceptContextOnYPress verifies that when
// the ActiveQuote has no AcceptContext (the normal post-prefetch state),
// executeQuoteSubmitCmd fetches it before doing the on-chain interactions.
func TestExecuteQuoteSubmitCmdFetchesAcceptContextOnYPress(t *testing.T) {
	stubNoRegistry(t)

	qc := &fakeQuoteClient{
		acceptContext: testAcceptContext(),
	}
	w := &fakeTradeWallet{
		holdings: []wallet.HoldingContract{testHolding()},
		submitResults: []*wallet.TransactionResult{
			{
				CommandID: "cmd-alloc",
				TransactionTree: map[string]any{
					"eventsById": map[string]any{
						"0": map[string]any{
							"created": map[string]any{
								"templateId": "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation",
								"contractId": "00alloc",
							},
						},
					},
				},
			},
			{
				CommandID:       "cmd-confirm",
				TransactionTree: map[string]any{"eventsById": map[string]any{}},
			},
		},
	}

	oldClient := newQuoteClientFn
	oldWallet := newTradeWalletFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	newTradeWalletFn = func() tradeWallet { return w }
	t.Cleanup(func() {
		newQuoteClientFn = oldClient
		newTradeWalletFn = oldWallet
	})

	// No AcceptContext — simulates the post-prefetch state.
	aq := &ActiveQuote{
		ID: "QT-1",
	}
	msg := executeQuoteSubmitCmd(config.AppConfig{
		PrivateKeyHex: "deadbeef",
		WalletPartyID: "party-a",
		TrngleAPIURL:  "http://localhost:18080",
	}, aq)()
	res, ok := msg.(quoteSubmitResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if res.submitErr != nil {
		t.Fatalf("unexpected submit error: %v", res.submitErr)
	}
	if !qc.acceptCtxCalled {
		t.Fatalf("expected AcceptQuoteContext to be called when AcceptContext is nil")
	}
	if w.submitCount != 2 {
		t.Fatalf("expected 2 submits (allocate + confirm), got %d", w.submitCount)
	}
	if !qc.confirmCalled {
		t.Fatalf("expected operator confirm call")
	}
}

func TestExecuteQuoteSubmitCmdAcceptContextSuccessCallsConfirm(t *testing.T) {
	stubNoRegistry(t)

	qc := &fakeQuoteClient{}
	w := &fakeTradeWallet{
		holdings: []wallet.HoldingContract{testHolding()},
		submitResults: []*wallet.TransactionResult{
			{
				CommandID: "cmd-alloc",
				TransactionTree: map[string]any{
					"eventsById": map[string]any{
						"0": map[string]any{
							"created": map[string]any{
								"templateId": "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation",
								"contractId": "00alloc",
							},
						},
					},
				},
			},
			{
				CommandID:       "cmd-confirm",
				TransactionTree: map[string]any{"eventsById": map[string]any{}},
			},
		},
	}

	oldClient := newQuoteClientFn
	oldWallet := newTradeWalletFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	newTradeWalletFn = func() tradeWallet { return w }
	t.Cleanup(func() {
		newQuoteClientFn = oldClient
		newTradeWalletFn = oldWallet
	})

	aq := &ActiveQuote{
		ID:            "QT-1",
		AcceptContext: testAcceptContext(),
	}
	msg := executeQuoteSubmitCmd(config.AppConfig{
		PrivateKeyHex: "deadbeef",
		WalletPartyID: "party-a",
		TrngleAPIURL:  "http://localhost:18080",
	}, aq)()
	res, ok := msg.(quoteSubmitResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if res.submitErr != nil {
		t.Fatalf("unexpected submit error: %v", res.submitErr)
	}
	if !w.submitCalled {
		t.Fatalf("expected wallet PrepareAndSubmit call")
	}
	if w.submitCount != 2 {
		t.Fatalf("expected 2 submits (allocate + confirm), got %d", w.submitCount)
	}
	if !qc.confirmCalled {
		t.Fatalf("expected confirm call")
	}
	if qc.confirmedQuote != "QT-1" {
		t.Fatalf("unexpected confirm quote id: %s", qc.confirmedQuote)
	}
	payload, ok := qc.confirmedPayload.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", qc.confirmedPayload)
	}
	if payload["accept_context_id"] != "AC-1" {
		t.Fatalf("missing accept_context_id in confirm payload: %+v", payload)
	}
	if _, ok := payload["command_id"]; ok {
		t.Fatalf("confirm payload should not include command_id: %+v", payload)
	}
}

func TestExecuteQuoteSubmitCmdAcceptContextSubmitFailureSkipsConfirm(t *testing.T) {
	stubNoRegistry(t)

	qc := &fakeQuoteClient{}
	w := &fakeTradeWallet{
		holdings:  []wallet.HoldingContract{testHolding()},
		submitErr: errors.New("submit failed"),
	}

	oldClient := newQuoteClientFn
	oldWallet := newTradeWalletFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	newTradeWalletFn = func() tradeWallet { return w }
	t.Cleanup(func() {
		newQuoteClientFn = oldClient
		newTradeWalletFn = oldWallet
	})

	aq := &ActiveQuote{
		ID:            "QT-1",
		AcceptContext: testAcceptContext(),
	}
	msg := executeQuoteSubmitCmd(config.AppConfig{
		PrivateKeyHex: "deadbeef",
		WalletPartyID: "party-a",
		TrngleAPIURL:  "http://localhost:18080",
	}, aq)()
	res, ok := msg.(quoteSubmitResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if res.submitErr == nil {
		t.Fatalf("expected submit error")
	}
	if qc.confirmCalled {
		t.Fatalf("confirm should not be called on submit failure")
	}
}

func TestExecuteQuoteSubmitCmdRetriesTemplateVariantsOnContractNotFound(t *testing.T) {
	stubNoRegistry(t)

	qc := &fakeQuoteClient{}
	w := &fakeTradeWallet{
		holdings: []wallet.HoldingContract{testHolding()},
		submitErrs: []error{
			nil,
			errors.New("submit transaction failed (400): contract_not_found"),
			nil,
		},
		submitResults: []*wallet.TransactionResult{
			{
				CommandID: "cmd-alloc",
				TransactionTree: map[string]any{
					"eventsById": map[string]any{
						"0": map[string]any{
							"created": map[string]any{
								"templateId": "#splice-api-token-allocation-v1:Splice.Api.Token.AllocationV1:Allocation",
								"contractId": "00alloc",
							},
						},
					},
				},
			},
			nil,
			{
				CommandID:       "cmd-confirm-retry",
				TransactionTree: map[string]any{"eventsById": map[string]any{}},
			},
		},
	}

	oldClient := newQuoteClientFn
	oldWallet := newTradeWalletFn
	newQuoteClientFn = func(_ string) core.QuoteClient { return qc }
	newTradeWalletFn = func() tradeWallet { return w }
	t.Cleanup(func() {
		newQuoteClientFn = oldClient
		newTradeWalletFn = oldWallet
	})

	aq := &ActiveQuote{
		ID:            "QT-1",
		AcceptContext: testAcceptContext(),
	}
	msg := executeQuoteSubmitCmd(config.AppConfig{
		PrivateKeyHex: "deadbeef",
		WalletPartyID: "party-a",
		TrngleAPIURL:  "http://localhost:18080",
	}, aq)()
	res, ok := msg.(quoteSubmitResultMsg)
	if !ok {
		t.Fatalf("unexpected message type: %T", msg)
	}
	if res.submitErr != nil {
		t.Fatalf("unexpected submit error: %v", res.submitErr)
	}
	if w.submitCount != 3 {
		t.Fatalf("expected 3 submits (allocate + confirm + retry), got %d", w.submitCount)
	}
	if !qc.confirmCalled {
		t.Fatalf("expected confirm call after successful retry")
	}
}

func TestConfirmTemplateCandidatesIncludeModernFallback(t *testing.T) {
	candidates := confirmTemplateCandidates("#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2")
	want := "#trngle-cip56-v2:TradeConfirmV2:TradeConfirmV2"
	found := false
	for _, c := range candidates {
		if c == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected candidates to include %q, got=%v", want, candidates)
	}
}
