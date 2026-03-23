package core

import (
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TradeEvent represents a notification from the operator about trade lifecycle changes.
type TradeEvent struct {
	Type       string `json:"type"`
	QuoteID    string `json:"quote_id"`
	TradeID    string `json:"trade_id"`
	TradeCID   string `json:"trade_cid"`
	Status     string `json:"status"`
	TradeState string `json:"trade_state"`
	Action     string `json:"action"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	Reason     string `json:"reason,omitempty"`
	MakerID    string `json:"maker_id"`
	TakerID    string `json:"taker_id"`
	TakerParty string `json:"taker_party"`
	MakerParty string `json:"maker_party"`
	Phase      string `json:"phase,omitempty"`
}

// NotifyClient connects to the operator's notification WebSocket
// and delivers trade lifecycle events to a callback.
type NotifyClient struct {
	baseURL  string
	role     string
	partyID  string
	callback func(TradeEvent)

	mu   sync.Mutex
	conn *websocket.Conn
	done chan struct{}
}

// NewNotifyClient creates a notification client. Call Connect() to start receiving events.
func NewNotifyClient(operatorBaseURL, role, partyID string, callback func(TradeEvent)) *NotifyClient {
	return &NotifyClient{
		baseURL:  operatorBaseURL,
		role:     role,
		partyID:  partyID,
		callback: callback,
		done:     make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection and starts reading events.
// It reconnects automatically on disconnect. Non-blocking — runs in background.
func (nc *NotifyClient) Connect() {
	go nc.connectLoop()
}

// Close shuts down the notification client.
func (nc *NotifyClient) Close() {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	select {
	case <-nc.done:
	default:
		close(nc.done)
	}
	if nc.conn != nil {
		_ = nc.conn.Close()
	}
}

func (nc *NotifyClient) connectLoop() {
	for {
		select {
		case <-nc.done:
			return
		default:
		}

		err := nc.dial()
		if err != nil {
			log.Printf("notify: connection failed: %v", err)
			select {
			case <-nc.done:
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		nc.readLoop()

		// readLoop exited — connection lost, reconnect
		select {
		case <-nc.done:
			return
		case <-time.After(1 * time.Second):
		}
	}
}

func (nc *NotifyClient) dial() error {
	// Convert http(s) base URL to ws(s)
	wsURL := nc.baseURL
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)

	u, err := url.Parse(wsURL + "/api/v1/notifications/ws")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("role", nc.role)
	q.Set("id", nc.partyID)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}

	nc.mu.Lock()
	if nc.conn != nil {
		nc.conn.Close()
	}
	nc.conn = conn
	nc.mu.Unlock()
	return nil
}

func (nc *NotifyClient) readLoop() {
	nc.mu.Lock()
	conn := nc.conn
	nc.mu.Unlock()
	if conn == nil {
		return
	}

	for {
		select {
		case <-nc.done:
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var evt TradeEvent
		if err := json.Unmarshal(message, &evt); err != nil {
			continue
		}
		if nc.callback != nil {
			nc.callback(evt)
		}
	}
}
