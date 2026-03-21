package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
)

type createIncidentRequest struct {
	SiteID      string `json:"siteId"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurredAt"`
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

		// Use provided occurredAt or default to now (handled by DB default)
		var result sql.Result
		var err error
		if req.OccurredAt != "" {
			result, err = db.Exec(
				"INSERT INTO incidents (site_id, description, occurred_at) VALUES (?, ?, ?)",
				req.SiteID, req.Description, req.OccurredAt,
			)
		} else {
			result, err = db.Exec(
				"INSERT INTO incidents (site_id, description) VALUES (?, ?)",
				req.SiteID, req.Description,
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
		row := db.QueryRow("SELECT address, manager_name, manager_phone FROM sites LIMIT 1")
		if err := row.Scan(&address, &managerName, &managerPhone); err != nil {
			// Site info may not exist yet — use empty values
			log.Printf("site info not found for broadcast: %v", err)
		}

		occurredAt := req.OccurredAt
		if occurredAt == "" {
			// Fetch the DB-generated timestamp
			db.QueryRow("SELECT datetime(occurred_at) FROM incidents WHERE id = ?", incidentID).Scan(&occurredAt)
		}

		log.Printf("incident created: id=%d siteId=%s description=%s", incidentID, req.SiteID, req.Description)

		// Broadcast crisis_alert to all WebSocket clients
		BroadcastCrisisAlert(map[string]any{
			"incidentId":  incidentID,
			"siteId":      req.SiteID,
			"description": req.Description,
			"occurredAt":  occurredAt,
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

		// Build query with optional date filters
		where := ""
		args := []any{}
		if from != "" {
			where += " AND occurred_at >= ?"
			args = append(args, from)
		}
		if to != "" {
			where += " AND occurred_at <= ?"
			args = append(args, to)
		}

		// Count total
		var total int
		countQuery := "SELECT COUNT(*) FROM incidents WHERE 1=1" + where
		if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
			log.Printf("count incidents error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Fetch page
		dataQuery := fmt.Sprintf(
			"SELECT id, site_id, description, datetime(occurred_at), confirmed_at, confirmed_by FROM incidents WHERE 1=1%s ORDER BY datetime(occurred_at) DESC LIMIT ? OFFSET ?",
			where,
		)
		dataArgs := append(args, limit, offset)
		rows, err := db.Query(dataQuery, dataArgs...)
		if err != nil {
			log.Printf("list incidents error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		type incidentRow struct {
			ID          int64   `json:"id"`
			SiteID      string  `json:"siteId"`
			Description string  `json:"description"`
			OccurredAt  string  `json:"occurredAt"`
			ConfirmedAt *string `json:"confirmedAt"`
			ConfirmedBy *string `json:"confirmedBy"`
		}

		incidents := []incidentRow{}
		for rows.Next() {
			var inc incidentRow
			if err := rows.Scan(&inc.ID, &inc.SiteID, &inc.Description, &inc.OccurredAt, &inc.ConfirmedAt, &inc.ConfirmedBy); err != nil {
				log.Printf("scan incident error: %v", err)
				continue
			}
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
