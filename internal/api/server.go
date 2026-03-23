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

type Server struct {
	version    string
	startedAt  time.Time
	wallet     wallet.WalletAdapter
	quotes     core.QuoteClient
	historyDB  *history.Store
	httpServer *http.Server
}

func NewServer(addr string, walletAdapter wallet.WalletAdapter, quoteClient core.QuoteClient, historyDB *history.Store) *Server {
	s := &Server{
		version:   "0.1.0",
		startedAt: time.Now(),
		wallet:    walletAdapter,
		quotes:    quoteClient,
		historyDB: historyDB,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/balances", s.handleBalances)
	mux.HandleFunc("/quote", s.handleQuote)
	mux.HandleFunc("/quote/", s.handleQuoteRoutes)
	mux.HandleFunc("/transfers/pending", s.handleTransfersPending)
	mux.HandleFunc("/transfers/", s.handleTransferRoutes)
	mux.HandleFunc("/history", s.handleHistory)
	mux.HandleFunc("/history/", s.handleHistoryByID)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Close() error {
	return s.httpServer.Close()
}

// Shutdown gracefully drains in-flight requests before closing.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

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

func (s *Server) handleQuote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
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

	quote, err := s.quotes.RequestQuote(r.Context(), req.From, req.To, req.Amount, s.wallet.PartyID())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_quote", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func (s *Server) handleQuoteRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/quote/")
	if !strings.HasSuffix(path, "/accept") {
		writeErr(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported")
		return
	}

	quoteID := strings.TrimSuffix(path, "/accept")
	quoteID = strings.TrimSuffix(quoteID, "/")
	if quoteID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_quote_id", "quote id is required")
		return
	}

	payload, err := s.quotes.AcceptQuote(r.Context(), quoteID)
	if err != nil {
		if strings.Contains(err.Error(), "expired") {
			writeErr(w, http.StatusGone, "quote_expired", err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, "quote_not_found", err.Error())
		return
	}

	tx, err := s.wallet.PrepareAndSubmit(*payload)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "signing_failed", err.Error())
		return
	}
	if err := s.quotes.ConfirmQuote(r.Context(), quoteID, tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "confirm_failed", err.Error())
		return
	}

	tradeID := fmt.Sprintf("TRADE-%d", time.Now().UnixNano())
	status := "submitted"
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.historyDB.Insert(history.Tx{
		Type:      "trade",
		Status:    status,
		QuoteID:   strPtr(quoteID),
		TradeID:   strPtr(tradeID),
		CreatedAt: now,
		SettledAt: strPtr(now),
	}); err != nil {
		log.Printf("history: insert failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   status,
		"quote_id": quoteID,
		"trade_id": tradeID,
		"message":  "Trade confirmed. Settlement is in progress.",
	})
}

func (s *Server) handleTransfersPending(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleTransferRoutes(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "invalid_transfer_id", "transfer id is required")
		return
	}

	if _, err := s.wallet.AcceptTransfer(transferID); err != nil {
		writeErr(w, http.StatusInternalServerError, "accept_failed", err.Error())
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.historyDB.Insert(history.Tx{
		Type:      "transfer_in",
		Status:    "accepted",
		QuoteID:   nil,
		TradeID:   nil,
		CreatedAt: now,
		SettledAt: strPtr(now),
	}); err != nil {
		log.Printf("history: insert failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "accepted",
		"transfer_id": transferID,
		"message":     "Transfer accepted. Tokens received.",
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
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
		"transactions": txs,
		"total":        total,
		"limit":        limit,
		"offset":       offset,
	})
}

func (s *Server) handleHistoryByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/history/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid_id", "history id is required")
		return
	}

	tx, err := s.historyDB.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "history_error", err.Error())
		return
	}
	if tx == nil {
		writeErr(w, http.StatusNotFound, "not_found", "transaction not found")
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

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

