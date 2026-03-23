package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trngle-xyz/cli/internal/wallet"
)

type QuoteClient interface {
	GetAssets(ctx context.Context) ([]Asset, error)
	RequestQuote(ctx context.Context, from, to, amount, partyID string) (*Quote, error)
	AcceptQuote(ctx context.Context, quoteID string) (*wallet.CommandPayload, error)
	AcceptQuoteContext(ctx context.Context, quoteID string) (*AcceptContext, error)
	ConfirmQuote(ctx context.Context, quoteID string, txResult any) error
	GetTradeStatus(ctx context.Context, tradeID string) (*TradeStatus, error)
	ReportError(ctx context.Context, quoteID string, report ErrorReport) error
}

type ErrorReport struct {
	AcceptContextID string `json:"accept_context_id"`
	Phase           string `json:"phase"`
	ErrorMessage    string `json:"error_message"`
	AllocationID    string `json:"allocation_id,omitempty"`
}

type Asset struct {
	Symbol string `json:"symbol"`
}

type Quote struct {
	ID         string    `json:"quote_id"`
	FromAsset  string    `json:"from_asset"`
	ToAsset    string    `json:"to_asset"`
	FromAmount string    `json:"from_amount"`
	ToAmount   string    `json:"to_amount"`
	Rate       string    `json:"rate"`
	TTLSeconds int       `json:"ttl_seconds"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type TradeStatus struct {
	TradeID string `json:"trade_id"`
	Status  string `json:"status"`
}

type QuoteLeg struct {
	Asset  string `json:"asset"`
	Amount string `json:"amount"`
}

type AcceptContext struct {
	QuoteID         string    `json:"quote_id"`
	TradeCID        string    `json:"trade_cid"`
	TradeID         string    `json:"trade_id"`
	OperatorParty   string    `json:"operator_party"`
	TakerParty      string    `json:"taker_party"`
	MakerParty      string    `json:"maker_party"`
	TradeTemplateID string    `json:"trade_template_id,omitempty"`
	FactoryCID      string    `json:"allocation_factory_cid,omitempty"`
	Disclosed       []any     `json:"disclosed_contracts,omitempty"`
	TakerLeg        QuoteLeg  `json:"taker_leg"`
	MakerLeg        QuoteLeg  `json:"maker_leg"`
	QuoteExpiresAt  time.Time `json:"quote_expires_at"`
	ConfirmBefore   time.Time `json:"confirm_before"`
	AcceptContextID string    `json:"accept_context_id"`
	Status          string    `json:"status"`
}

type MockQuoteClient struct {
	mu     sync.Mutex
	quotes map[string]*Quote
}

func NewMockQuoteClient() *MockQuoteClient {
	return &MockQuoteClient{
		quotes: make(map[string]*Quote),
	}
}

// NewQuoteClient returns a QuoteClient. If operatorBaseURL is empty or "mock", returns a MockQuoteClient.
// Otherwise returns a QuoteAPIClient that calls the operator API at operatorBaseURL (e.g. http://localhost:8080).
func NewQuoteClient(operatorBaseURL, apiKey string) QuoteClient {
	if operatorBaseURL == "" || strings.ToLower(strings.TrimSpace(operatorBaseURL)) == "mock" {
		return NewMockQuoteClient()
	}
	return NewQuoteAPIClient(operatorBaseURL, apiKey)
}

func (m *MockQuoteClient) GetAssets(_ context.Context) ([]Asset, error) {
	return []Asset{
		{Symbol: "CC"},
		{Symbol: "CBTC"},
		{Symbol: "USDXLR"},
		{Symbol: "USDCx"},
	}, nil
}

func (m *MockQuoteClient) RequestQuote(_ context.Context, from, to, amount, partyID string) (*Quote, error) {
	_ = partyID

	from = normalizeSymbol(from)
	to = normalizeSymbol(to)
	if from == to {
		return nil, fmt.Errorf("from and to assets must differ")
	}

	amountVal, err := strconv.ParseFloat(amount, 64)
	if err != nil || amountVal <= 0 {
		return nil, fmt.Errorf("amount must be a positive number")
	}

	fromPrice, ok := priceTable[from]
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s", from)
	}
	toPrice, ok := priceTable[to]
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s", to)
	}

	toAmount := (amountVal * fromPrice) / toPrice
	rate := fromPrice / toPrice

	q := &Quote{
		ID:         fmt.Sprintf("QT-%d", time.Now().UnixNano()%1000000),
		FromAsset:  from,
		ToAsset:    to,
		FromAmount: fmt.Sprintf("%.10f", amountVal),
		ToAmount:   fmt.Sprintf("%.10f", toAmount),
		Rate:       fmt.Sprintf("%.10f", rate),
		TTLSeconds: 30,
		ExpiresAt:  time.Now().Add(30 * time.Second).UTC(),
	}

	m.mu.Lock()
	m.quotes[q.ID] = q
	m.mu.Unlock()
	return q, nil
}

func (m *MockQuoteClient) AcceptQuote(_ context.Context, quoteID string) (*wallet.CommandPayload, error) {
	m.mu.Lock()
	q, ok := m.quotes[quoteID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("quote not found")
	}
	if time.Now().After(q.ExpiresAt) {
		return nil, fmt.Errorf("quote expired")
	}

	return &wallet.CommandPayload{
		Commands: []any{
			map[string]any{"ExerciseCommand": map[string]any{"choice": "AllocationFactory_Allocate"}},
			map[string]any{"ExerciseCommand": map[string]any{"choice": "TakerConfirm"}},
		},
		DisclosedContracts:           []any{},
		ActAs:                        []string{"mock-party"},
		ReadAs:                       []string{"mock-party"},
		SynchronizerID:               "global-domain::mock",
		PackageIDSelectionPreference: []string{},
	}, nil
}

func (m *MockQuoteClient) AcceptQuoteContext(_ context.Context, quoteID string) (*AcceptContext, error) {
	m.mu.Lock()
	q, ok := m.quotes[quoteID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("quote not found")
	}
	if time.Now().After(q.ExpiresAt) {
		return nil, fmt.Errorf("quote expired")
	}

	return &AcceptContext{
		QuoteID:         q.ID,
		TradeCID:        "00mocktradecid",
		TradeID:         fmt.Sprintf("TRADE-%d", time.Now().UnixNano()%1000000),
		OperatorParty:   "mock-operator::1220mock",
		TakerParty:      "mock-party::1220mock",
		MakerParty:      "mock-maker::1220mock",
		TradeTemplateID: "#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
		FactoryCID:      "00mockfactorycid",
		Disclosed:       []any{},
		TakerLeg: QuoteLeg{
			Asset:  q.FromAsset,
			Amount: q.FromAmount,
		},
		MakerLeg: QuoteLeg{
			Asset:  q.ToAsset,
			Amount: q.ToAmount,
		},
		QuoteExpiresAt:  q.ExpiresAt.UTC(),
		ConfirmBefore:   q.ExpiresAt.UTC(),
		AcceptContextID: fmt.Sprintf("AC-%d", time.Now().UnixNano()%100000),
		Status:          "ready_for_taker_confirm",
	}, nil
}

func (m *MockQuoteClient) ConfirmQuote(_ context.Context, quoteID string, txResult any) error {
	_ = txResult
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.quotes[quoteID]; !ok {
		return fmt.Errorf("quote not found")
	}
	delete(m.quotes, quoteID)
	return nil
}

func (m *MockQuoteClient) GetTradeStatus(_ context.Context, tradeID string) (*TradeStatus, error) {
	return &TradeStatus{
		TradeID: tradeID,
		Status:  "submitted",
	}, nil
}

func (m *MockQuoteClient) ReportError(_ context.Context, quoteID string, report ErrorReport) error {
	return nil
}

var priceTable = map[string]float64{
	"CC":     1.0,
	"CBTC":   42000.0,
	"USDXLR": 1.0,
	"USDCX":  1.0,
}

func normalizeSymbol(symbol string) string {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "AMULET" {
		return "CC"
	}
	return s
}
