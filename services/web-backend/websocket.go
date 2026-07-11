package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// healthMon is the process-wide health monitor, set in main. Used by the WS
// handler to replay the current unhealthy snapshot to a newly connected admin
// (assertion O4).
var healthMon *HealthMonitor

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

	// Fields for periodic token re-validation (issue #82). The original token is
	// re-parsed on each ping tick to detect expiry; temp tokens are additionally
	// checked against the revocation blacklist, and regular tokens against the
	// owner's password-change boundary.
	token  string  // original ?token= value
	isTemp bool    // true for temp-link tokens
	db     *sql.DB // for the password-change boundary lookup
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

func handleWebSocket(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := r.URL.Query().Get("token")
		if tokenStr == "" {
			http.Error(w, "token query parameter required", http.StatusUnauthorized)
			return
		}

		var role string
		var userID int64
		var isTemp bool

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
			isTemp = true
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
			// Reject at connect time if the token predates the owner's
			// password-change boundary (issue #83 / #82).
			bctx, bcancel := dbCtx(r.Context())
			invalidated := tokenInvalidatedByPasswordChange(bctx, db, claims.UserID, claims.IssuedAt.Time)
			bcancel()
			if invalidated {
				http.Error(w, "token invalidated by password change", http.StatusUnauthorized)
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
			token:  tokenStr,
			isTemp: isTemp,
			db:     db,
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

		// O4: right after `connected`, replay the current unhealthy snapshot to a
		// newly connected admin so an admin that was offline at transition time
		// still observes in-progress unhealthy targets. Enqueued before the pumps
		// start; the send buffer holds them until writePump drains.
		sendUnhealthySnapshot(client)

		go client.writePump()
		go client.readPump()
	}
}

// sendUnhealthySnapshot replays every currently-unhealthy monitored target to a
// single admin client as a health-sourced system_alarm (assertion O4). Non-admin
// (user/temp) clients receive nothing.
func sendUnhealthySnapshot(c *wsClient) {
	if c.role != "admin" || healthMon == nil {
		return
	}
	for _, e := range healthMon.snapshot() {
		if e.Status != StatusUnhealthy {
			continue
		}
		data, err := json.Marshal(WSMessage{
			Type:      "system_alarm",
			Payload:   healthAlarmPayload(e.Kind, e.ID, e.Status),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			continue
		}
		c.safeSend(data)
	}
}

// healthAlarmPayload builds the fixed health-sourced system_alarm payload
// (interface-web-api.md §계약14): envelope {type,message,details} with the
// details sub-schema {entityKind, entityId, status}.
func healthAlarmPayload(entityKind, entityID, status string) map[string]any {
	return map[string]any{
		"type":    "system_alarm",
		"message": fmt.Sprintf("%s %s is %s", entityKind, entityID, status),
		"details": map[string]any{
			"entityKind": entityKind,
			"entityId":   entityID,
			"status":     status,
		},
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
			// Ride the ping cycle (30s ≤ 60s contract bound) to re-validate the
			// connection's token (issue #82). If the token is now invalid —
			// temp-link revoked, expired, or past the password-change boundary —
			// actively close the socket so no further crisis_alert is delivered.
			if revalidateWSToken(c) != nil {
				c.conn.SetWriteDeadline(time.Now().Add(writeWait))
				c.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "token no longer valid"))
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// revalidateWSToken re-checks a live connection's token. It returns nil while the
// token remains valid, or an error describing why the connection must be closed:
//   - temp token: expired (parse fails) or its link was revoked (blacklist)
//   - regular token: expired (parse fails) or issued before the owner's
//     password-change boundary (assertion Q2 / issue #83)
func revalidateWSToken(c *wsClient) error {
	if c.isTemp {
		claims, err := parseTempLinkJWT(c.token)
		if err != nil {
			return fmt.Errorf("temp token expired: %w", err)
		}
		linkStore.mu.RLock()
		_, revoked := linkStore.blacklist[claims.LinkID]
		linkStore.mu.RUnlock()
		if revoked {
			return fmt.Errorf("temp link revoked")
		}
		return nil
	}

	claims, err := parseJWT(c.token)
	if err != nil {
		return fmt.Errorf("token expired: %w", err)
	}
	if c.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
		invalidated := tokenInvalidatedByPasswordChange(ctx, c.db, claims.UserID, claims.IssuedAt.Time)
		cancel()
		if invalidated {
			return fmt.Errorf("token invalidated by password change")
		}
	}
	return nil
}
