package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket message envelope
type WSMessage struct {
	Type      string `json:"type"`
	Payload   any    `json:"payload"`
	Timestamp string `json:"timestamp"`
}

// Client represents a connected WebSocket client
type wsClient struct {
	conn   *websocket.Conn
	role   string // "admin", "user", or "temp"
	userID int64  // 0 for temp viewers
	send   chan []byte
}

// Hub manages all active WebSocket connections
type wsHub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

var hub = &wsHub{
	clients: make(map[*wsClient]struct{}),
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins (CORS handled elsewhere)
	},
}

func (h *wsHub) register(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	log.Printf("ws: client connected role=%s userId=%d total=%d", c.role, c.userID, len(h.clients))
}

func (h *wsHub) unregister(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	close(c.send)
	log.Printf("ws: client disconnected role=%s userId=%d total=%d", c.role, c.userID, len(h.clients))
}

// broadcast sends a message to all connected clients based on role filtering
func (h *wsHub) broadcast(msg WSMessage, filter func(c *wsClient) bool) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws: marshal error: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if filter != nil && !filter(c) {
			continue
		}
		c.safeSend(data)
	}
}

// BroadcastCrisisAlert sends crisis_alert to all connected clients
func BroadcastCrisisAlert(payload any) {
	hub.broadcast(WSMessage{
		Type:      "crisis_alert",
		Payload:   payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil) // nil filter = all clients
}

// BroadcastSystemAlarm sends system_alarm to admin clients only
func BroadcastSystemAlarm(payload any) {
	hub.broadcast(WSMessage{
		Type:      "system_alarm",
		Payload:   payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, func(c *wsClient) bool {
		return c.role == "admin"
	})
}

// safeSend attempts a non-blocking send to the client's channel.
// Recovers from panic if the channel is already closed.
func (c *wsClient) safeSend(data []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ws: send on closed channel role=%s userId=%d", c.role, c.userID)
		}
	}()
	select {
	case c.send <- data:
	default:
		log.Printf("ws: dropping message for slow client role=%s userId=%d", c.role, c.userID)
	}
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 40 * time.Second
	pingPeriod = 30 * time.Second
)

func handleWebSocket() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := r.URL.Query().Get("token")
		if tokenStr == "" {
			http.Error(w, "token query parameter required", http.StatusUnauthorized)
			return
		}

		var role string
		var userID int64

		// Try regular JWT first
		claims, err := parseJWT(tokenStr)
		if err == nil {
			role = claims.Role
			userID = claims.UserID
		} else {
			// Try temp link JWT
			tempClaims, tempErr := parseTempLinkJWT(tokenStr)
			if tempErr != nil {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			// Check blacklist
			linkStore.mu.RLock()
			_, revoked := linkStore.blacklist[tempClaims.LinkID]
			linkStore.mu.RUnlock()
			if revoked {
				http.Error(w, "token revoked", http.StatusUnauthorized)
				return
			}
			role = "temp"
			userID = 0
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws: upgrade error: %v", err)
			return
		}

		client := &wsClient{
			conn:   conn,
			role:   role,
			userID: userID,
			send:   make(chan []byte, 64),
		}

		hub.register(client)

		// Send connected message
		connMsg, err := json.Marshal(WSMessage{
			Type: "connected",
			Payload: map[string]any{
				"userId":      userID,
				"role":        role,
				"connectedAt": time.Now().UTC().Format(time.RFC3339),
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			log.Printf("ws: marshal connected message error: %v", err)
		} else {
			client.safeSend(connMsg)
		}

		go client.writePump()
		go client.readPump()
	}
}

func (c *wsClient) readPump() {
	defer func() {
		hub.unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("ws: read error: %v", err)
			}
			break
		}
		// Server doesn't process client messages — read loop just keeps connection alive
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
