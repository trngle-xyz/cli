package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperatorAcceptQuoteContextParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/quote/QT-1/accept" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quote_id":               "QT-1",
			"trade_cid":              "00tradecid",
			"trade_id":               "ST-1",
			"operator_party":         "operator::party",
			"taker_party":            "taker::party",
			"maker_party":            "maker::party",
			"trade_template_id":      "#trngle-cip56:V2.TradeConfirmV2:TradeConfirmV2",
			"allocation_factory_cid": "00factorycid",
			"disclosed_contracts":    []any{},
			"taker_leg": map[string]any{
				"asset":  "CC",
				"amount": "1.0000000000",
			},
			"maker_leg": map[string]any{
				"asset":  "CBTC",
				"amount": "0.0000200000",
			},
			"quote_expires_at":  "2026-03-03T12:00:00Z",
			"confirm_before":    "2026-03-03T12:00:00Z",
			"accept_context_id": "AC-1",
			"status":            "ready_for_taker_confirm",
		})
	}))
	defer srv.Close()

	client := NewQuoteAPIClient(srv.URL)
	ctx, err := client.AcceptQuoteContext(context.Background(), "QT-1")
	if err != nil {
		t.Fatalf("AcceptQuoteContext error: %v", err)
	}
	if ctx == nil {
		t.Fatalf("expected non-nil context")
	}
	if ctx.AcceptContextID != "AC-1" {
		t.Fatalf("unexpected accept_context_id: %s", ctx.AcceptContextID)
	}
	if ctx.TradeCID != "00tradecid" {
		t.Fatalf("unexpected trade_cid: %s", ctx.TradeCID)
	}
}

func TestOperatorAcceptQuoteRejectsEmptyCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commands": []any{},
		})
	}))
	defer srv.Close()

	client := NewQuoteAPIClient(srv.URL)
	_, err := client.AcceptQuote(context.Background(), "QT-1")
	if err == nil || !strings.Contains(err.Error(), "quote unavailable") {
		t.Fatalf("expected empty payload error, got %v", err)
	}
}

func TestOperatorAcceptQuoteContextPropagatesStructuredError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "accept_required",
			"message": "quote must be accepted before confirm",
		})
	}))
	defer srv.Close()

	client := NewQuoteAPIClient(srv.URL)
	_, err := client.AcceptQuoteContext(context.Background(), "QT-1")
	if err == nil {
		t.Fatalf("expected accept context error")
	}
	if !strings.Contains(err.Error(), "accept_required") {
		t.Fatalf("expected structured error, got %v", err)
	}
}

func TestOperatorConfirmQuotePropagatesStructuredError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "settlement_failed",
			"message": "maker rejected",
		})
	}))
	defer srv.Close()

	client := NewQuoteAPIClient(srv.URL)
	err := client.ConfirmQuote(context.Background(), "QT-1", map[string]any{"command_id": "c1"})
	if err == nil {
		t.Fatalf("expected confirm error")
	}
	if !strings.Contains(err.Error(), "settlement_failed") || !strings.Contains(err.Error(), "maker rejected") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestOperatorConfirmQuoteAccepts202(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "confirm_received_verifying",
		})
	}))
	defer srv.Close()

	client := NewQuoteAPIClient(srv.URL)
	if err := client.ConfirmQuote(context.Background(), "QT-1", map[string]any{"accept_context_id": "AC-1"}); err != nil {
		t.Fatalf("expected 202 to be treated as success, got: %v", err)
	}
}
