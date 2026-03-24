package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/history"
	"github.com/trngle-xyz/cli/internal/wallet"
)

// ---------------------------------------------------------------------------
// Mock wallet adapter
// ---------------------------------------------------------------------------

type mockWallet struct {
	partyID   string
	balances  []wallet.Balance
	transfers []wallet.PendingTransfer
}

func (m *mockWallet) PartyID() string  { return m.partyID }
func (m *mockWallet) PublicKey() string { return "mock-pubkey" }
func (m *mockWallet) Initialize(string) error {
	return nil
}
func (m *mockWallet) GetBalances() ([]wallet.Balance, error) {
	return m.balances, nil
}
func (m *mockWallet) GetHoldingContracts(string) ([]wallet.HoldingContract, error) {
	return nil, nil
}
func (m *mockWallet) PrepareAndSubmit(wallet.CommandPayload) (*wallet.TransactionResult, error) {
	return &wallet.TransactionResult{CommandID: "cmd-1"}, nil
}
func (m *mockWallet) GetPendingTransfers() ([]wallet.PendingTransfer, error) {
	return m.transfers, nil
}
func (m *mockWallet) AcceptTransfer(string) (*wallet.TransactionResult, error) {
	return &wallet.TransactionResult{CommandID: "cmd-2"}, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T, opts ...ServerOption) (*Server, *history.Store) {
	t.Helper()

	db, err := history.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open history db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	w := &mockWallet{
		partyID: "test-party-123",
		balances: []wallet.Balance{
			{Symbol: "CC", Amount: "1000.00"},
			{Symbol: "CBTC", Amount: "0.5"},
		},
		transfers: []wallet.PendingTransfer{
			{ID: "xfer-1", From: "alice", Amount: "50", Asset: "CC", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339)},
		},
	}

	q := core.NewMockQuoteClient()
	srv := NewServer("127.0.0.1:0", w, q, db, opts...)
	go srv.hub.Run()
	t.Cleanup(func() { srv.hub.Stop() })

	return srv, db
}

func doRequest(srv *Server, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests: GET /health
// ---------------------------------------------------------------------------

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "GET", "/health", nil, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
	w, ok := body["wallet"].(map[string]any)
	if !ok {
		t.Fatal("missing wallet field")
	}
	if w["party_id"] != "test-party-123" {
		t.Errorf("expected party_id test-party-123, got %v", w["party_id"])
	}
}

func TestHealth_WrongMethod(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/health", nil, nil)
	if rec.Code != 405 {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /balances
// ---------------------------------------------------------------------------

func TestBalances(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "GET", "/balances", nil, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	balances, ok := body["balances"].([]any)
	if !ok {
		t.Fatal("missing balances field")
	}
	if len(balances) != 2 {
		t.Errorf("expected 2 balances, got %d", len(balances))
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /trade/quote
// ---------------------------------------------------------------------------

func TestTradeQuote(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/trade/quote", map[string]string{
		"from": "CC", "to": "CBTC", "amount": "100",
	}, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["quote_id"] == nil || body["quote_id"] == "" {
		t.Error("expected quote_id in response")
	}
	if body["from"] != "CC" {
		t.Errorf("expected from=CC, got %v", body["from"])
	}
	if body["to"] != "CBTC" {
		t.Errorf("expected to=CBTC, got %v", body["to"])
	}
	if body["rate"] == nil {
		t.Error("expected rate in response")
	}
}

func TestTradeQuote_MissingFields(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/trade/quote", map[string]string{
		"from": "CC",
	}, nil)

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTradeQuote_InvalidJSON(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest("POST", "/trade/quote", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTradeQuote_SameAsset(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/trade/quote", map[string]string{
		"from": "CC", "to": "CC", "amount": "100",
	}, nil)

	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /trade/{quote_id}/confirm
// ---------------------------------------------------------------------------

func TestTradeConfirm(t *testing.T) {
	srv, db := newTestServer(t)

	// First get a quote.
	rec := doRequest(srv, "POST", "/trade/quote", map[string]string{
		"from": "CC", "to": "CBTC", "amount": "100",
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("quote request failed: %d", rec.Code)
	}
	quoteBody := decodeBody(t, rec)
	quoteID := quoteBody["quote_id"].(string)

	// Confirm the trade.
	rec = doRequest(srv, "POST", "/trade/"+quoteID+"/confirm", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["trade_id"] == nil || body["trade_id"] == "" {
		t.Error("expected trade_id in response")
	}
	if body["status"] != "submitted" {
		t.Errorf("expected status=submitted, got %v", body["status"])
	}
	if body["from"] != "CC" {
		t.Errorf("expected from=CC, got %v", body["from"])
	}

	// Verify trade was recorded in history.
	txs, total, err := db.List(10, 0, "", "")
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 trade in history, got %d", total)
	}
	if txs[0].Type != "trade" {
		t.Errorf("expected type=trade, got %s", txs[0].Type)
	}
	if txs[0].FromAsset == nil || *txs[0].FromAsset != "CC" {
		t.Error("expected from_asset=CC in history record")
	}
}

func TestTradeConfirm_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/trade/nonexistent/confirm", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestTradeConfirm_Expired(t *testing.T) {
	srv, _ := newTestServer(t)

	// Get a quote, then wait for it to expire (mock TTL is 30s, but we can
	// test by confirming twice — second time the quote is already consumed).
	rec := doRequest(srv, "POST", "/trade/quote", map[string]string{
		"from": "CC", "to": "CBTC", "amount": "100",
	}, nil)
	quoteBody := decodeBody(t, rec)
	quoteID := quoteBody["quote_id"].(string)

	// First confirm succeeds.
	doRequest(srv, "POST", "/trade/"+quoteID+"/confirm", nil, nil)

	// Second confirm should fail (quote consumed/not found).
	rec = doRequest(srv, "POST", "/trade/"+quoteID+"/confirm", nil, nil)
	if rec.Code != 404 && rec.Code != 410 {
		t.Fatalf("expected 404 or 410, got %d", rec.Code)
	}
}

func TestTradeConfirm_BadPath(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/trade/some-id/invalid", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /trades
// ---------------------------------------------------------------------------

func TestTrades_Empty(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "GET", "/trades", nil, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	trades := body["trades"].([]any)
	if len(trades) != 0 {
		t.Errorf("expected 0 trades, got %d", len(trades))
	}
	if body["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", body["total"])
	}
}

func TestTrades_WithData(t *testing.T) {
	srv, db := newTestServer(t)

	db.Insert(history.Tx{Type: "trade", Status: "submitted", CreatedAt: time.Now().Format(time.RFC3339)})
	db.Insert(history.Tx{Type: "trade", Status: "settled", CreatedAt: time.Now().Format(time.RFC3339)})

	rec := doRequest(srv, "GET", "/trades?limit=10", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["total"].(float64) != 2 {
		t.Errorf("expected total=2, got %v", body["total"])
	}
}

func TestTrades_FilterByType(t *testing.T) {
	srv, db := newTestServer(t)

	db.Insert(history.Tx{Type: "trade", Status: "submitted", CreatedAt: time.Now().Format(time.RFC3339)})
	db.Insert(history.Tx{Type: "transfer_in", Status: "accepted", CreatedAt: time.Now().Format(time.RFC3339)})

	rec := doRequest(srv, "GET", "/trades?type=trade", nil, nil)
	body := decodeBody(t, rec)
	if body["total"].(float64) != 1 {
		t.Errorf("expected total=1 for type=trade filter, got %v", body["total"])
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /trades/{id}
// ---------------------------------------------------------------------------

func TestTradeByID(t *testing.T) {
	srv, db := newTestServer(t)

	tx := history.Tx{ID: "test-tx-1", Type: "trade", Status: "submitted", CreatedAt: time.Now().Format(time.RFC3339)}
	db.Insert(tx)

	rec := doRequest(srv, "GET", "/trades/test-tx-1", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["id"] != "test-tx-1" {
		t.Errorf("expected id=test-tx-1, got %v", body["id"])
	}
}

func TestTradeByID_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "GET", "/trades/nonexistent", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: GET /transfers
// ---------------------------------------------------------------------------

func TestTransfers(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "GET", "/transfers", nil, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	transfers := body["transfers"].([]any)
	if len(transfers) != 1 {
		t.Errorf("expected 1 transfer, got %d", len(transfers))
	}
}

// ---------------------------------------------------------------------------
// Tests: POST /transfers/{id}/accept
// ---------------------------------------------------------------------------

func TestTransferAccept(t *testing.T) {
	srv, db := newTestServer(t)
	rec := doRequest(srv, "POST", "/transfers/xfer-1/accept", nil, nil)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["status"] != "accepted" {
		t.Errorf("expected status=accepted, got %v", body["status"])
	}
	if body["asset"] != "CC" {
		t.Errorf("expected asset=CC, got %v", body["asset"])
	}

	// Verify recorded in history with asset data.
	txs, _, _ := db.List(10, 0, "", "")
	if len(txs) != 1 {
		t.Fatalf("expected 1 tx in history, got %d", len(txs))
	}
	if txs[0].ToAsset == nil || *txs[0].ToAsset != "CC" {
		t.Error("expected to_asset=CC in history record")
	}
}

func TestTransferAccept_BadPath(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := doRequest(srv, "POST", "/transfers/xfer-1/invalid", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Auth middleware
// ---------------------------------------------------------------------------

func TestAuth_Rejected(t *testing.T) {
	srv, _ := newTestServer(t, WithAuth("secret-token"))

	// No token → 401
	rec := doRequest(srv, "GET", "/balances", nil, nil)
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// Wrong token → 401
	rec = doRequest(srv, "GET", "/balances", nil, map[string]string{
		"Authorization": "Bearer wrong",
	})
	if rec.Code != 401 {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuth_Accepted(t *testing.T) {
	srv, _ := newTestServer(t, WithAuth("secret-token"))

	rec := doRequest(srv, "GET", "/balances", nil, map[string]string{
		"Authorization": "Bearer secret-token",
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuth_HealthBypassesAuth(t *testing.T) {
	srv, _ := newTestServer(t, WithAuth("secret-token"))

	// /health should work without token
	rec := doRequest(srv, "GET", "/health", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Method not allowed
// ---------------------------------------------------------------------------

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)

	cases := []struct {
		method string
		path   string
	}{
		{"POST", "/balances"},
		{"GET", "/trade/quote"},
		{"POST", "/trades"},
		{"POST", "/transfers"},
	}

	for _, tc := range cases {
		rec := doRequest(srv, tc.method, tc.path, nil, nil)
		if rec.Code != 405 {
			t.Errorf("%s %s: expected 405, got %d", tc.method, tc.path, rec.Code)
		}
	}
}
