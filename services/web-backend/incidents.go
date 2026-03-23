package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type createIncidentRequest struct {
	SiteID      string `json:"siteId"`
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
		var result sql.Result
		var err error
		if req.OccurredAt != "" {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, description, occurred_at, is_test) VALUES (?, ?, ?, ?)",
				req.SiteID, req.Description, req.OccurredAt, isTest,
			)
		} else {
			result, err = db.ExecContext(ctx,
				"INSERT INTO incidents (site_id, description, is_test) VALUES (?, ?, ?)",
				req.SiteID, req.Description, isTest,
			)
		}
		if err != nil {
			log.Printf("insert incident error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		incidentID, _ := result.LastInsertId()

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
		dataQuery := "SELECT id, site_id, description, datetime(occurred_at), confirmed_at, confirmed_by, is_test, status, resolved_at, resolved_by, resolution_notes FROM incidents " +
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
		}

		incidents := []incidentRow{}
		for rows.Next() {
			var inc incidentRow
			var isTest int
			if err := rows.Scan(&inc.ID, &inc.SiteID, &inc.Description, &inc.OccurredAt, &inc.ConfirmedAt, &inc.ConfirmedBy, &isTest, &inc.Status, &inc.ResolvedAt, &inc.ResolvedBy, &inc.ResolutionNotes); err != nil {
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

		// Check incident exists
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

		// Get username
		var username string
		err = db.QueryRowContext(ctx, "SELECT username FROM users WHERE id = ?", user.UserID).Scan(&username)
		if err != nil {
			log.Printf("query username error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		result, err := db.ExecContext(ctx,
			"UPDATE incidents SET status = 'resolved', resolved_at = datetime('now'), resolved_by = ?, resolution_notes = ? WHERE id = ? AND status != 'resolved'",
			username, strings.TrimSpace(req.ResolutionNotes), id,
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

		log.Printf("incident %d resolved by %s: %s", id, username, req.ResolutionNotes)
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
	}
}

