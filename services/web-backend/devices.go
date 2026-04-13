package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type deviceResponse struct {
	ID        int64   `json:"id"`
	SiteID    string  `json:"siteId"`
	DeviceID  string  `json:"deviceId"`
	Alias     string  `json:"alias"`
	FirstSeen string  `json:"firstSeen"`
	LastSeen  string  `json:"lastSeen"`
	DeletedAt *string `json:"deletedAt"`
}

// handleListDevices handles GET /api/devices — returns non-deleted devices.
func handleListDevices(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		includeDeleted := false
		if r.URL.Path == "/api/devices/all" {
			user := getAuthUser(r)
			if user.Role != "admin" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
				return
			}
			includeDeleted = true
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		query := `SELECT id, site_id, device_id, alias, datetime(first_seen), datetime(last_seen), datetime(deleted_at) FROM devices`
		if !includeDeleted {
			query += ` WHERE deleted_at IS NULL`
		}
		query += ` ORDER BY id ASC`

		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			log.Printf("query devices error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		devices := []deviceResponse{}
		for rows.Next() {
			var d deviceResponse
			if err := rows.Scan(&d.ID, &d.SiteID, &d.DeviceID, &d.Alias, &d.FirstSeen, &d.LastSeen, &d.DeletedAt); err != nil {
				log.Printf("scan device error: %v", err)
				continue
			}
			devices = append(devices, d)
		}

		writeJSON(w, http.StatusOK, devices)
	}
}

// handleSeenDevice handles POST /api/devices/seen from hw-gateway (internal, no auth).
// Body: {"siteId": "...", "deviceId": "..."}
func handleSeenDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SiteID   string `json:"siteId"`
			DeviceID string `json:"deviceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req.SiteID = strings.TrimSpace(req.SiteID)
		req.DeviceID = strings.TrimSpace(req.DeviceID)
		if req.SiteID == "" || req.DeviceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "siteId and deviceId are required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		_, err := db.ExecContext(ctx, `
			INSERT INTO devices (site_id, device_id, last_seen)
			VALUES (?, ?, datetime('now'))
			ON CONFLICT(site_id, device_id) DO UPDATE SET
				last_seen = datetime('now'),
				deleted_at = NULL
		`, req.SiteID, req.DeviceID)
		if err != nil {
			log.Printf("upsert device error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// handleUpdateDeviceAlias handles PATCH /api/devices/{id} — update alias.
func handleUpdateDeviceAlias(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
			return
		}

		var req struct {
			Alias string `json:"alias"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx, "UPDATE devices SET alias = ? WHERE id = ?", strings.TrimSpace(req.Alias), id)
		if err != nil {
			log.Printf("update device alias error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"id": id, "alias": strings.TrimSpace(req.Alias)})
	}
}

// handleDeleteDevice handles DELETE /api/devices/{id} — soft delete.
func handleDeleteDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"UPDATE devices SET deleted_at = datetime('now') WHERE id = ? AND deleted_at IS NULL",
			id,
		)
		if err != nil {
			log.Printf("soft delete device error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found or already deleted"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRestoreDevice handles POST /api/devices/{id}/restore.
func handleRestoreDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"UPDATE devices SET deleted_at = NULL WHERE id = ?",
			id,
		)
		if err != nil {
			log.Printf("restore device error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "restored"})
	}
}
