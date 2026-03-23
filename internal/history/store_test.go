package history

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func tempDB(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sp(s string) *string { return &s }

func TestInsertAndGet(t *testing.T) {
	s := tempDB(t)

	tx := Tx{
		ID:        "tx-001",
		Type:      "trade",
		Status:    "pending",
		FromAsset: sp("CC"),
		ToAsset:   sp("CBTC"),
		QuoteID:   sp("QT-123"),
	}
	if err := s.Insert(tx); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.Get("tx-001")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if got.QuoteID == nil || *got.QuoteID != "QT-123" {
		t.Errorf("quote_id = %v, want QT-123", got.QuoteID)
	}
}

func TestInsertAutoGeneratesID(t *testing.T) {
	s := tempDB(t)
	tx := Tx{Type: "trade", Status: "pending"}
	if err := s.Insert(tx); err != nil {
		t.Fatalf("insert: %v", err)
	}
	txs, total, err := s.List(10, 0, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(txs) != 1 {
		t.Fatalf("expected 1 tx, got %d", total)
	}
	if txs[0].ID == "" {
		t.Error("expected auto-generated ID")
	}
}

func TestGetByTradeID(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "submitted", TradeID: sp("TRADE-99")})

	got, err := s.GetByTradeID("TRADE-99")
	if err != nil {
		t.Fatalf("get by trade id: %v", err)
	}
	if got == nil || got.ID != "tx-001" {
		t.Fatalf("expected tx-001, got %v", got)
	}

	got, _ = s.GetByTradeID("NONEXISTENT")
	if got != nil {
		t.Fatal("expected nil for nonexistent trade ID")
	}
}

func TestGetByQuoteID(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending", QuoteID: sp("QT-456")})

	got, err := s.GetByQuoteID("QT-456")
	if err != nil {
		t.Fatalf("get by quote id: %v", err)
	}
	if got == nil || got.ID != "tx-001" {
		t.Fatalf("expected tx-001, got %v", got)
	}
}

func TestUpdateStatusValidTransitions(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	// pending → submitted
	if err := s.UpdateStatus("tx-001", "submitted"); err != nil {
		t.Fatalf("pending → submitted: %v", err)
	}
	got, _ := s.Get("tx-001")
	if got.Status != "submitted" {
		t.Errorf("status = %q, want submitted", got.Status)
	}
	if got.EventCount != 1 {
		t.Errorf("event_count = %d, want 1", got.EventCount)
	}

	// submitted → settled
	now := time.Now()
	if err := s.UpdateStatus("tx-001", "settled",
		WithSettledAt(now),
		WithFinalEvent("trade_settled"),
	); err != nil {
		t.Fatalf("submitted → settled: %v", err)
	}
	got, _ = s.Get("tx-001")
	if got.Status != "settled" {
		t.Errorf("status = %q, want settled", got.Status)
	}
	if got.SettledAt == nil {
		t.Error("settled_at should be set")
	}
	if got.FinalEvent == nil || *got.FinalEvent != "trade_settled" {
		t.Errorf("final_event = %v, want trade_settled", got.FinalEvent)
	}
}

func TestUpdateStatusSignFailed(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	if err := s.UpdateStatus("tx-001", "sign_failed",
		WithError("rpc timeout", "sign"),
		WithFinalEvent("sign_failed"),
	); err != nil {
		t.Fatalf("pending → sign_failed: %v", err)
	}

	got, _ := s.Get("tx-001")
	if got.Status != "sign_failed" {
		t.Errorf("status = %q, want sign_failed", got.Status)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "rpc timeout" {
		t.Errorf("error_message = %v, want rpc timeout", got.ErrorMessage)
	}
	if got.ErrorPhase == nil || *got.ErrorPhase != "sign" {
		t.Errorf("error_phase = %v, want sign", got.ErrorPhase)
	}
}

func TestUpdateStatusContextFailed(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	if err := s.UpdateStatus("tx-001", "context_failed",
		WithError("404 not found", "accept_context"),
		WithFinalEvent("context_failed"),
	); err != nil {
		t.Fatalf("pending → context_failed: %v", err)
	}

	got, _ := s.Get("tx-001")
	if got.Status != "context_failed" {
		t.Errorf("status = %q, want context_failed", got.Status)
	}
}

func TestUpdateStatusInvalidTransition(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	// pending → settled is not allowed (must go through submitted first)
	err := s.UpdateStatus("tx-001", "settled")
	if err == nil {
		t.Fatal("expected error for invalid transition pending → settled")
	}

	// Verify status unchanged.
	got, _ := s.Get("tx-001")
	if got.Status != "pending" {
		t.Errorf("status should still be pending, got %q", got.Status)
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	s := tempDB(t)

	err := s.UpdateStatus("nonexistent", "submitted")
	if err == nil {
		t.Fatal("expected error for nonexistent tx")
	}
}

func TestUpdateFields(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	if err := s.UpdateFields("tx-001",
		WithTradeID("TRADE-42"),
		WithTradeCID("CID-99"),
		WithCounterparty("maker-party::1220abc"),
	); err != nil {
		t.Fatalf("update fields: %v", err)
	}

	got, _ := s.Get("tx-001")
	if got.TradeID == nil || *got.TradeID != "TRADE-42" {
		t.Errorf("trade_id = %v, want TRADE-42", got.TradeID)
	}
	if got.TradeCID == nil || *got.TradeCID != "CID-99" {
		t.Errorf("trade_cid = %v, want CID-99", got.TradeCID)
	}
	if got.Counterparty == nil || *got.Counterparty != "maker-party::1220abc" {
		t.Errorf("counterparty = %v, want maker-party::1220abc", got.Counterparty)
	}
}

func TestUpdateFieldsNoop(t *testing.T) {
	s := tempDB(t)
	// No options — should be a no-op, no error.
	if err := s.UpdateFields("tx-001"); err != nil {
		t.Fatalf("noop update: %v", err)
	}
}

func TestRecordEventAndGetEvents(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "submitted"})

	if err := s.RecordEvent("tx-001", "taker_confirmed", `{"type":"taker_confirmed"}`); err != nil {
		t.Fatalf("record event 1: %v", err)
	}
	if err := s.RecordEvent("tx-001", "maker_confirmed", `{"type":"maker_confirmed"}`); err != nil {
		t.Fatalf("record event 2: %v", err)
	}

	got, _ := s.Get("tx-001")
	if got.EventCount != 2 {
		t.Errorf("event_count = %d, want 2", got.EventCount)
	}

	events, err := s.GetEvents("tx-001")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "taker_confirmed" {
		t.Errorf("event[0].type = %q, want taker_confirmed", events[0].EventType)
	}
	if events[1].EventType != "maker_confirmed" {
		t.Errorf("event[1].type = %q, want maker_confirmed", events[1].EventType)
	}
}

func TestGetEventsEmpty(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "pending"})

	events, err := s.GetEvents("tx-001")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events, got %d", len(events))
	}
}

func TestListByStatus(t *testing.T) {
	s := tempDB(t)
	s.Insert(Tx{ID: "tx-001", Type: "trade", Status: "settled"})
	s.Insert(Tx{ID: "tx-002", Type: "trade", Status: "failed"})
	s.Insert(Tx{ID: "tx-003", Type: "trade", Status: "settled"})

	settled, err := s.ListByStatus("settled", 10)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(settled) != 2 {
		t.Errorf("expected 2 settled, got %d", len(settled))
	}

	failed, err := s.ListByStatus("failed", 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(failed) != 1 {
		t.Errorf("expected 1 failed, got %d", len(failed))
	}
}

func TestList(t *testing.T) {
	s := tempDB(t)
	for i := 0; i < 5; i++ {
		s.Insert(Tx{ID: GenerateID(), Type: "trade", Status: "settled", FromAsset: sp("CC")})
		time.Sleep(time.Millisecond)
	}

	txs, total, err := s.List(3, 0, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(txs) != 3 {
		t.Errorf("len = %d, want 3", len(txs))
	}

	// Paginate.
	txs2, total2, err := s.List(3, 3, "", "")
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total2 != 5 {
		t.Errorf("total = %d, want 5", total2)
	}
	if len(txs2) != 2 {
		t.Errorf("len = %d, want 2", len(txs2))
	}
}

func TestFullTradeLifecycle(t *testing.T) {
	s := tempDB(t)

	// 1. User presses Y → pending
	s.Insert(Tx{
		ID: "tx-life", Type: "trade", Status: "pending",
		FromAsset: sp("CC"), ToAsset: sp("CBTC"), QuoteID: sp("QT-100"),
	})

	// 2. AcceptContext arrives
	s.UpdateFields("tx-life",
		WithTradeID("TRADE-500"),
		WithTradeCID("CID-500"),
		WithCounterparty("maker::1220xyz"),
	)

	// 3. Sign succeeds → submitted
	s.UpdateStatus("tx-life", "submitted")

	// 4. Events arrive
	s.RecordEvent("tx-life", "taker_confirmed", `{"type":"taker_confirmed"}`)
	s.RecordEvent("tx-life", "maker_confirmed", `{"type":"maker_confirmed"}`)

	// 5. Settlement → settled
	s.UpdateStatus("tx-life", "settled",
		WithSettledAt(time.Now()),
		WithFinalEvent("trade_settled"),
	)

	got, _ := s.Get("tx-life")
	if got.Status != "settled" {
		t.Errorf("final status = %q, want settled", got.Status)
	}
	if got.TradeID == nil || *got.TradeID != "TRADE-500" {
		t.Errorf("trade_id = %v, want TRADE-500", got.TradeID)
	}
	if got.FinalEvent == nil || *got.FinalEvent != "trade_settled" {
		t.Errorf("final_event = %v, want trade_settled", got.FinalEvent)
	}
	// event_count: 1 (submitted) + 2 (record) + 1 (settled) = 4
	if got.EventCount != 4 {
		t.Errorf("event_count = %d, want 4", got.EventCount)
	}

	events, _ := s.GetEvents("tx-life")
	if len(events) != 2 {
		t.Errorf("expected 2 audit events, got %d", len(events))
	}
}

func TestFailedSignLifecycle(t *testing.T) {
	s := tempDB(t)

	s.Insert(Tx{ID: "tx-fail", Type: "trade", Status: "pending", QuoteID: sp("QT-200")})

	err := s.UpdateStatus("tx-fail", "sign_failed",
		WithError("insufficient funds", "sign"),
		WithFinalEvent("sign_failed"),
	)
	if err != nil {
		t.Fatalf("sign fail: %v", err)
	}

	got, _ := s.Get("tx-fail")
	if got.Status != "sign_failed" {
		t.Errorf("status = %q, want sign_failed", got.Status)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "insufficient funds" {
		t.Errorf("error_message = %v", got.ErrorMessage)
	}
}

func TestTimeoutRefundLifecycle(t *testing.T) {
	s := tempDB(t)

	s.Insert(Tx{ID: "tx-timeout", Type: "trade", Status: "pending"})
	s.UpdateStatus("tx-timeout", "submitted")
	s.RecordEvent("tx-timeout", "taker_confirmed", `{}`)
	s.RecordEvent("tx-timeout", "trade_expired", `{}`)
	s.UpdateStatus("tx-timeout", "refunded", WithFinalEvent("trade_cleanup_complete"))

	got, _ := s.Get("tx-timeout")
	if got.Status != "refunded" {
		t.Errorf("status = %q, want refunded", got.Status)
	}
	if got.FinalEvent == nil || *got.FinalEvent != "trade_cleanup_complete" {
		t.Errorf("final_event = %v, want trade_cleanup_complete", got.FinalEvent)
	}
}

func TestMigrationFromV1(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v1.db")

	// Create a v1 database manually.
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rawDB.Exec("PRAGMA journal_mode=WAL")
	_, err = rawDB.Exec(`
		CREATE TABLE transactions (
			id TEXT PRIMARY KEY, type TEXT NOT NULL, status TEXT NOT NULL,
			from_asset TEXT, from_amount TEXT, to_asset TEXT, to_amount TEXT, rate TEXT,
			counterparty TEXT, quote_id TEXT, trade_id TEXT, trade_cid TEXT, allocation_id TEXT,
			created_at TEXT NOT NULL, settled_at TEXT, raw_result TEXT
		);
		CREATE INDEX idx_tx_type ON transactions(type);
		CREATE INDEX idx_tx_status ON transactions(status);
		CREATE INDEX idx_tx_created ON transactions(created_at);
		CREATE INDEX idx_tx_asset ON transactions(from_asset);
		PRAGMA user_version = 1;
	`)
	if err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	rawDB.Exec(`INSERT INTO transactions (id, type, status, created_at) VALUES ('old-tx', 'trade', 'settled', '2026-01-01T00:00:00Z')`)
	rawDB.Close()

	// Open with our migrator — should upgrade to v2.
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open v1 db: %v", err)
	}
	defer s.Close()

	// Verify old data survived.
	got, err := s.Get("old-tx")
	if err != nil {
		t.Fatalf("get old tx: %v", err)
	}
	if got == nil || got.Status != "settled" {
		t.Fatalf("old tx not found or wrong status: %v", got)
	}
	// event_count should default to 0 for migrated rows.
	if got.EventCount != 0 {
		t.Errorf("event_count = %d, want 0 for migrated row", got.EventCount)
	}

	// Verify new columns work.
	s.Insert(Tx{
		ID: "new-tx", Type: "trade", Status: "pending",
		ErrorMessage: sp("test error"),
	})
	got2, _ := s.Get("new-tx")
	if got2.ErrorMessage == nil || *got2.ErrorMessage != "test error" {
		t.Errorf("new column error_message not working: %v", got2.ErrorMessage)
	}

	// Verify trade_events table exists.
	if err := s.RecordEvent("new-tx", "test", `{}`); err != nil {
		t.Fatalf("record event on migrated db: %v", err)
	}
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	time.Sleep(time.Nanosecond)
	id2 := GenerateID()
	if id1 == id2 {
		t.Errorf("IDs should be unique, both were %q", id1)
	}
	if len(id1) < 10 {
		t.Errorf("ID too short: %q", id1)
	}
}
