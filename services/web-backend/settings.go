package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type SystemSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updatedAt"`
}

// getSettingValue reads a single setting from the database.
// Returns empty string if not found.
func getSettingValue(db *sql.DB, key string) string {
	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM system_settings WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

// handleListSettings handles GET /api/settings (admin only)
func handleListSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx, "SELECT key, value, updated_at FROM system_settings ORDER BY key")
		if err != nil {
			log.Printf("list settings error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		settings := []SystemSetting{}
		for rows.Next() {
			var s SystemSetting
			if err := rows.Scan(&s.Key, &s.Value, &s.UpdatedAt); err != nil {
				log.Printf("scan setting error: %v", err)
				continue
			}
			settings = append(settings, s)
		}

		writeJSON(w, http.StatusOK, settings)
	}
}

// handleUpdateSetting handles PUT /api/settings/{key} (admin only)
func handleUpdateSetting(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		key := r.PathValue("key")
		if key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
			return
		}

		var req struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		result, err := db.ExecContext(ctx,
			"UPDATE system_settings SET value = ?, updated_at = ? WHERE key = ?",
			req.Value, now, key)
		if err != nil {
			log.Printf("update setting error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rows, _ := result.RowsAffected()
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "setting not found"})
			return
		}

		log.Printf("setting updated: %s = %s", key, req.Value)
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": req.Value, "updatedAt": now})
	}
}

// handleInternalGetSetting handles GET /internal/settings/{key}
// Internal endpoint — no auth required (Docker network only)
func handleInternalGetSetting(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
			return
		}

		value := getSettingValue(db, key)
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": value})
	}
}
