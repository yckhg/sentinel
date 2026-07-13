package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	sqlitedrv "modernc.org/sqlite"
)

// SQLite extended result codes for UNIQUE / PRIMARY KEY constraint violations.
// (modernc.org/sqlite surfaces these via *sqlite.Error.Code().)
const (
	sqliteConstraintUnique     = 2067 // SQLITE_CONSTRAINT_UNIQUE
	sqliteConstraintPrimaryKey = 1555 // SQLITE_CONSTRAINT_PRIMARYKEY
)

// isUniqueViolation reports whether err is a SQLite UNIQUE/PK constraint failure.
// It checks both the driver's structured error code (modernc.org/sqlite errno
// 2067/1555) and the human-readable message ("UNIQUE constraint failed") so the
// classification is robust across driver versions and wrapping.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var serr *sqlitedrv.Error
	if errors.As(err, &serr) {
		switch serr.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// returnExistingIncidentByAlert looks up the incident already mapped to alertID
// and, if present, writes it as HTTP 200 and returns handled=true. A missing row
// yields handled=false with err=nil; any other query failure returns err.
func returnExistingIncidentByAlert(ctx context.Context, db *sql.DB, w http.ResponseWriter, alertID string) (handled bool, err error) {
	var existingID int64
	var existingSiteID, existingDescription, existingOccurredAt string
	qerr := db.QueryRowContext(ctx,
		"SELECT id, site_id, description, datetime(occurred_at) FROM incidents WHERE alert_id = ?",
		alertID,
	).Scan(&existingID, &existingSiteID, &existingDescription, &existingOccurredAt)
	if qerr == sql.ErrNoRows {
		return false, nil
	}
	if qerr != nil {
		return false, qerr
	}
	log.Printf("incident dedup: alertId=%s already mapped to incident id=%d", alertID, existingID)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          existingID,
		"siteId":      existingSiteID,
		"description": existingDescription,
		"occurredAt":  existingOccurredAt,
	})
	return true, nil
}

type createIncidentRequest struct {
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId,omitempty"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurredAt"`
	IsTest      bool   `json:"isTest,omitempty"`
	AlertID     string `json:"alertId,omitempty"`
}

// handleCreateIncident handles POST /api/incidents from hw-gateway (internal)
// Creates an incident record and broadcasts crisis_alert to all WebSocket clients
func handleCreateIncident(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Internal-only, fail-closed (I2): the device auto-register side effect of
		// incident creation must be gated exactly like /api/devices/seen. The sole
		// runtime caller is hw-gateway (which sends X-Internal-Token); web-frontend
		// only reads incidents.
		if !checkInternalToken(w, r) {
			return
		}
		var req createIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.SiteID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "siteId is required"})
			return
		}

		// Trim the device id so a device with incidental surrounding whitespace maps
		// to the same devices row as /api/devices/seen (which trims). Otherwise the
		// same physical device could split into two rows depending on padding.
		req.DeviceID = strings.TrimSpace(req.DeviceID)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		hasAlertID := strings.TrimSpace(req.AlertID) != ""

		// Dedup fast path: if alertId provided, return any existing incident with
		// the same alertId (the common re-post case) without inserting again.
		if hasAlertID {
			handled, derr := returnExistingIncidentByAlert(ctx, db, w, req.AlertID)
			if derr != nil {
				log.Printf("dedup check error: %v", derr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			if handled {
				return
			}
		}

		// Use provided occurredAt or default to now (handled by DB default)
		isTest := 0
		if req.IsTest {
			isTest = 1
		}
		var deviceIDArg any
		if req.DeviceID != "" {
			deviceIDArg = req.DeviceID
		} else {
			deviceIDArg = nil
		}
		var result sql.Result
		var err error
		if req.OccurredAt != "" {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, device_id, description, occurred_at, is_test, alert_id) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))",
				req.SiteID, deviceIDArg, req.Description, req.OccurredAt, isTest, req.AlertID,
			)
		} else {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, device_id, description, is_test, alert_id) VALUES (?, ?, ?, ?, NULLIF(?, ''))",
				req.SiteID, deviceIDArg, req.Description, isTest, req.AlertID,
			)
		}
		if err != nil {
			// Concurrency: two requests carrying the same alertId can both pass the
			// dedup SELECT (no row yet) and race into INSERT. The partial UNIQUE
			// index (idx_incidents_alert_id) lets exactly one win; the loser's
			// INSERT fails with SQLITE_CONSTRAINT_UNIQUE. Treat that as a dedup hit
			// (not a 5xx): the winner has committed, so re-select and return 200.
			// This closes the check-then-insert race window without a transaction.
			if hasAlertID && isUniqueViolation(err) {
				handled, selErr := returnExistingIncidentByAlert(ctx, db, w, req.AlertID)
				if selErr == nil && handled {
					return
				}
				log.Printf("unique-violation re-select failed: alertId=%s handled=%v selErr=%v", req.AlertID, handled, selErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			log.Printf("insert incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		incidentID, _ := result.LastInsertId()
		markWALDirty()

		// Device presence upsert (best-effort) + reappearance dedup in ONE transaction
		// (계약 2 "두 문장 같은 트랜잭션"). Sticky-safe: last_seen only, NEVER deleted_at —
		// a soft-deleted device stays deleted even when it emits a crisis (계약 3,
		// assertion H2). A device BORN from a crisis is created alert_state='active'
		// (crisis context; a later heartbeat reconciles it); an existing row keeps its
		// alert_state. RETURNING datetime(deleted_at) gates the shared reappear guard so
		// only a crisis that landed on a soft-deleted row runs the extra dedup UPDATE,
		// broadcasting device_reappeared once on the first crisis re-signal after
		// deletion (E2) — shared with the seen path so neither silently consumes the
		// other's alert.
		if req.SiteID != "" && req.DeviceID != "" {
			pending := upsertIncidentPresence(ctx, db, req.SiteID, req.DeviceID)
			if pending != nil {
				BroadcastDeviceReappeared(pending.siteID, pending.deviceID, pending.lastSeen)
			}
		}

		// Fetch site info for the broadcast payload
		address, managerName, managerPhone := fetchSiteInfo(ctx, db, req.SiteID)

		occurredAt := req.OccurredAt
		if occurredAt == "" {
			// Fetch the DB-generated timestamp
			db.QueryRowContext(ctx, "SELECT datetime(occurred_at) FROM incidents WHERE id = ?", incidentID).Scan(&occurredAt)
		}

		log.Printf("incident created: id=%d siteId=%s description=%s", incidentID, req.SiteID, req.Description)

		// Broadcast crisis_alert to all WebSocket clients
		BroadcastCrisisAlert(crisisAlertPayload(
			incidentID, req.SiteID, req.Description, occurredAt, req.IsTest,
			address, managerName, managerPhone,
		))

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          incidentID,
			"siteId":      req.SiteID,
			"description": req.Description,
			"occurredAt":  occurredAt,
		})
	}
}

// upsertIncidentPresence records a device's presence from a crisis (single
// autocommit upsert) and, only when the upsert landed on a soft-deleted row, runs the
// shared reappearance guard serially (계약 2 "직렬 실행" — no tx, so the hot path holds
// no extra write lock). Best-effort: any error is logged and swallowed (device
// presence must never fail incident creation). Returns a pending device_reappeared
// broadcast for the caller to emit after this returns (write already succeeded), or
// nil. alert_state is set 'active' on BOTH insert and conflict — a crisis inherently
// means the device is alarming — symmetric with the seen path's alert_state handling
// (F4); a later heartbeat reconciles it via the seen COALESCE update.
func upsertIncidentPresence(ctx context.Context, db *sql.DB, siteID, deviceID string) *reappearBroadcast {
	var deletedAt *string
	err := db.QueryRowContext(ctx, `
		INSERT INTO devices (site_id, device_id, last_seen, alert_state)
		VALUES (?, ?, datetime('now'), 'active')
		ON CONFLICT(site_id, device_id) DO UPDATE SET
			last_seen = datetime('now'),
			alert_state = 'active'
		RETURNING datetime(deleted_at)
	`, siteID, deviceID).Scan(&deletedAt)
	if err != nil {
		log.Printf("upsert device from incident error: %v", err)
		return nil
	}
	markWALDirty()

	if deletedAt == nil {
		return nil
	}
	pending, gErr := guardReappearFn(ctx, db, siteID, deviceID)
	if gErr != nil {
		log.Printf("incident reappear guard error: %v", gErr)
		return nil
	}
	return pending
}

// handleListIncidents handles GET /api/incidents — paginated incident history
func handleListIncidents(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse pagination params
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		offset := (page - 1) * limit

		// Date filters
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		statusFilter := r.URL.Query().Get("status")

		// Build query with optional date filters using parameterized builder
		conditions := []string{"1=1"}
		args := []any{}
		// Normalize both sides through datetime(): occurred_at is stored as
		// "YYYY-MM-DD HH:MM:SS" but clients may send ISO-8601 (e.g.
		// "2026-04-13T00:00:00Z"). A raw lexicographic comparison would treat
		// 'T' (0x54) > ' ' (0x20) and silently mis-filter at the boundary.
		// datetime() coerces both formats to a common canonical form.
		if from != "" {
			conditions = append(conditions, "datetime(occurred_at) >= datetime(?)")
			args = append(args, from)
		}
		if to != "" {
			conditions = append(conditions, "datetime(occurred_at) <= datetime(?)")
			args = append(args, to)
		}
		if statusFilter != "" {
			// Status filter whitelist: only {open, resolved} are valid (case-sensitive).
			// Any other value (incl. the removed 'acknowledged') → 400.
			if statusFilter != "open" && statusFilter != "resolved" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid status filter"})
				return
			}
			conditions = append(conditions, "status = ?")
			args = append(args, statusFilter)
		}

		whereClause := "WHERE " + strings.Join(conditions, " AND ")

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Count total
		var total int
		countQuery := "SELECT COUNT(*) FROM incidents " + whereClause
		if err := db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
			log.Printf("count incidents error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Fetch page
		dataQuery := "SELECT id, site_id, description, datetime(occurred_at), confirmed_at, confirmed_by, is_test, status, resolved_at, resolved_by, resolution_notes, resolved_by_kind, resolved_by_id, resolved_by_label FROM incidents " +
			whereClause + " ORDER BY datetime(occurred_at) DESC LIMIT ? OFFSET ?"
		dataArgs := append(args, limit, offset)
		rows, err := db.QueryContext(ctx, dataQuery, dataArgs...)
		if err != nil {
			log.Printf("list incidents error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		type incidentRow struct {
			ID              int64   `json:"id"`
			SiteID          string  `json:"siteId"`
			Description     string  `json:"description"`
			OccurredAt      string  `json:"occurredAt"`
			ConfirmedAt     *string `json:"confirmedAt"`
			ConfirmedBy     *string `json:"confirmedBy"`
			IsTest          bool    `json:"isTest"`
			Status          string  `json:"status"`
			ResolvedAt      *string `json:"resolvedAt"`
			ResolvedBy      *string `json:"resolvedBy"`
			ResolutionNotes *string `json:"resolutionNotes"`
			ResolvedByKind  *string `json:"resolvedByKind"`
			ResolvedByID    *string `json:"resolvedById"`
			ResolvedByLabel *string `json:"resolvedByLabel"`
		}

		incidents := []incidentRow{}
		for rows.Next() {
			var inc incidentRow
			var isTest int
			if err := rows.Scan(&inc.ID, &inc.SiteID, &inc.Description, &inc.OccurredAt, &inc.ConfirmedAt, &inc.ConfirmedBy, &isTest, &inc.Status, &inc.ResolvedAt, &inc.ResolvedBy, &inc.ResolutionNotes, &inc.ResolvedByKind, &inc.ResolvedByID, &inc.ResolvedByLabel); err != nil {
				log.Printf("scan incident error: %v", err)
				continue
			}
			inc.IsTest = isTest == 1
			incidents = append(incidents, inc)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"data": incidents,
			"pagination": map[string]any{
				"page":  page,
				"limit": limit,
				"total": total,
			},
		})
	}
}

// fetchSiteInfo returns the site contact fields joined for an incident payload.
// The sites table holds a single deployment site (no site_id join key), so the
// same LIMIT 1 lookup the crisis_alert broadcast has always used is reused here
// verbatim to keep the /active payload isomorphic with the live push.
func fetchSiteInfo(ctx context.Context, db *sql.DB, siteID string) (address, managerName, managerPhone string) {
	row := db.QueryRowContext(ctx, "SELECT address, manager_name, manager_phone FROM sites LIMIT 1")
	if err := row.Scan(&address, &managerName, &managerPhone); err != nil {
		// Site info may not exist yet — use empty values.
		log.Printf("site info not found: %v", err)
	}
	return address, managerName, managerPhone
}

// crisisAlertPayload builds the crisis_alert WS payload (contract 14). The
// /api/incidents/active backfill endpoint (contract 2) reuses this exact shape
// so a reconstructed banner is isomorphic with the live push (no half-banner).
func crisisAlertPayload(incidentID int64, siteID, description, occurredAt string, isTest bool, address, managerName, managerPhone string) map[string]any {
	return map[string]any{
		"incidentId":  incidentID,
		"siteId":      siteID,
		"description": description,
		"occurredAt":  occurredAt,
		"isTest":      isTest,
		"site": map[string]string{
			"address":      address,
			"managerName":  managerName,
			"managerPhone": managerPhone,
		},
	}
}

// activeIncidentsLimit caps the unresolved-banner backfill. Like GET /api/incidents
// (which clamps to 100), the banner only needs the most-recent unresolved
// incidents; without a cap a polluted table (tens of thousands of stale open rows)
// would be streamed in full. The most-recent-N (occurred_at DESC) is sufficient to
// reconstruct the in-progress banner.
const activeIncidentsLimit = 200

// handleActiveIncidents handles GET /api/incidents/active — the unresolved-banner
// backfill (contract 2). Unresolved == status 'open' only (the intermediate
// 'acknowledged' state no longer exists; resolved excluded), occurred_at DESC,
// capped at activeIncidentsLimit. Each
// element is isomorphic to the crisis_alert payload plus a status field. The
// identifier is incidentId (not the list `id`).
func handleActiveIncidents(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			`SELECT id, site_id, description, datetime(occurred_at), is_test, status
			 FROM incidents
			 WHERE status = 'open'
			 ORDER BY datetime(occurred_at) DESC
			 LIMIT ?`, activeIncidentsLimit)
		if err != nil {
			log.Printf("list active incidents error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		out := []map[string]any{}
		for rows.Next() {
			var id int64
			var siteID, description, occurredAt, status string
			var isTest int
			if err := rows.Scan(&id, &siteID, &description, &occurredAt, &isTest, &status); err != nil {
				log.Printf("scan active incident error: %v", err)
				continue
			}
			address, managerName, managerPhone := fetchSiteInfo(ctx, db, siteID)
			payload := crisisAlertPayload(id, siteID, description, occurredAt, isTest == 1, address, managerName, managerPhone)
			payload["status"] = status
			out = append(out, payload)
		}

		writeJSON(w, http.StatusOK, out)
	}
}

// handleResolveIncident handles PATCH /api/incidents/{id}/resolve
func handleResolveIncident(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
			return
		}

		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid incident id"})
			return
		}

		var req struct {
			ResolutionNotes string `json:"resolutionNotes"`
		}
		// Resolution notes are optional: an absent/empty body (io.EOF) is allowed,
		// as are empty/whitespace notes → stored empty (never a 400). Only a
		// malformed (non-empty, invalid) JSON body is rejected.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Check incident exists and fetch details for archive finalization
		var status, siteID, occurredAt string
		err = db.QueryRowContext(ctx, "SELECT status, site_id, datetime(occurred_at) FROM incidents WHERE id = ?", id).Scan(&status, &siteID, &occurredAt)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "incident not found"})
			return
		}
		if err != nil {
			log.Printf("query incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Get username + display name (for resolvedBy attribution)
		var username string
		var displayName sql.NullString
		err = db.QueryRowContext(ctx, "SELECT username, name FROM users WHERE id = ?", user.UserID).Scan(&username, &displayName)
		if err != nil {
			log.Printf("query username error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		label := username
		if displayName.Valid && strings.TrimSpace(displayName.String) != "" {
			label = displayName.String
		}

		result, err := db.ExecContext(ctx,
			"UPDATE incidents SET status = 'resolved', resolved_at = datetime('now'), resolved_by = ?, resolution_notes = ?, resolved_by_kind = 'web', resolved_by_id = ?, resolved_by_label = ? WHERE id = ? AND status = 'open'",
			username, strings.TrimSpace(req.ResolutionNotes), username, label, id,
		)
		if err != nil {
			log.Printf("resolve incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "incident is already resolved"})
			return
		}
		markWALDirty()

		// Fetch resolved_at timestamp for archive finalization
		var resolvedAt string
		db.QueryRowContext(ctx, "SELECT datetime(resolved_at) FROM incidents WHERE id = ?", id).Scan(&resolvedAt)

		// Fetch deviceId for originalAlert (best-effort)
		var deviceID sql.NullString
		db.QueryRowContext(ctx, "SELECT device_id FROM incidents WHERE id = ?", id).Scan(&deviceID)

		// Trigger archive finalization asynchronously (Phase 2 of two-phase archiving)
		go requestArchiveFinalize(siteID, occurredAt, resolvedAt)

		// Best-effort: tell hw-gateway to publish MQTT alert/resolved (web → sensor sync)
		go publishAlertResolvedToHWGateway(id, siteID, username, label, deviceID.String, resolvedAt)

		// Broadcast incident_resolved over WebSocket
		BroadcastIncidentResolved(map[string]any{
			"incidentId":      id,
			"siteId":          siteID,
			"resolvedAt":      resolvedAt,
			"resolvedByKind":  "web",
			"resolvedById":    username,
			"resolvedByLabel": label,
		})

		log.Printf("incident %d resolved by %s (web): %s", id, username, req.ResolutionNotes)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "resolved",
			"resolvedByKind":  "web",
			"resolvedById":    username,
			"resolvedByLabel": label,
		})
	}
}

// publishAlertResolvedToHWGateway calls hw-gateway POST /api/alert/resolved (best-effort, fire-and-forget).
func publishAlertResolvedToHWGateway(incidentID int64, siteID, userID, userLabel, deviceID, resolvedAt string) {
	if hwGatewayURL == "" || siteID == "" {
		return
	}
	resolvedAtIso := resolvedAt
	if t, err := time.Parse("2006-01-02 15:04:05", resolvedAt); err == nil {
		resolvedAtIso = t.UTC().Format(time.RFC3339)
	}
	payload := map[string]any{
		"incidentId": incidentID,
		"siteId":     siteID,
		"resolvedAt": resolvedAtIso,
		"resolvedBy": map[string]string{
			"kind":  "web",
			"id":    userID,
			"label": userLabel,
		},
	}
	if deviceID != "" {
		payload["originalAlert"] = map[string]string{"deviceId": deviceID}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[ALERT-RESOLVED] marshal error: %v", err)
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(hwGatewayURL+"/api/alert/resolved", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("[ALERT-RESOLVED] hw-gateway call failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[ALERT-RESOLVED] hw-gateway returned %d", resp.StatusCode)
	} else {
		log.Printf("[ALERT-RESOLVED] hw-gateway publish requested: incident=%d site=%s", incidentID, siteID)
	}
}

// resolvedBy struct (matches MQTT contract 5.5)
type resolvedByPayload struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type resolveFromSensorRequest struct {
	IncidentID    int64             `json:"incidentId"`
	SiteID        string            `json:"siteId"`
	ResolvedAt    string            `json:"resolvedAt"`
	ResolvedBy    resolvedByPayload `json:"resolvedBy"`
	OriginalAlert map[string]any    `json:"originalAlert,omitempty"`
}

// handleResolveIncidentFromSensor handles POST /api/incidents/{id}/resolve-from-sensor (internal — from hw-gateway).
// id path param may be 0 → fall back to body incidentId; if still 0 → most-recent unresolved on siteId.
func handleResolveIncidentFromSensor(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		pathID, _ := strconv.ParseInt(idStr, 10, 64)

		var req resolveFromSensorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		incidentID := pathID
		if incidentID == 0 {
			incidentID = req.IncidentID
		}

		if req.SiteID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "siteId is required"})
			return
		}
		if req.ResolvedBy.Kind == "" {
			req.ResolvedBy.Kind = "sensor_button"
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// If incidentID == 0: find most recent unresolved incident for siteId
		if incidentID == 0 {
			err := db.QueryRowContext(ctx,
				"SELECT id FROM incidents WHERE site_id = ? AND status = 'open' ORDER BY datetime(occurred_at) DESC LIMIT 1",
				req.SiteID,
			).Scan(&incidentID)
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no unresolved incident found for site"})
				return
			}
			if err != nil {
				log.Printf("query latest unresolved incident error: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		}

		// Verify exists and not already resolved
		var status, siteID, occurredAt string
		err := db.QueryRowContext(ctx,
			"SELECT status, site_id, datetime(occurred_at) FROM incidents WHERE id = ?",
			incidentID,
		).Scan(&status, &siteID, &occurredAt)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "incident not found"})
			return
		}
		if err != nil {
			log.Printf("query incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if status == "resolved" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "incident is already resolved"})
			return
		}

		notes := fmt.Sprintf("[센서 해제] %s", req.ResolvedBy.Label)
		result, err := db.ExecContext(ctx,
			"UPDATE incidents SET status = 'resolved', resolved_at = datetime('now'), resolved_by = ?, resolution_notes = ?, resolved_by_kind = ?, resolved_by_id = ?, resolved_by_label = ? WHERE id = ? AND status = 'open'",
			req.ResolvedBy.ID, notes, req.ResolvedBy.Kind, req.ResolvedBy.ID, req.ResolvedBy.Label, incidentID,
		)
		if err != nil {
			log.Printf("resolve-from-sensor error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "incident is already resolved"})
			return
		}
		markWALDirty()

		var resolvedAt string
		db.QueryRowContext(ctx, "SELECT datetime(resolved_at) FROM incidents WHERE id = ?", incidentID).Scan(&resolvedAt)

		// Archive finalize (same as web path)
		go requestArchiveFinalize(siteID, occurredAt, resolvedAt)

		// Broadcast incident_resolved
		BroadcastIncidentResolved(map[string]any{
			"incidentId":      incidentID,
			"siteId":          siteID,
			"resolvedAt":      resolvedAt,
			"resolvedByKind":  req.ResolvedBy.Kind,
			"resolvedById":    req.ResolvedBy.ID,
			"resolvedByLabel": req.ResolvedBy.Label,
		})

		log.Printf("incident %d resolved by sensor: kind=%s id=%s label=%s", incidentID, req.ResolvedBy.Kind, req.ResolvedBy.ID, req.ResolvedBy.Label)
		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "resolved",
			"incidentId":      incidentID,
			"resolvedByKind":  req.ResolvedBy.Kind,
			"resolvedById":    req.ResolvedBy.ID,
			"resolvedByLabel": req.ResolvedBy.Label,
		})
	}
}

// requestArchiveFinalize calls the recording service to finalize archives for a resolved incident.
func requestArchiveFinalize(siteID, occurredAt, resolvedAt string) {
	if recordingURL == "" {
		return
	}

	// Reconstruct the archive incidentID (same format as notifier)
	incidentTime, err := time.Parse("2006-01-02 15:04:05", occurredAt)
	if err != nil {
		incidentTime, err = time.Parse(time.RFC3339, occurredAt)
		if err != nil {
			log.Printf("[archive-finalize] Cannot parse occurredAt %q: %v", occurredAt, err)
			return
		}
	}
	incidentID := fmt.Sprintf("incident_%s_%s", siteID, incidentTime.UTC().Format("20060102_150405"))

	// Parse resolvedAt for the finalize request
	resolvedTime, err := time.Parse("2006-01-02 15:04:05", resolvedAt)
	if err != nil {
		resolvedTime, err = time.Parse(time.RFC3339, resolvedAt)
		if err != nil {
			log.Printf("[archive-finalize] Cannot parse resolvedAt %q: %v", resolvedAt, err)
			return
		}
	}

	payload, err := json.Marshal(map[string]string{
		"incidentId": incidentID,
		"resolvedAt": resolvedTime.UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("[archive-finalize] Failed to marshal request: %v", err)
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(recordingURL+"/api/archives/finalize", "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[archive-finalize] Failed to call recording service: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[archive-finalize] Finalize request accepted for incident %s", incidentID)
	} else {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[archive-finalize] Finalize request failed: status %d, body: %s", resp.StatusCode, string(body))
	}
}
