package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trngle-xyz/cli/internal/core"
	"github.com/trngle-xyz/cli/internal/history"
	"github.com/trngle-xyz/cli/internal/wallet"
)

// ServerOption configures optional Server behavior.
type ServerOption func(*Server)

// WithAuth enables bearer token authentication on all endpoints except /health.
func WithAuth(token string) ServerOption {
	return func(s *Server) {
		if token != "" {
			s.authToken = token
		}
	}
}

// WithNotifyURL enables the WebSocket event hub by connecting to the operator
// notification endpoint and rebroadcasting trade events to local clients.
func WithNotifyURL(operatorURL, partyID string) ServerOption {
	return func(s *Server) {
		s.notifyURL = operatorURL
		s.notifyPartyID = partyID
	}
}

// WithNetwork sets the network name so the server can gate trading.
func WithNetwork(network string) ServerOption {
	return func(s *Server) {
		s.network = network
	}
}

type Server struct {
	version       string
	startedAt     time.Time
	wallet        wallet.WalletAdapter
	quotes        core.QuoteClient
	historyDB     *history.Store
	authToken     string
	network       string
	hub           *Hub
	notifyURL     string
	notifyPartyID string
	notifyClient  *core.NotifyClient
	httpServer    *http.Server
}

func NewServer(addr string, walletAdapter wallet.WalletAdapter, quoteClient core.QuoteClient, historyDB *history.Store, opts ...ServerOption) *Server {
	s := &Server{
		version:   "0.1.0",
		startedAt: time.Now(),
		wallet:    walletAdapter,
		quotes:    quoteClient,
		historyDB: historyDB,
	}
	for _, o := range opts {
		o(s)
	}

	s.hub = NewHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/balances", s.requireAuth(s.handleBalances))
	mux.HandleFunc("/trade/quote", s.requireAuth(s.handleTradeQuote))
	mux.HandleFunc("/trade/", s.requireAuth(s.handleTradeConfirm))
	mux.HandleFunc("/trades", s.requireAuth(s.handleTrades))
	mux.HandleFunc("/trades/", s.requireAuth(s.handleTradeByID))
	mux.HandleFunc("/transfers", s.requireAuth(s.handleTransfers))
	mux.HandleFunc("/transfers/", s.requireAuth(s.handleTransferAccept))
	mux.HandleFunc("/ws", s.requireAuth(s.handleWS))

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

// requireAuth wraps a handler with bearer token validation when authToken is set.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authToken != "" {
			auth := r.Header.Get("Authorization")
			if !strings.EqualFold(auth, "Bearer "+s.authToken) {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid or missing bearer token")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) Start() error {
	go s.hub.Run()

	if s.notifyURL != "" && s.notifyPartyID != "" {
		s.notifyClient = core.NewNotifyClient(s.notifyURL, "taker", s.notifyPartyID, func(evt core.TradeEvent) {
			s.hub.Broadcast(evt)
		})
		s.notifyClient.Connect()
	}

	return s.httpServer.ListenAndServe()
}

func (s *Server) Close() error {
	s.hub.Stop()
	if s.notifyClient != nil {
		s.notifyClient.Close()
	}
	return s.httpServer.Close()
}

// Shutdown gracefully drains in-flight requests before closing.
func (s *Server) Shutdown(ctx context.Context) error {
	s.hub.Stop()
	if s.notifyClient != nil {
		s.notifyClient.Close()
	}
	return s.httpServer.Shutdown(ctx)
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"wallet": map[string]any{
			"connected": s.wallet.PartyID() != "",
			"provider":  "loop",
			"party_id":  s.wallet.PartyID(),
		},
		"version":        s.version,
		"uptime_seconds": int(time.Since(s.startedAt).Seconds()),
	})
}

// ---------------------------------------------------------------------------
// GET /balances
// ---------------------------------------------------------------------------

func (s *Server) handleBalances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	balances, err := s.wallet.GetBalances()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "wallet_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balances": balances})
}

// ---------------------------------------------------------------------------
// POST /trade/quote  — request a quote
// ---------------------------------------------------------------------------

func (s *Server) handleTradeQuote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	if s.network != "" && s.network != "testnet" {
		writeErr(w, http.StatusForbidden, "network_not_supported",
			"Trading is only available on testnet. Switch to testnet in settings to trade.")
		return
	}

	var req struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Amount string `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if req.From == "" || req.To == "" || req.Amount == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "from, to, and amount are required")
		return
	}

	quote, err := s.quotes.RequestQuote(r.Context(), req.From, req.To, req.Amount, s.wallet.PartyID())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "quote_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"quote_id":    quote.ID,
		"from":        quote.FromAsset,
		"to":          quote.ToAsset,
		"send_amount": quote.FromAmount,
		"receive_amount": quote.ToAmount,
		"rate":        quote.Rate,
		"expires_in":  quote.TTLSeconds,
		"expires_at":  quote.ExpiresAt,
	})
}

// ---------------------------------------------------------------------------
// POST /trade/{quote_id}/confirm  — execute a quoted trade (full flow)
// ---------------------------------------------------------------------------

func (s *Server) handleTradeConfirm(w http.ResponseWriter, r *http.Request) {
	if s.network != "" && s.network != "testnet" {
		writeErr(w, http.StatusForbidden, "network_not_supported",
			"Trading is only available on testnet. Switch to testnet in settings to trade.")
		return
	}

	// Parse: /trade/{quote_id}/confirm
	path := strings.TrimPrefix(r.URL.Path, "/trade/")
	if !strings.HasSuffix(path, "/confirm") {
		writeErr(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	quoteID := strings.TrimSuffix(path, "/confirm")
	quoteID = strings.TrimSuffix(quoteID, "/")
	if quoteID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "quote_id is required")
		return
	}

	// Step 1: Get accept context (asset/amount metadata for the trade record).
	acceptCtx, err := s.quotes.AcceptQuoteContext(r.Context(), quoteID)
	if err != nil {
		if strings.Contains(err.Error(), "expired") {
			writeErr(w, http.StatusGone, "quote_expired", "This quote has expired. Request a new one.")
			return
		}
		writeErr(w, http.StatusNotFound, "quote_not_found", "Quote not found. It may have already been used or expired.")
		return
	}

	// Step 2: Get the signing payload from the operator.
	payload, err := s.quotes.AcceptQuote(r.Context(), quoteID)
	if err != nil {
		if strings.Contains(err.Error(), "expired") {
			writeErr(w, http.StatusGone, "quote_expired", "This quote has expired. Request a new one.")
			return
		}
		writeErr(w, http.StatusInternalServerError, "trade_failed", "Failed to prepare trade. Please try again.")
		return
	}

	// Step 3: Sign and submit the transaction on-chain.
	txResult, err := s.wallet.PrepareAndSubmit(*payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "trade_failed", "Transaction signing failed. Please try again.")
		return
	}

	// Step 4: Confirm with the operator.
	if err := s.quotes.ConfirmQuote(r.Context(), quoteID, txResult); err != nil {
		writeErr(w, http.StatusInternalServerError, "trade_failed", "Trade submitted but confirmation failed. Check /trades for status.")
		return
	}

	// Build the trade record.
	tradeID := acceptCtx.TradeID
	if tradeID == "" {
		tradeID = fmt.Sprintf("TRADE-%d", time.Now().UnixNano())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.historyDB.Insert(history.Tx{
		Type:         "trade",
		Status:       "submitted",
		FromAsset:    strPtr(acceptCtx.TakerLeg.Asset),
		FromAmount:   strPtr(acceptCtx.TakerLeg.Amount),
		ToAsset:      strPtr(acceptCtx.MakerLeg.Asset),
		ToAmount:     strPtr(acceptCtx.MakerLeg.Amount),
		Counterparty: strPtr(acceptCtx.MakerParty),
		QuoteID:      strPtr(quoteID),
		TradeID:      strPtr(tradeID),
		TradeCID:     strPtr(acceptCtx.TradeCID),
		CreatedAt:    now,
	}); err != nil {
		log.Printf("history: insert failed: %v", err)
	}

	s.hub.Broadcast(core.TradeEvent{
		Type:    "taker_confirmed",
		QuoteID: quoteID,
		TradeID: tradeID,
		Status:  "submitted",
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"trade_id": tradeID,
		"status":   "submitted",
		"from":     acceptCtx.TakerLeg.Asset,
		"to":       acceptCtx.MakerLeg.Asset,
		"sent":     acceptCtx.TakerLeg.Amount,
		"received": acceptCtx.MakerLeg.Amount,
		"message":  "Trade submitted. Settlement is in progress.",
	})
}

// ---------------------------------------------------------------------------
// GET /trades          — list trades (paginated)
// GET /trades/{id}     — single trade by ID
// ---------------------------------------------------------------------------

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 20)
	offset := parseIntOrDefault(r.URL.Query().Get("offset"), 0)
	txType := r.URL.Query().Get("type")
	asset := r.URL.Query().Get("asset")

	txs, total, err := s.historyDB.List(limit, offset, txType, asset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "history_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trades": txs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleTradeByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/trades/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "trade id is required")
		return
	}

	tx, err := s.historyDB.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "history_error", err.Error())
		return
	}
	if tx == nil {
		writeErr(w, http.StatusNotFound, "not_found", "trade not found")
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

// ---------------------------------------------------------------------------
// GET  /transfers          — list pending incoming transfers
// POST /transfers/{id}/accept — accept a transfer
// ---------------------------------------------------------------------------

func (s *Server) handleTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	transfers, err := s.wallet.GetPendingTransfers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "wallet_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": transfers})
}

func (s *Server) handleTransferAccept(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/transfers/")
	if !strings.HasSuffix(path, "/accept") {
		writeErr(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	transferID := strings.TrimSuffix(path, "/accept")
	transferID = strings.TrimSuffix(transferID, "/")
	if transferID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "transfer id is required")
		return
	}

	// Look up the pending transfer to capture asset/amount before accepting.
	var transferAsset, transferAmount, transferFrom string
	if pending, err := s.wallet.GetPendingTransfers(); err == nil {
		for _, t := range pending {
			if t.ID == transferID {
				transferAsset = t.Asset
				transferAmount = t.Amount
				transferFrom = t.From
				break
			}
		}
	}

	if _, err := s.wallet.AcceptTransfer(transferID); err != nil {
		writeErr(w, http.StatusInternalServerError, "accept_failed", err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	htx := history.Tx{
		Type:      "transfer_in",
		Status:    "accepted",
		CreatedAt: now,
		SettledAt: strPtr(now),
	}
	if transferAsset != "" {
		htx.ToAsset = strPtr(transferAsset)
		htx.ToAmount = strPtr(transferAmount)
		htx.Counterparty = strPtr(transferFrom)
	}
	if err := s.historyDB.Insert(htx); err != nil {
		log.Printf("history: insert failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "accepted",
		"asset":   transferAsset,
		"amount":  transferAmount,
		"from":    transferFrom,
		"message": "Transfer accepted.",
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("writeJSON: encode failed: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, errCode, message string) {
	writeJSON(w, code, map[string]string{
		"error":   errCode,
		"message": message,
	})
}

func parseIntOrDefault(v string, defaultVal int) int {
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func strPtr(v string) *string {
	return &v
}
