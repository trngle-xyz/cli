package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trngle-xyz/cli/internal/wallet"
)

// QuoteAPIClient implements QuoteClient by calling the operator HTTP API.
// BaseURL should be the operator root (e.g. http://localhost:8080) without trailing slash.
// Request/response shapes match docs/design/OPERATOR_DESIGN.md.
type QuoteAPIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewQuoteAPIClient(baseURL, apiKey string) *QuoteAPIClient {
	if baseURL == "" {
		baseURL = "https://api.trngle.com"
	}
	return &QuoteAPIClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Operator API request/response types (match operator API contract)

type operatorQuoteRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Amount  string `json:"amount"`
	PartyID string `json:"party_id,omitempty"`
}

type operatorQuoteResponse struct {
	QuoteID    string         `json:"quote_id"`
	FromAsset  string         `json:"from_asset"`
	ToAsset    string         `json:"to_asset"`
	FromAmount stringOrNumber `json:"from_amount"`
	ToAmount   stringOrNumber `json:"to_amount"`
	Rate       stringOrNumber `json:"rate"`
	TTLSeconds intOrString    `json:"ttl_seconds"`
	ExpiresAt  string         `json:"expires_at"`
}

type operatorAssetsResponse struct {
	Assets []struct {
		Symbol string         `json:"symbol"`
		Price  stringOrNumber `json:"price,omitempty"`
	} `json:"assets"`
}

type operatorErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type operatorAcceptContextResponse struct {
	QuoteID         string               `json:"quote_id"`
	TradeCID        string               `json:"trade_cid"`
	TradeID         string               `json:"trade_id"`
	OperatorParty   string               `json:"operator_party"`
	TakerParty      string               `json:"taker_party"`
	MakerParty      string               `json:"maker_party"`
	TradeTemplateID string               `json:"trade_template_id"`
	FactoryCID      string               `json:"allocation_factory_cid"`
	Disclosed       []any                `json:"disclosed_contracts"`
	TakerLeg        operatorQuoteLegView `json:"taker_leg"`
	MakerLeg        operatorQuoteLegView `json:"maker_leg"`
	QuoteExpiresAt  string               `json:"quote_expires_at"`
	ConfirmBefore   string               `json:"confirm_before"`
	AcceptContextID string               `json:"accept_context_id"`
	Status          string               `json:"status"`
}

type operatorQuoteLegView struct {
	Asset  string         `json:"asset"`
	Amount stringOrNumber `json:"amount"`
}

type stringOrNumber string

func (s *stringOrNumber) UnmarshalJSON(data []byte) error {
	raw := bytes.TrimSpace(data)
	if len(raw) == 0 || string(raw) == "null" {
		*s = ""
		return nil
	}
	if raw[0] == '"' {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		*s = stringOrNumber(strings.TrimSpace(v))
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var num json.Number
	if err := dec.Decode(&num); err == nil {
		*s = stringOrNumber(num.String())
		return nil
	}
	var anyValue any
	if err := json.Unmarshal(raw, &anyValue); err != nil {
		return err
	}
	*s = stringOrNumber(strings.TrimSpace(fmt.Sprint(anyValue)))
	return nil
}

type intOrString int

func (v *intOrString) UnmarshalJSON(data []byte) error {
	raw := bytes.TrimSpace(data)
	if len(raw) == 0 || string(raw) == "null" {
		*v = 0
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*v = 0
			return nil
		}
		i, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		*v = intOrString(i)
		return nil
	}
	var i int
	if err := json.Unmarshal(raw, &i); err == nil {
		*v = intOrString(i)
		return nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		*v = intOrString(int(f))
		return nil
	}
	return fmt.Errorf("invalid integer value: %s", string(raw))
}

// doRequest builds an HTTP request with the given context, method, URL, and
// optional JSON body, executes it, reads the response body, and returns the
// body bytes together with the status code. It returns an error if the request
// cannot be created, executed, or if the response body cannot be read.
func (c *QuoteAPIClient) doRequest(ctx context.Context, method, url string, body []byte) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("operator request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

func (c *QuoteAPIClient) GetAssets(ctx context.Context) ([]Asset, error) {
	body, status, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/api/v1/assets", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, operatorStatusError(status, body)
	}
	var out operatorAssetsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse assets: %w", err)
	}
	assets := make([]Asset, 0, len(out.Assets))
	for _, a := range out.Assets {
		assets = append(assets, Asset{Symbol: a.Symbol})
	}
	return assets, nil
}

func (c *QuoteAPIClient) RequestQuote(ctx context.Context, from, to, amount, partyID string) (*Quote, error) {
	reqBody := operatorQuoteRequest{From: from, To: to, Amount: amount, PartyID: partyID}
	enc, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal quote request: %w", err)
	}
	body, status, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/api/v1/quote", enc)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, operatorStatusError(status, body)
	}
	var out operatorQuoteResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse quote: %w", err)
	}
	expiresAt, _ := time.Parse(time.RFC3339, out.ExpiresAt)
	return &Quote{
		ID:         out.QuoteID,
		FromAsset:  out.FromAsset,
		ToAsset:    out.ToAsset,
		FromAmount: string(out.FromAmount),
		ToAmount:   string(out.ToAmount),
		Rate:       string(out.Rate),
		TTLSeconds: int(out.TTLSeconds),
		ExpiresAt:  expiresAt,
	}, nil
}

func (c *QuoteAPIClient) AcceptQuote(ctx context.Context, quoteID string) (*wallet.CommandPayload, error) {
	body, status, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/api/v1/quote/"+quoteID+"/accept", nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("quote not found")
	}
	if status == http.StatusGone {
		return nil, fmt.Errorf("quote expired")
	}
	if status != http.StatusOK {
		return nil, operatorStatusError(status, body)
	}
	var payload wallet.CommandPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse command payload: %w", err)
	}
	if len(payload.Commands) == 0 {
		return nil, fmt.Errorf("quote unavailable: operator returned no executable payload")
	}
	return &payload, nil
}

func (c *QuoteAPIClient) AcceptQuoteContext(ctx context.Context, quoteID string) (*AcceptContext, error) {
	body, status, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/api/v1/quote/"+quoteID+"/accept", nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("quote not found")
	}
	if status == http.StatusGone {
		return nil, fmt.Errorf("quote expired")
	}
	if status != http.StatusOK {
		return nil, operatorStatusError(status, body)
	}

	var out operatorAcceptContextResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse accept context: %w", err)
	}
	if err := validateAcceptContextResponse(out, body); err != nil {
		return nil, err
	}

	quoteExpiresAt, _ := time.Parse(time.RFC3339, out.QuoteExpiresAt)
	confirmBefore, _ := time.Parse(time.RFC3339, out.ConfirmBefore)

	return &AcceptContext{
		QuoteID:         out.QuoteID,
		TradeCID:        out.TradeCID,
		TradeID:         out.TradeID,
		OperatorParty:   out.OperatorParty,
		TakerParty:      out.TakerParty,
		MakerParty:      out.MakerParty,
		TradeTemplateID: out.TradeTemplateID,
		FactoryCID:      out.FactoryCID,
		Disclosed:       out.Disclosed,
		TakerLeg: QuoteLeg{
			Asset:  out.TakerLeg.Asset,
			Amount: string(out.TakerLeg.Amount),
		},
		MakerLeg: QuoteLeg{
			Asset:  out.MakerLeg.Asset,
			Amount: string(out.MakerLeg.Amount),
		},
		QuoteExpiresAt:  quoteExpiresAt,
		ConfirmBefore:   confirmBefore,
		AcceptContextID: out.AcceptContextID,
		Status:          out.Status,
	}, nil
}

func validateAcceptContextResponse(out operatorAcceptContextResponse, body []byte) error {
	missing := make([]string, 0, 8)
	if strings.TrimSpace(out.QuoteID) == "" {
		missing = append(missing, "quote_id")
	}
	if strings.TrimSpace(out.TradeCID) == "" {
		missing = append(missing, "trade_cid")
	}
	if strings.TrimSpace(out.TradeID) == "" {
		missing = append(missing, "trade_id")
	}
	if strings.TrimSpace(out.OperatorParty) == "" {
		missing = append(missing, "operator_party")
	}
	if strings.TrimSpace(out.TakerParty) == "" {
		missing = append(missing, "taker_party")
	}
	if strings.TrimSpace(out.MakerParty) == "" {
		missing = append(missing, "maker_party")
	}
	// FactoryCID is looked up lazily via the Canton registry at confirm time;
	// the operator may omit it without breaking the flow.
	if strings.TrimSpace(out.AcceptContextID) == "" {
		missing = append(missing, "accept_context_id")
	}
	if len(missing) == 0 {
		return nil
	}

	var legacy struct {
		Commands []any `json:"commands"`
	}
	if err := json.Unmarshal(body, &legacy); err == nil && legacy.Commands != nil {
		if len(legacy.Commands) == 0 {
			return fmt.Errorf("operator /accept returned legacy placeholder payload (commands empty); deploy latest operator with accept-context support")
		}
		return fmt.Errorf("operator /accept returned legacy command payload; deploy latest operator with accept-context support")
	}

	return fmt.Errorf("operator /accept response missing required accept-context fields: %s", strings.Join(missing, ", "))
}

func (c *QuoteAPIClient) ConfirmQuote(ctx context.Context, quoteID string, txResult any) error {
	enc, err := json.Marshal(txResult)
	if err != nil {
		return fmt.Errorf("marshal confirm request: %w", err)
	}
	body, status, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/api/v1/quote/"+quoteID+"/confirm", enc)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("quote not found")
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return operatorStatusError(status, body)
	}
	return nil
}

func (c *QuoteAPIClient) ReportError(ctx context.Context, quoteID string, report ErrorReport) error {
	enc, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal error report: %w", err)
	}
	body, status, err := c.doRequest(ctx, http.MethodPost, c.baseURL+"/api/v1/quote/"+quoteID+"/error", enc)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		return nil // quote already expired from operator memory, nothing to clean
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return operatorStatusError(status, body)
	}
	return nil
}

func (c *QuoteAPIClient) GetTradeStatus(ctx context.Context, tradeID string) (*TradeStatus, error) {
	body, status, err := c.doRequest(ctx, http.MethodGet, c.baseURL+"/api/v1/trades", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, operatorStatusError(status, body)
	}
	var out struct {
		Trades []struct {
			TradeID string `json:"trade_id"`
			Status  string `json:"status"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse trades: %w", err)
	}
	for _, t := range out.Trades {
		if t.TradeID == tradeID {
			return &TradeStatus{TradeID: t.TradeID, Status: t.Status}, nil
		}
	}
	return &TradeStatus{TradeID: tradeID, Status: "unknown"}, nil
}

func operatorStatusError(statusCode int, body []byte) error {
	var er operatorErrorResponse
	if err := json.Unmarshal(body, &er); err == nil {
		msg := strings.TrimSpace(er.Message)
		code := strings.TrimSpace(er.Error)
		if msg != "" && code != "" {
			return fmt.Errorf("%s: %s", code, msg)
		}
		if msg != "" {
			return fmt.Errorf("operator (%d): %s", statusCode, msg)
		}
	}
	raw := strings.TrimSpace(string(body))
	if raw != "" {
		if len(raw) > 200 {
			raw = raw[:200] + "..."
		}
		return fmt.Errorf("operator (%d): %s", statusCode, raw)
	}
	return fmt.Errorf("operator status %d", statusCode)
}
