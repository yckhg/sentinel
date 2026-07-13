package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type deviceResponse struct {
	ID         int64   `json:"id"`
	SiteID     string  `json:"siteId"`
	DeviceID   string  `json:"deviceId"`
	Alias      string  `json:"alias"`
	FirstSeen  string  `json:"firstSeen"`
	LastSeen   *string `json:"lastSeen"`
	DeletedAt  *string `json:"deletedAt"`
	AlertState string  `json:"alertState"`
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

		// Composite-key filter (spec 계약 6 델타): a device search maps the operator's
		// synthetic siteId:deviceId to the numeric DB id via GET /api/devices?siteId=&deviceId=.
		siteID := strings.TrimSpace(r.URL.Query().Get("siteId"))
		deviceID := strings.TrimSpace(r.URL.Query().Get("deviceId"))

		query := `SELECT id, site_id, device_id, alias, datetime(first_seen), datetime(last_seen), datetime(deleted_at), alert_state FROM devices`
		args := []any{}
		where := []string{}
		if !includeDeleted {
			where = append(where, `deleted_at IS NULL`)
		}
		if siteID != "" {
			where = append(where, `site_id = ?`)
			args = append(args, siteID)
		}
		if deviceID != "" {
			where = append(where, `device_id = ?`)
			args = append(args, deviceID)
		}
		if len(where) > 0 {
			query += ` WHERE ` + strings.Join(where, ` AND `)
		}
		query += ` ORDER BY id ASC`

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			log.Printf("query devices error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		devices := []deviceResponse{}
		for rows.Next() {
			var d deviceResponse
			if err := rows.Scan(&d.ID, &d.SiteID, &d.DeviceID, &d.Alias, &d.FirstSeen, &d.LastSeen, &d.DeletedAt, &d.AlertState); err != nil {
				log.Printf("scan device error: %v", err)
				continue
			}
			devices = append(devices, d)
		}

		writeJSON(w, http.StatusOK, devices)
	}
}

// handleGetDevice handles GET /api/devices/{id} — single non-deleted device by
// numeric DB id (spec 계약 6 델타, assertion D). Returns the device object
// (lastSeen/alertState included) so the panel can derive current state via the
// shared category function. Unregistered/soft-deleted id → 404 (계약 6 규약).
func handleGetDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid device id"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		var d deviceResponse
		err = db.QueryRowContext(ctx,
			`SELECT id, site_id, device_id, alias, datetime(first_seen), datetime(last_seen), datetime(deleted_at), alert_state
			 FROM devices WHERE id = ? AND deleted_at IS NULL`, id,
		).Scan(&d.ID, &d.SiteID, &d.DeviceID, &d.Alias, &d.FirstSeen, &d.LastSeen, &d.DeletedAt, &d.AlertState)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
			return
		}
		if err != nil {
			log.Printf("get device error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, d)
	}
}

// handleCreateDevice handles POST /api/devices (admin) — explicit register OR
// reactivate. This is the SINGLE explicit path that brings a soft-deleted sensor
// back under management (계약 1). Three atomic branches on UNIQUE(site_id,device_id):
//
//	no row            → INSERT (last_seen NULL = offline 대기, deleted_at NULL) → 201
//	row, not deleted  → 409 (duplicate registration guard)
//	row, soft-deleted → reactivate (deleted_at NULL, reappear_alerted_at NULL,
//	                    last_seen UNCHANGED, alias updated only if provided) → 200
//
// alias is *string so a missing field (nil) is distinguished from an empty string.
func handleCreateDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		var req struct {
			SiteID   string  `json:"siteId"`
			DeviceID string  `json:"deviceId"`
			Alias    *string `json:"alias"`
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

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("create device begin tx error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer tx.Rollback()

		var id int64
		var deletedAt *string
		err = tx.QueryRowContext(ctx,
			`SELECT id, datetime(deleted_at) FROM devices WHERE site_id = ? AND device_id = ?`,
			req.SiteID, req.DeviceID,
		).Scan(&id, &deletedAt)

		status := http.StatusOK
		switch {
		case err == sql.ErrNoRows:
			// New device: explicit NULL last_seen (offline 대기 until first heartbeat).
			alias := ""
			if req.Alias != nil {
				alias = strings.TrimSpace(*req.Alias)
			}
			res, insErr := tx.ExecContext(ctx,
				`INSERT INTO devices (site_id, device_id, alias, last_seen, alert_state)
				 VALUES (?, ?, ?, NULL, 'none')`,
				req.SiteID, req.DeviceID, alias,
			)
			if insErr != nil {
				// Concurrency: two POSTs for the same (siteId,deviceId) can both pass the
				// SELECT (no row yet) and race into INSERT. UNIQUE(site_id,device_id) lets
				// one win; the loser's INSERT fails with SQLITE_CONSTRAINT_UNIQUE. Treat it
				// as the duplicate case (409), closing the check-then-insert window.
				if isUniqueViolation(insErr) {
					writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered"})
					return
				}
				log.Printf("create device insert error: %v", insErr)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			id, _ = res.LastInsertId()
			status = http.StatusCreated
		case err != nil:
			log.Printf("create device lookup error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		case deletedAt == nil:
			// Already exists and is not deleted → duplicate.
			writeJSON(w, http.StatusConflict, map[string]string{"error": "device already registered"})
			return
		default:
			// Soft-deleted → reactivate. last_seen is left untouched; reappear dedup
			// resets so a later delete→reappear cycle alerts once more.
			if req.Alias != nil {
				_, upErr := tx.ExecContext(ctx,
					`UPDATE devices SET deleted_at = NULL, reappear_alerted_at = NULL, alias = ? WHERE id = ?`,
					strings.TrimSpace(*req.Alias), id,
				)
				if upErr != nil {
					log.Printf("reactivate device error: %v", upErr)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
					return
				}
			} else {
				_, upErr := tx.ExecContext(ctx,
					`UPDATE devices SET deleted_at = NULL, reappear_alerted_at = NULL WHERE id = ?`, id,
				)
				if upErr != nil {
					log.Printf("reactivate device error: %v", upErr)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
					return
				}
			}
		}

		var d deviceResponse
		err = tx.QueryRowContext(ctx,
			`SELECT id, site_id, device_id, alias, datetime(first_seen), datetime(last_seen), datetime(deleted_at), alert_state
			 FROM devices WHERE id = ?`, id,
		).Scan(&d.ID, &d.SiteID, &d.DeviceID, &d.Alias, &d.FirstSeen, &d.LastSeen, &d.DeletedAt, &d.AlertState)
		if err != nil {
			log.Printf("create device read-back error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if err := tx.Commit(); err != nil {
			log.Printf("create device commit error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		markWALDirty()

		writeJSON(w, status, d)
	}
}

// handleSeenDevice handles POST /api/devices/seen from hw-gateway (internal —
// X-Internal-Token, fail-closed). Body: {"siteId","deviceId","alertState"?}.
// Presence-only: it updates last_seen (and alert_state when provided) and NEVER
// touches deleted_at — a soft-deleted device stays deleted (sticky, 계약 2). If the
// device is soft-deleted, the shared reappear helper broadcasts device_reappeared
// exactly once per delete→reappear cycle.
func handleSeenDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkInternalToken(w, r) {
			return
		}
		var req struct {
			SiteID     string  `json:"siteId"`
			DeviceID   string  `json:"deviceId"`
			AlertState *string `json:"alertState"`
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

		// Presence upsert + reappearance dedup in ONE transaction (계약 2 "두 문장 같은
		// 트랜잭션"). last_seen only; deleted_at is DELIBERATELY absent so a re-signal
		// cannot silently revive a soft-deleted device (sticky delete). alert_state is
		// preserved when the notification omits it (COALESCE), and defaults to 'none'
		// for a brand-new auto-discovered row. RETURNING datetime(deleted_at) lets us
		// gate the reappearance guard: the normal-heartbeat hot path (live device →
		// NULL) does ZERO extra write; only a seen that landed on a soft-deleted row
		// runs the rowcount-guarded dedup.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("seen begin tx error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer tx.Rollback()

		var deletedAt *string
		err = tx.QueryRowContext(ctx, `
			INSERT INTO devices (site_id, device_id, last_seen, alert_state)
			VALUES (?, ?, datetime('now'), COALESCE(?, 'none'))
			ON CONFLICT(site_id, device_id) DO UPDATE SET
				last_seen = datetime('now'),
				alert_state = COALESCE(?, alert_state)
			RETURNING datetime(deleted_at)
		`, req.SiteID, req.DeviceID, req.AlertState, req.AlertState).Scan(&deletedAt)
		if err != nil {
			log.Printf("upsert device error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		var pending *reappearBroadcast
		if deletedAt != nil {
			pending, err = guardReappearTx(ctx, tx, req.SiteID, req.DeviceID)
			if err != nil {
				log.Printf("reappear guard error: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		}
		if err := tx.Commit(); err != nil {
			log.Printf("seen commit error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		markWALDirty()
		if pending != nil {
			BroadcastDeviceReappeared(pending.siteID, pending.deviceID, pending.lastSeen)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// reappearBroadcast carries a pending device_reappeared broadcast so the caller can
// emit it AFTER the transaction commits (never on a rolled-back guard).
type reappearBroadcast struct {
	siteID, deviceID string
	lastSeen         *string
}

// guardReappearTx runs the shared rowcount-guarded reappearance dedup INSIDE tx. It
// is the single dedup+broadcast decision shared by BOTH the seen and incident
// presence paths (계약 2·3), and must be invoked ONLY when the presence upsert landed
// on a soft-deleted row (RETURNING deleted_at non-NULL) — the live-device hot path
// skips it entirely, so a normal heartbeat performs no extra write. The UPDATE flips
// reappear_alerted_at from NULL→now; a broadcast is warranted only when exactly one
// row changed (changes()==1), so the alert fires exactly once per delete→reappear
// cycle regardless of clock resolution or which path (heartbeat or crisis) arrives
// first (a shared guard means one path cannot silently consume the other's alert).
// Reactivation resets reappear_alerted_at, re-arming the next cycle.
func guardReappearTx(ctx context.Context, tx *sql.Tx, siteID, deviceID string) (*reappearBroadcast, error) {
	res, err := tx.ExecContext(ctx, `
		UPDATE devices SET reappear_alerted_at = datetime('now')
		WHERE site_id = ? AND device_id = ? AND deleted_at IS NOT NULL AND reappear_alerted_at IS NULL
	`, siteID, deviceID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, nil
	}
	var lastSeen *string
	if err := tx.QueryRowContext(ctx,
		`SELECT datetime(last_seen) FROM devices WHERE site_id = ? AND device_id = ?`,
		siteID, deviceID,
	).Scan(&lastSeen); err != nil {
		return nil, err
	}
	return &reappearBroadcast{siteID: siteID, deviceID: deviceID, lastSeen: lastSeen}, nil
}

// handleUpdateDeviceAlias handles PATCH /api/devices/{id} — update alias (admin).
func handleUpdateDeviceAlias(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}
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

// handleDeleteDevice handles DELETE /api/devices/{id} — soft delete (admin).
// Delete is STICKY: a subsequent seen/incident re-signal does not revive it; the
// only way back is the POST /api/devices reactivation (계약 1).
func handleDeleteDevice(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}
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
