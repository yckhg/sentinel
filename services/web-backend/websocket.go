package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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
	userID int64  // 0 for temp link users
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

// allowedWSOrigins holds Origin values (scheme+host) explicitly permitted for
// WebSocket upgrades beyond same-origin. Populated from ALLOWED_WS_ORIGINS
// (comma-separated) via initWSOrigins.
var allowedWSOrigins []string

// initWSOrigins parses the ALLOWED_WS_ORIGINS env var into the allow list.
func initWSOrigins() {
	allowedWSOrigins = nil
	raw := strings.TrimSpace(os.Getenv("ALLOWED_WS_ORIGINS"))
	if raw == "" {
		return
	}
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			allowedWSOrigins = append(allowedWSOrigins, strings.TrimRight(p, "/"))
		}
	}
	if len(allowedWSOrigins) > 0 {
		log.Printf("ws: %d additional allowed origin(s) configured", len(allowedWSOrigins))
	}
}

// checkWSOrigin blunts cross-site WebSocket hijacking. It permits: requests with
// no Origin header (non-browser clients — native apps, internal tooling), same
// -origin requests (Origin host matches the Host the request targeted), and any
// Origin explicitly whitelisted via ALLOWED_WS_ORIGINS. Everything else is denied.
func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, allowed := range allowedWSOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	log.Printf("ws: rejected origin %q (host=%q)", origin, r.Host)
	return false
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkWSOrigin,
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

// BroadcastIncidentResolved sends incident_resolved to all connected clients.
func BroadcastIncidentResolved(payload any) {
	hub.broadcast(WSMessage{
		Type:      "incident_resolved",
		Payload:   payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil)
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

		// Identify temp-link tokens FIRST (non-empty linkId claim). Both token
		// kinds share the signing secret, so trying parseJWT first would accept a
		// temp token as a role-less user and skip the revocation check — letting a
		// revoked temp token keep a live WS connection.
		if tempClaims, err := parseTempLinkJWT(tokenStr); err == nil && tempClaims.LinkID != "" {
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
		} else {
			// Regular user/admin JWT.
			claims, err := parseJWT(tokenStr)
			if err != nil {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			if claims.UserID == 0 || (claims.Role != "admin" && claims.Role != "user") {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			role = claims.Role
			userID = claims.UserID
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
