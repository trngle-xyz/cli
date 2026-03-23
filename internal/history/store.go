package history

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrInvalidTransition = errors.New("invalid status transition")

// validTransitions defines the allowed status transitions.
// Each key is the current status; the value is the set of statuses it can move to.
var validTransitions = map[string]map[string]bool{
	"pending": {
		"submitted":      true,
		"sign_failed":    true,
		"context_failed": true,
	},
	"submitted": {
		"settled":  true,
		"failed":   true,
		"expired":  true,
		"refunded": true,
	},
}

// Tx represents a single transaction record (trade or transfer).
type Tx struct {
	ID           string  `json:"id"`
	Type         string  `json:"type"`            // trade | transfer_in | transfer_out
	Status       string  `json:"status"`          // pending | submitted | settled | failed | sign_failed | context_failed | refunded | expired
	FromAsset    *string `json:"from_asset"`
	FromAmount   *string `json:"from_amount"`
	ToAsset      *string `json:"to_asset"`
	ToAmount     *string `json:"to_amount"`
	Rate         *string `json:"rate"`
	Counterparty *string `json:"counterparty"`
	QuoteID      *string `json:"quote_id"`
	TradeID      *string `json:"trade_id"`
	TradeCID     *string `json:"trade_cid"`
	AllocationID *string `json:"allocation_id"`
	CreatedAt    string  `json:"created_at"`
	SettledAt    *string `json:"settled_at"`
	RawResult    *string `json:"raw_result"`
	ErrorMessage *string `json:"error_message"`
	ErrorPhase   *string `json:"error_phase"`
	EventCount   int     `json:"event_count"`
	FinalEvent   *string `json:"final_event"`
}

// TradeEventRecord is a single row from the trade_events audit log.
type TradeEventRecord struct {
	ID        int64  `json:"id"`
	TxID      string `json:"tx_id"`
	EventType string `json:"event_type"`
	EventJSON string `json:"event_json"`
	CreatedAt string `json:"created_at"`
}

// Store wraps a SQLite database for local transaction history.
type Store struct {
	db *sql.DB
}

// DefaultPath returns ~/.trngle/history.db.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trngle", "history.db"), nil
}

// Open opens (or creates) the SQLite history database at the given path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create history dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open history db: %w", err)
	}

	// WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate history db: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// GenerateID creates a unique transaction ID based on current time.
func GenerateID() string {
	return fmt.Sprintf("tx-%s", time.Now().UTC().Format("20060102T150405.000000000"))
}

// Insert adds a new transaction record.
func (s *Store) Insert(tx Tx) error {
	if tx.ID == "" {
		tx.ID = GenerateID()
	}
	if tx.CreatedAt == "" {
		tx.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := s.db.Exec(`
		INSERT INTO transactions (
			id, type, status,
			from_asset, from_amount, to_asset, to_amount, rate,
			counterparty, quote_id, trade_id, trade_cid, allocation_id,
			created_at, settled_at, raw_result,
			error_message, error_phase, event_count, final_event
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tx.ID, tx.Type, tx.Status,
		tx.FromAsset, tx.FromAmount, tx.ToAsset, tx.ToAmount, tx.Rate,
		tx.Counterparty, tx.QuoteID, tx.TradeID, tx.TradeCID, tx.AllocationID,
		tx.CreatedAt, tx.SettledAt, tx.RawResult,
		tx.ErrorMessage, tx.ErrorPhase, tx.EventCount, tx.FinalEvent,
	)
	return err
}

// UpdateOption configures optional fields set during a status update.
type UpdateOption func(*updateParams)

type updateParams struct {
	errorMessage *string
	errorPhase   *string
	settledAt    *time.Time
	finalEvent   *string
	rawResult    *string
}

func WithError(msg, phase string) UpdateOption {
	return func(p *updateParams) {
		p.errorMessage = &msg
		p.errorPhase = &phase
	}
}

func WithSettledAt(t time.Time) UpdateOption {
	return func(p *updateParams) {
		p.settledAt = &t
	}
}

func WithFinalEvent(eventType string) UpdateOption {
	return func(p *updateParams) {
		p.finalEvent = &eventType
	}
}

func WithRawResult(json string) UpdateOption {
	return func(p *updateParams) {
		p.rawResult = &json
	}
}

// UpdateStatus atomically transitions a trade's status.
// Returns ErrInvalidTransition if the transition is not allowed by the state machine.
func (s *Store) UpdateStatus(txID string, newStatus string, opts ...UpdateOption) error {
	var params updateParams
	for _, o := range opts {
		o(&params)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Read current status under the transaction lock.
	var currentStatus string
	err = tx.QueryRow("SELECT status FROM transactions WHERE id = ?", txID).Scan(&currentStatus)
	if err == sql.ErrNoRows {
		return fmt.Errorf("transaction %s not found", txID)
	}
	if err != nil {
		return fmt.Errorf("read status: %w", err)
	}

	// Validate transition.
	allowed, ok := validTransitions[currentStatus]
	if !ok || !allowed[newStatus] {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, currentStatus, newStatus)
	}

	// Build the UPDATE dynamically based on options.
	setClauses := "status = ?, event_count = event_count + 1"
	args := []any{newStatus}

	if params.errorMessage != nil {
		setClauses += ", error_message = ?"
		args = append(args, *params.errorMessage)
	}
	if params.errorPhase != nil {
		setClauses += ", error_phase = ?"
		args = append(args, *params.errorPhase)
	}
	if params.settledAt != nil {
		setClauses += ", settled_at = ?"
		args = append(args, params.settledAt.UTC().Format(time.RFC3339))
	}
	if params.finalEvent != nil {
		setClauses += ", final_event = ?"
		args = append(args, *params.finalEvent)
	}
	if params.rawResult != nil {
		setClauses += ", raw_result = ?"
		args = append(args, *params.rawResult)
	}

	args = append(args, txID)
	_, err = tx.Exec(
		fmt.Sprintf("UPDATE transactions SET %s WHERE id = ?", setClauses),
		args...,
	)
	if err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	return tx.Commit()
}

// UpdateFields updates non-status fields on a transaction (e.g. trade_id, counterparty
// that arrive after the initial insert).
type FieldOption func(setClauses *[]string, args *[]any)

func WithTradeID(id string) FieldOption {
	return func(s *[]string, a *[]any) {
		*s = append(*s, "trade_id = ?")
		*a = append(*a, id)
	}
}

func WithTradeCID(cid string) FieldOption {
	return func(s *[]string, a *[]any) {
		*s = append(*s, "trade_cid = ?")
		*a = append(*a, cid)
	}
}

func WithCounterparty(party string) FieldOption {
	return func(s *[]string, a *[]any) {
		*s = append(*s, "counterparty = ?")
		*a = append(*a, party)
	}
}

func WithAllocationID(id string) FieldOption {
	return func(s *[]string, a *[]any) {
		*s = append(*s, "allocation_id = ?")
		*a = append(*a, id)
	}
}

func (s *Store) UpdateFields(txID string, opts ...FieldOption) error {
	if len(opts) == 0 {
		return nil
	}
	var setClauses []string
	var args []any
	for _, o := range opts {
		o(&setClauses, &args)
	}
	if len(setClauses) == 0 {
		return nil
	}

	query := "UPDATE transactions SET "
	for i, c := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += c
	}
	query += " WHERE id = ?"
	args = append(args, txID)

	_, err := s.db.Exec(query, args...)
	return err
}

// RecordEvent appends a trade event to the audit log and increments the event count.
func (s *Store) RecordEvent(txID string, eventType string, eventJSON string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT INTO trade_events (tx_id, event_type, event_json, created_at) VALUES (?, ?, ?, ?)",
		txID, eventType, eventJSON, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	_, err = tx.Exec("UPDATE transactions SET event_count = event_count + 1 WHERE id = ?", txID)
	if err != nil {
		return fmt.Errorf("increment event_count: %w", err)
	}

	return tx.Commit()
}

// scanTx reads a full Tx from a row scanner (20 columns).
func scanTx(scanner interface{ Scan(dest ...any) error }) (Tx, error) {
	var t Tx
	err := scanner.Scan(
		&t.ID, &t.Type, &t.Status,
		&t.FromAsset, &t.FromAmount, &t.ToAsset, &t.ToAmount, &t.Rate,
		&t.Counterparty, &t.QuoteID, &t.TradeID, &t.TradeCID, &t.AllocationID,
		&t.CreatedAt, &t.SettledAt, &t.RawResult,
		&t.ErrorMessage, &t.ErrorPhase, &t.EventCount, &t.FinalEvent,
	)
	return t, err
}

const txColumns = `id, type, status, from_asset, from_amount, to_asset, to_amount, rate,
	counterparty, quote_id, trade_id, trade_cid, allocation_id,
	created_at, settled_at, raw_result,
	error_message, error_phase, event_count, final_event`

// List returns transactions with pagination and optional filters.
func (s *Store) List(limit, offset int, txType, asset string) ([]Tx, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	where := "1=1"
	args := []any{}
	if txType != "" {
		where += " AND type = ?"
		args = append(args, txType)
	}
	if asset != "" {
		where += " AND from_asset = ?"
		args = append(args, asset)
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM transactions WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := fmt.Sprintf(
		"SELECT %s FROM transactions WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?",
		txColumns, where,
	)
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var txs []Tx
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, 0, err
		}
		txs = append(txs, t)
	}
	if txs == nil {
		txs = []Tx{}
	}
	return txs, total, rows.Err()
}

// ListByStatus returns transactions filtered by status.
func (s *Store) ListByStatus(status string, limit int) ([]Tx, error) {
	if limit <= 0 {
		limit = 20
	}
	query := fmt.Sprintf(
		"SELECT %s FROM transactions WHERE status = ? ORDER BY created_at DESC LIMIT ?",
		txColumns,
	)
	rows, err := s.db.Query(query, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txs []Tx
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

// Get returns a single transaction by ID.
func (s *Store) Get(id string) (*Tx, error) {
	t, err := scanTx(s.db.QueryRow(
		fmt.Sprintf("SELECT %s FROM transactions WHERE id = ?", txColumns), id,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetByTradeID looks up a transaction by operator trade ID.
func (s *Store) GetByTradeID(tradeID string) (*Tx, error) {
	t, err := scanTx(s.db.QueryRow(
		fmt.Sprintf("SELECT %s FROM transactions WHERE trade_id = ?", txColumns), tradeID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetByQuoteID looks up a transaction by quote ID.
func (s *Store) GetByQuoteID(quoteID string) (*Tx, error) {
	t, err := scanTx(s.db.QueryRow(
		fmt.Sprintf("SELECT %s FROM transactions WHERE quote_id = ?", txColumns), quoteID,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetEvents returns all events for a transaction, ordered chronologically.
func (s *Store) GetEvents(txID string) ([]TradeEventRecord, error) {
	rows, err := s.db.Query(
		"SELECT id, tx_id, event_type, event_json, created_at FROM trade_events WHERE tx_id = ? ORDER BY id ASC",
		txID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []TradeEventRecord
	for rows.Next() {
		var e TradeEventRecord
		if err := rows.Scan(&e.ID, &e.TxID, &e.EventType, &e.EventJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// migrate uses PRAGMA user_version to apply schema changes incrementally.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	if version < 1 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS transactions (
				id            TEXT PRIMARY KEY,
				type          TEXT NOT NULL,
				status        TEXT NOT NULL,
				from_asset    TEXT,
				from_amount   TEXT,
				to_asset      TEXT,
				to_amount     TEXT,
				rate          TEXT,
				counterparty  TEXT,
				quote_id      TEXT,
				trade_id      TEXT,
				trade_cid     TEXT,
				allocation_id TEXT,
				created_at    TEXT NOT NULL,
				settled_at    TEXT,
				raw_result    TEXT
			);
			CREATE INDEX IF NOT EXISTS idx_tx_type ON transactions(type);
			CREATE INDEX IF NOT EXISTS idx_tx_status ON transactions(status);
			CREATE INDEX IF NOT EXISTS idx_tx_created ON transactions(created_at);
			CREATE INDEX IF NOT EXISTS idx_tx_asset ON transactions(from_asset);
			PRAGMA user_version = 1;
		`)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v1: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v1: %w", err)
		}
		version = 1
	}

	if version < 2 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
			ALTER TABLE transactions ADD COLUMN error_message TEXT;
			ALTER TABLE transactions ADD COLUMN error_phase TEXT;
			ALTER TABLE transactions ADD COLUMN event_count INTEGER DEFAULT 0;
			ALTER TABLE transactions ADD COLUMN final_event TEXT;

			CREATE INDEX IF NOT EXISTS idx_tx_trade_id ON transactions(trade_id);
			CREATE INDEX IF NOT EXISTS idx_tx_quote_id ON transactions(quote_id);

			CREATE TABLE IF NOT EXISTS trade_events (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				tx_id      TEXT NOT NULL,
				event_type TEXT NOT NULL,
				event_json TEXT NOT NULL,
				created_at TEXT NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_te_tx_id ON trade_events(tx_id);

			PRAGMA user_version = 2;
		`)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v2: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v2: %w", err)
		}
	}

	return nil
}
