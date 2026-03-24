package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/trngle-xyz/cli/internal/core"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// client is a single WebSocket subscriber.
type client struct {
	conn   *websocket.Conn
	send   chan []byte
	filter string // optional trade_id filter; empty = all events
	done   chan struct{}
	once   sync.Once
}

func newClient(conn *websocket.Conn, filter string) *client {
	return &client{
		conn:   conn,
		send:   make(chan []byte, 64),
		filter: filter,
		done:   make(chan struct{}),
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// writePump sends queued messages to the WebSocket connection.
func (c *client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// readPump reads (and discards) messages; detects disconnection.
func (c *client) readPump(hub *Hub) {
	defer func() {
		hub.unregister <- c
		c.close()
	}()
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// eventMsg is sent through the hub's internal channel so Run() can
// filter per-client by trade_id without needing external synchronization.
type eventMsg struct {
	tradeID string
	data    []byte
}

// Hub manages connected WebSocket clients and broadcasts trade events.
type Hub struct {
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	events     chan eventMsg
	done       chan struct{}
	once       sync.Once
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		events:     make(chan eventMsg, 256),
		done:       make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case evt := <-h.events:
			for c := range h.clients {
				// Skip clients that have a filter and it doesn't match.
				if c.filter != "" && c.filter != evt.tradeID {
					continue
				}
				select {
				case c.send <- evt.data:
				default:
					// Client too slow — disconnect it.
					delete(h.clients, c)
					close(c.send)
				}
			}
		case <-h.done:
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			return
		}
	}
}

func (h *Hub) Stop() {
	h.once.Do(func() { close(h.done) })
}

// Broadcast serializes a TradeEvent and sends it to all matching clients.
func (h *Hub) Broadcast(evt core.TradeEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		log.Printf("hub: marshal event: %v", err)
		return
	}
	select {
	case h.events <- eventMsg{tradeID: evt.TradeID, data: data}:
	default:
		log.Printf("hub: event channel full, dropping event")
	}
}

// handleWS upgrades the HTTP connection to a WebSocket and registers the client.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	filter := r.URL.Query().Get("trade_id")
	c := newClient(conn, filter)

	// Replay recent events from history if a trade_id filter is specified.
	if filter != "" && s.historyDB != nil {
		if events, err := s.historyDB.GetEvents(filter); err == nil {
			for _, evt := range events {
				msg, _ := json.Marshal(map[string]string{
					"type":       evt.EventType,
					"trade_id":   filter,
					"event_json": evt.EventJSON,
					"created_at": evt.CreatedAt,
					"replayed":   "true",
				})
				select {
				case c.send <- msg:
				default:
				}
			}
		}
	}

	s.hub.register <- c
	go c.writePump()
	go c.readPump(s.hub)
}
