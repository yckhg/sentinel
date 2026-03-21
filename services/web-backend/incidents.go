package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
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
