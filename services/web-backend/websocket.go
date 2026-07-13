package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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

// BroadcastDeviceReappeared notifies connected admins that a soft-deleted device
// signaled again (계약 2/3). Admin-filtered (operator-facing management alert). The
// device is NOT restored — the operator decides whether to reactivate. lastSeen is
// nullable (a device explicitly registered then deleted has no heartbeat yet).
//
// It is a package var (not a plain func) so the once-only gate (F1) can substitute a
// counting sink and assert the delete→reappear cycle broadcasts EXACTLY once,
// independent of clock resolution. Production always points at the real broadcast.
var BroadcastDeviceReappeared = func(siteID, deviceID string, lastSeen *string) {
	hub.broadcast(WSMessage{
		Type: "device_reappeared",
		Payload: map[string]any{
			"siteId":   siteID,
			"deviceId": deviceID,
			"lastSeen": lastSeen,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, func(c *wsClient) bool {
		return c.role == "admin"
	})
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

		// Reappearance backfill (계약 2 유실 보정): replay device_reappeared for every
		// device currently soft-deleted AND already alerted at least once, so an admin
		// offline at reappear time still learns of it.
		sendReappearedSnapshot(client, db)

		go client.writePump()
		go client.readPump()
	}
}

// sendUnhealthySnapshot replays currently-unhealthy monitored targets to a single
// admin client as health-sourced system_alarm frames (assertion O4). Non-admin
// (user/temp) clients receive nothing.
//
// The replay is ordered newest-transition-first, deduped (one frame per entity),
// and bounded (maxReplaySnapshotFrames). This keeps a just-made-unhealthy target
// inside the bound even when the live unhealthy set is huge/polluted (thousands of
// stale sensors), and keeps the whole burst within the WS send buffer so nothing
// is silently dropped — which also bounds the admin-connect flood.
func sendUnhealthySnapshot(c *wsClient) {
	if c.role != "admin" || healthMon == nil {
		return
	}
	for _, e := range healthMon.unhealthySnapshotForReplay(maxReplaySnapshotFrames) {
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

// sendReappearedSnapshot replays device_reappeared frames to a single newly
// connected admin for every device that is currently soft-deleted AND has already
// been alerted at least once (deleted_at IS NOT NULL AND reappear_alerted_at IS NOT
// NULL). This is the reappearance analogue of sendUnhealthySnapshot (계약 2 backfill)
// — an independent function, not a reuse of the health snapshot. Non-admin clients
// receive nothing. A device that keeps signaling while the operator declines to
// reactivate is re-notified on every connect: an intended reminder that a deleted
// device is still alive.
func sendReappearedSnapshot(c *wsClient, db *sql.DB) {
	if c.role != "admin" || db == nil {
		return
	}
	ctx, cancel := dbCtx(context.Background())
	defer cancel()
	rows, err := db.QueryContext(ctx, `
		SELECT site_id, device_id, datetime(last_seen)
		FROM devices
		WHERE deleted_at IS NOT NULL AND reappear_alerted_at IS NOT NULL
		ORDER BY id ASC
	`)
	if err != nil {
		log.Printf("ws: reappeared snapshot query error: %v", err)
		return
	}
	type row struct {
		siteID, deviceID string
		lastSeen         *string
	}
	var out []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(&rr.siteID, &rr.deviceID, &rr.lastSeen); err != nil {
			log.Printf("ws: reappeared snapshot scan error: %v", err)
			continue
		}
		out = append(out, rr)
	}
	rows.Close()

	for _, rr := range out {
		data, err := json.Marshal(WSMessage{
			Type: "device_reappeared",
			Payload: map[string]any{
				"siteId":   rr.siteID,
				"deviceId": rr.deviceID,
				"lastSeen": rr.lastSeen,
			},
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
