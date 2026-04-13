package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type createIncidentRequest struct {
	SiteID      string `json:"siteId"`
	DeviceID    string `json:"deviceId,omitempty"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurredAt"`
	IsTest      bool   `json:"isTest,omitempty"`
}

// handleCreateIncident handles POST /api/incidents from hw-gateway (internal)
// Creates an incident record and broadcasts crisis_alert to all WebSocket clients
func handleCreateIncident(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.SiteID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "siteId is required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Use provided occurredAt or default to now (handled by DB default)
		isTest := 0
		if req.IsTest {
			isTest = 1
		}
		var deviceIDArg any
		if strings.TrimSpace(req.DeviceID) != "" {
			deviceIDArg = req.DeviceID
		} else {
			deviceIDArg = nil
		}
		var result sql.Result
		var err error
		if req.OccurredAt != "" {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, device_id, description, occurred_at, is_test) VALUES (?, ?, ?, ?, ?)",
				req.SiteID, deviceIDArg, req.Description, req.OccurredAt, isTest,
			)
		} else {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, device_id, description, is_test) VALUES (?, ?, ?, ?)",
				req.SiteID, deviceIDArg, req.Description, isTest,
			)
		}
		if err != nil {
			log.Printf("insert incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		incidentID, _ := result.LastInsertId()

		// Ensure device is registered/restored if deviceId provided (best-effort)
		if req.SiteID != "" && strings.TrimSpace(req.DeviceID) != "" {
			if _, upErr := db.ExecContext(ctx, `
				INSERT INTO devices (site_id, device_id, last_seen)
				VALUES (?, ?, datetime('now'))
				ON CONFLICT(site_id, device_id) DO UPDATE SET
					last_seen = datetime('now'),
					deleted_at = NULL
			`, req.SiteID, req.DeviceID); upErr != nil {
				log.Printf("upsert device from incident error: %v", upErr)
			}
		}

		// Fetch site info for the broadcast payload
		var address, managerName, managerPhone string
		row := db.QueryRowContext(ctx, "SELECT address, manager_name, manager_phone FROM sites LIMIT 1")
		if err := row.Scan(&address, &managerName, &managerPhone); err != nil {
			// Site info may not exist yet — use empty values
			log.Printf("site info not found for broadcast: %v", err)
		}

		occurredAt := req.OccurredAt
		if occurredAt == "" {
			// Fetch the DB-generated timestamp
			db.QueryRowContext(ctx, "SELECT datetime(occurred_at) FROM incidents WHERE id = ?", incidentID).Scan(&occurredAt)
		}

		log.Printf("incident created: id=%d siteId=%s description=%s", incidentID, req.SiteID, req.Description)

		// Broadcast crisis_alert to all WebSocket clients
		BroadcastCrisisAlert(map[string]any{
			"incidentId":  incidentID,
			"siteId":      req.SiteID,
			"description": req.Description,
			"occurredAt":  occurredAt,
			"isTest":      req.IsTest,
			"site": map[string]string{
				"address":      address,
				"managerName":  managerName,
				"managerPhone": managerPhone,
			},
		})

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          incidentID,
			"siteId":      req.SiteID,
			"description": req.Description,
			"occurredAt":  occurredAt,
		})
	}
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
		if from != "" {
			conditions = append(conditions, "occurred_at >= ?")
			args = append(args, from)
		}
		if to != "" {
			conditions = append(conditions, "occurred_at <= ?")
			args = append(args, to)
		}
		if statusFilter != "" {
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

// handleAcknowledgeIncident handles PATCH /api/incidents/{id}/acknowledge
func handleAcknowledgeIncident(db *sql.DB) http.HandlerFunc {
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

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Check current status
		var status string
		err = db.QueryRowContext(ctx, "SELECT status FROM incidents WHERE id = ?", id).Scan(&status)
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
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot acknowledge a resolved incident"})
			return
		}

		// Get username
		var username string
		err = db.QueryRowContext(ctx, "SELECT username FROM users WHERE id = ?", user.UserID).Scan(&username)
		if err != nil {
			log.Printf("query username error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		_, err = db.ExecContext(ctx,
			"UPDATE incidents SET status = 'acknowledged', confirmed_at = datetime('now'), confirmed_by = ? WHERE id = ?",
			username, id,
		)
		if err != nil {
			log.Printf("acknowledge incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("incident %d acknowledged by %s", id, username)
		writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if strings.TrimSpace(req.ResolutionNotes) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resolution notes are required"})
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
			"UPDATE incidents SET status = 'resolved', resolved_at = datetime('now'), resolved_by = ?, resolution_notes = ?, resolved_by_kind = 'web', resolved_by_id = ?, resolved_by_label = ? WHERE id = ? AND status != 'resolved'",
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
				"SELECT id FROM incidents WHERE site_id = ? AND status != 'resolved' ORDER BY datetime(occurred_at) DESC LIMIT 1",
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
			"UPDATE incidents SET status = 'resolved', resolved_at = datetime('now'), resolved_by = ?, resolution_notes = ?, resolved_by_kind = ?, resolved_by_id = ?, resolved_by_label = ? WHERE id = ? AND status != 'resolved'",
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

