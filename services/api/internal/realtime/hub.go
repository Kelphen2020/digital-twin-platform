// Package realtime owns the WebSocket hub used to fan events out to browsers.
package realtime

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Frame is the JSON envelope of docs/API.md §WebSocket.
type Frame struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 25 * time.Second
	maxMessage = 4096
)

// Hub tracks connected clients and broadcasts JSON frames to all of them.
type Hub struct {
	// OnConnect, when set, supplies the greeting frame sent to each new client
	// before any broadcast frame arrives.
	OnConnect func() Frame

	mu      sync.RWMutex
	clients map[*client]struct{}
	count   atomic.Int64
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// Clients is the current number of live WebSocket clients.
func (h *Hub) Clients() int { return int(h.count.Load()) }

// Broadcast marshals a frame and queues it for every client. Slow clients are
// dropped rather than blocking ingest (same policy as the production design).
func (h *Hub) Broadcast(frame Frame) {
	payload, err := json.Marshal(frame)
	if err != nil {
		log.Printf("realtime: marshal frame: %v", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- payload:
		default:
		}
	}
}

// ServeHTTP upgrades an HTTP request to a WebSocket and starts its pumps.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		// P0 runs on a trusted local network; the production deployment will
		// pin allowed origins per site.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("realtime: upgrade: %v", err)
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 256)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	h.count.Add(1)

	if h.OnConnect != nil {
		if payload, err := json.Marshal(h.OnConnect()); err == nil {
			select {
			case c.send <- payload:
			default:
			}
		}
	}
	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
		h.count.Add(-1)
	}
	h.mu.Unlock()
}

// readPump drains client frames (the P0 client sends none) and detects close.
func (h *Hub) readPump(c *client) {
	defer func() {
		h.remove(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessage)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
