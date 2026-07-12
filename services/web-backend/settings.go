package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
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

// --- Bulk settings (contract 11, assertion V, issue #95) ---

const (
	keySiteURL              = "site_url"
	keyServiceCheckInterval = "health.service_check_interval_sec"
	keyServiceDownThreshold = "health.service_down_threshold_sec"
	keySensorAliveThreshold = "health.sensor_alive_threshold_sec"
)

// intRange is the [min,max] valid range (inclusive) for an integer setting.
type intRange struct{ min, max int }

// knownIntSettings maps each integer setting key to its valid range. The
// presence of a key in one of the known-setting maps defines "known key".
var knownIntSettings = map[string]intRange{
	keyServiceCheckInterval: {5, 3600},
	keyServiceDownThreshold: {5, 86400},
	keySensorAliveThreshold: {5, 86400},
}

// isKnownSettingKey reports whether key is in the contract-11 known-key table.
func isKnownSettingKey(key string) bool {
	if key == keySiteURL {
		return true
	}
	_, ok := knownIntSettings[key]
	return ok
}

// validateKnownSetting checks a single key/value against the contract-11 table
// (unknown key, type, per-key range). Cross-constraints are checked separately
// in validateBulkSettings because they depend on the resulting final state.
func validateKnownSetting(key, value string) error {
	if key == keySiteURL {
		if value == "" {
			return fmt.Errorf("site_url must be a non-empty http(s) URL")
		}
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("site_url must be an absolute http(s) URL")
		}
		return nil
	}
	if rng, ok := knownIntSettings[key]; ok {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be an integer", key)
		}
		if n < rng.min || n > rng.max {
			return fmt.Errorf("%s must be within [%d,%d]", key, rng.min, rng.max)
		}
		return nil
	}
	return fmt.Errorf("unknown setting key: %s", key)
}

// validateBulkSettings validates a full bulk update against the known-key table
// and the cross-constraint (final service_down_threshold_sec >=
// service_check_interval_sec). curInterval/curDown are the current DB values,
// used as the "final" value for any of the two keys the request does not touch.
// Returns nil iff every key is known, every value valid, and the resulting state
// satisfies the cross-constraint — so the caller may apply all-or-nothing.
func validateBulkSettings(updates map[string]string, curInterval, curDown int) error {
	if len(updates) == 0 {
		return fmt.Errorf("no settings provided")
	}
	for k, v := range updates {
		if err := validateKnownSetting(k, v); err != nil {
			return err
		}
	}

	finalInterval := curInterval
	finalDown := curDown
	_, touchInterval := updates[keyServiceCheckInterval]
	_, touchDown := updates[keyServiceDownThreshold]
	if touchInterval {
		finalInterval, _ = strconv.Atoi(updates[keyServiceCheckInterval])
	}
	if touchDown {
		finalDown, _ = strconv.Atoi(updates[keyServiceDownThreshold])
	}
	if (touchInterval || touchDown) && finalDown < finalInterval {
		return fmt.Errorf("%s (%d) must be >= %s (%d)",
			keyServiceDownThreshold, finalDown, keyServiceCheckInterval, finalInterval)
	}
	return nil
}

// decodeBulkSettings parses the bulk PUT body, accepting either an object
// {"<key>":"<value>", ...} (contract 11 form) or an array of {key,value} pairs.
func decodeBulkSettings(body []byte) (map[string]string, error) {
	out := map[string]string{}
	// Object form first (the contract-11 shape).
	if err := json.Unmarshal(body, &out); err == nil {
		return out, nil
	}
	// Array-of-pairs fallback.
	var pairs []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &pairs); err == nil {
		for _, p := range pairs {
			out[p.Key] = p.Value
		}
		return out, nil
	}
	return nil, fmt.Errorf("invalid request body")
}

// currentIntSetting reads a known integer setting from the DB, falling back to
// def when absent or unparseable.
func currentIntSetting(ctx context.Context, db *sql.DB, key string, def int) int {
	var v string
	if err := db.QueryRowContext(ctx, "SELECT value FROM system_settings WHERE key = ?", key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// handleBulkUpdateSettings handles PUT /api/settings (admin only) — atomic
// multi-key save. If ANY key is unknown or any value invalid (type/range/cross-
// constraint), responds 400 with ZERO writes. All-valid → 200 with the updated
// rows. Unknown/invalid in bulk is a 400 (request invalid), distinct from the
// single-key PUT /api/settings/{key} 404 (missing resource).
func handleBulkUpdateSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		updates, err := decodeBulkSettings(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		curInterval := currentIntSetting(ctx, db, keyServiceCheckInterval, 30)
		curDown := currentIntSetting(ctx, db, keyServiceDownThreshold, 90)

		if err := validateBulkSettings(updates, curInterval, curDown); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Apply all keys in a single transaction — all-or-nothing.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("bulk settings begin tx error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		for k, v := range updates {
			res, err := tx.ExecContext(ctx,
				"UPDATE system_settings SET value = ?, updated_at = ? WHERE key = ?", v, now, k)
			if err != nil {
				tx.Rollback()
				log.Printf("bulk settings update error: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			// All known keys are seeded; a missing row means the key is not a
			// persisted setting — reject the whole batch (no partial writes).
			if n, _ := res.RowsAffected(); n == 0 {
				tx.Rollback()
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown setting key: " + k})
				return
			}
		}
		if err := tx.Commit(); err != nil {
			log.Printf("bulk settings commit error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		markWALDirty()

		// Return the updated rows.
		out := make([]SystemSetting, 0, len(updates))
		for k, v := range updates {
			out = append(out, SystemSetting{Key: k, Value: v, UpdatedAt: now})
		}
		log.Printf("bulk settings updated: %d keys", len(updates))
		writeJSON(w, http.StatusOK, out)
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
