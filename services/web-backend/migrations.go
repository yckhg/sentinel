package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql: `
			CREATE TABLE IF NOT EXISTS users (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				username TEXT NOT NULL UNIQUE,
				password_hash TEXT NOT NULL,
				name TEXT NOT NULL,
				role TEXT NOT NULL DEFAULT 'user',
				status TEXT NOT NULL DEFAULT 'pending',
				created_at DATETIME NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS contacts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				phone TEXT NOT NULL,
				created_at DATETIME NOT NULL DEFAULT (datetime('now'))
			);

			CREATE TABLE IF NOT EXISTS cameras (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				location TEXT NOT NULL DEFAULT '',
				zone TEXT NOT NULL DEFAULT ''
			);

			CREATE TABLE IF NOT EXISTS sites (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				address TEXT NOT NULL DEFAULT '',
				manager_name TEXT NOT NULL DEFAULT '',
				manager_phone TEXT NOT NULL DEFAULT ''
			);

			CREATE TABLE IF NOT EXISTS incidents (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				site_id TEXT NOT NULL DEFAULT '',
				description TEXT NOT NULL DEFAULT '',
				occurred_at DATETIME NOT NULL DEFAULT (datetime('now')),
				confirmed_at DATETIME,
				confirmed_by TEXT
			);
		`,
	},
	{
		version: 2,
		name:    "seed_youtube_cameras",
		sql: `
			INSERT INTO cameras (name, location, zone)
			SELECT 'yt-cam-1', 'YC Factory', 'Demo - Smart Manufacturing'
			WHERE NOT EXISTS (SELECT 1 FROM cameras WHERE name = 'yt-cam-1');
			INSERT INTO cameras (name, location, zone)
			SELECT 'yt-cam-2', 'YC Factory', 'Demo - CNC Machining'
			WHERE NOT EXISTS (SELECT 1 FROM cameras WHERE name = 'yt-cam-2');
		`,
	},
	{
		version: 3,
		name:    "add_camera_crud_fields",
		sql: `
			ALTER TABLE cameras ADD COLUMN stream_key TEXT NOT NULL DEFAULT '';
			ALTER TABLE cameras ADD COLUMN source_type TEXT NOT NULL DEFAULT 'rtsp';
			ALTER TABLE cameras ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
			ALTER TABLE cameras ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

			UPDATE cameras SET stream_key = name, source_type = 'youtube' WHERE name LIKE 'yt-cam-%';
		`,
	},
	{
		version: 4,
		name:    "create_invitations_table",
		sql: `
			CREATE TABLE IF NOT EXISTS invitations (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				email TEXT NOT NULL,
				token TEXT NOT NULL UNIQUE,
				status TEXT NOT NULL DEFAULT 'pending',
				created_at DATETIME NOT NULL DEFAULT (datetime('now')),
				expires_at DATETIME NOT NULL
			);
		`,
	},
	{
		version: 5,
		name:    "seed_youtube_camera_source_urls",
		sql: `
			UPDATE cameras SET source_url = 'https://www.youtube.com/watch?v=wvBnTOR36A4'
			WHERE stream_key = 'yt-cam-1' AND source_type = 'youtube' AND source_url = '';
			UPDATE cameras SET source_url = 'https://www.youtube.com/watch?v=aqsvNWQTiQ0'
			WHERE stream_key = 'yt-cam-2' AND source_type = 'youtube' AND source_url = '';
		`,
	},
	{
		version: 6,
		name:    "add_stream_key_unique_index",
		sql:     `CREATE UNIQUE INDEX IF NOT EXISTS idx_cameras_stream_key ON cameras(stream_key);`,
	},
	{
		version: 7,
		name:    "add_users_email",
		sql: `ALTER TABLE users ADD COLUMN email TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email IS NOT NULL;`,
	},
	{
		version: 8,
		name:    "add_contacts_email",
		sql: `
			ALTER TABLE contacts ADD COLUMN email TEXT;
			ALTER TABLE contacts ADD COLUMN notify_email INTEGER NOT NULL DEFAULT 0;
		`,
	},
	{
		version: 9,
		name:    "add_incidents_is_test",
		sql:     `ALTER TABLE incidents ADD COLUMN is_test INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 10,
		name:    "create_system_settings",
		sql: `
			CREATE TABLE IF NOT EXISTS system_settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL DEFAULT '',
				updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
			);
			INSERT OR IGNORE INTO system_settings (key, value) VALUES ('site_url', '');
		`,
	},
	{
		version: 11,
		name:    "add_incident_resolution_fields",
		sql: `
			ALTER TABLE incidents ADD COLUMN status TEXT NOT NULL DEFAULT 'open';
			ALTER TABLE incidents ADD COLUMN resolved_at DATETIME;
			ALTER TABLE incidents ADD COLUMN resolved_by TEXT;
			ALTER TABLE incidents ADD COLUMN resolution_notes TEXT;
		`,
	},
	{
		version: 12,
		name:    "create_devices_table",
		sql: `
			CREATE TABLE IF NOT EXISTS devices (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				site_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				alias TEXT NOT NULL DEFAULT '',
				first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
				last_seen DATETIME NOT NULL DEFAULT (datetime('now')),
				deleted_at DATETIME,
				UNIQUE(site_id, device_id)
			);
		`,
	},
	{
		version: 13,
		name:    "add_incidents_device_id",
		sql:     `ALTER TABLE incidents ADD COLUMN device_id TEXT;`,
	},
	{
		version: 14,
		name:    "create_health_events_and_settings",
		sql: `
			CREATE TABLE IF NOT EXISTS health_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				entity_kind TEXT NOT NULL,
				entity_id TEXT NOT NULL,
				status TEXT NOT NULL,
				detected_at DATETIME NOT NULL DEFAULT (datetime('now')),
				detail TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX IF NOT EXISTS idx_health_events_entity ON health_events(entity_kind, entity_id, detected_at DESC);
			INSERT OR IGNORE INTO system_settings (key, value) VALUES ('health.service_check_interval_sec', '30');
			INSERT OR IGNORE INTO system_settings (key, value) VALUES ('health.service_down_threshold_sec', '90');
			INSERT OR IGNORE INTO system_settings (key, value) VALUES ('health.sensor_alive_threshold_sec', '60');
		`,
	},
	{
		version: 15,
		name:    "add_incidents_resolution_attribution",
		sql: `
			ALTER TABLE incidents ADD COLUMN resolved_by_kind TEXT;
			ALTER TABLE incidents ADD COLUMN resolved_by_id TEXT;
			ALTER TABLE incidents ADD COLUMN resolved_by_label TEXT;
		`,
	},
	{
		version: 16,
		name:    "remove_cam_a4184f17",
		sql: `
			DELETE FROM cameras WHERE stream_key = 'cam-a4184f17';
		`,
	},
	{
		version: 17,
		name:    "add_incidents_alert_id",
		sql: `ALTER TABLE incidents ADD COLUMN alert_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_alert_id ON incidents(alert_id) WHERE alert_id IS NOT NULL;`,
	},
	{
		version: 18,
		name:    "add_devices_alert_state",
		sql:     `ALTER TABLE devices ADD COLUMN alert_state TEXT NOT NULL DEFAULT 'none';`,
	},
	{
		version: 19,
		name:    "add_users_password_changed_at",
		// Credential-change boundary (issue #83 / assertion Q2). NULL means "no
		// boundary" so pre-existing tokens stay valid (assertion Q). It is set to
		// the change instant on POST /api/auth/change-password; auth then rejects
		// any token whose iat precedes this value. DB-persisted so a stolen
		// pre-change token does not resurrect after a container restart.
		sql: `ALTER TABLE users ADD COLUMN password_changed_at DATETIME;`,
	},
	{
		version: 20,
		name:    "promote_legacy_acknowledged_incidents",
		// Alarm-history-lifecycle: the intermediate 'acknowledged' state is removed
		// from the contract (state machine open→resolved only). Legacy rows still
		// carrying status='acknowledged' are promoted back to 'open' (in-progress =
		// unresolved, row preserved — no deletion) and their confirmed_at/confirmed_by
		// attribution is NULLed so the "confirmedAt/confirmedBy are always null"
		// contract holds for promoted rows too. After this runs, no 'acknowledged'
		// value exists anywhere in the incidents table.
		sql: `UPDATE incidents SET status = 'open', confirmed_at = NULL, confirmed_by = NULL WHERE status = 'acknowledged';`,
	},
	{
		version: 21,
		name:    "devices_last_seen_nullable_rebuild",
		// Sensor-device-lifecycle: make last_seen NULLABLE so an explicitly
		// registered device can sit "offline 대기" (last_seen IS NULL) until its
		// first heartbeat. SQLite cannot drop a column's NOT NULL with ALTER, so the
		// table is rebuilt: create a twin with last_seen nullable, copy every row
		// (preserving surrogate ids and all columns incl. alert_state), drop the old
		// table, rename. The inline UNIQUE(site_id, device_id) recreates the
		// composite-key index. No other table has a FK onto devices, so the drop is
		// safe under foreign_keys=ON.
		sql: `
			CREATE TABLE devices_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				site_id TEXT NOT NULL,
				device_id TEXT NOT NULL,
				alias TEXT NOT NULL DEFAULT '',
				first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
				last_seen DATETIME,
				deleted_at DATETIME,
				alert_state TEXT NOT NULL DEFAULT 'none',
				UNIQUE(site_id, device_id)
			);
			INSERT INTO devices_new (id, site_id, device_id, alias, first_seen, last_seen, deleted_at, alert_state)
				SELECT id, site_id, device_id, alias, first_seen, last_seen, deleted_at, alert_state FROM devices;
			DROP TABLE devices;
			ALTER TABLE devices_new RENAME TO devices;
		`,
	},
	{
		version: 22,
		name:    "add_devices_reappear_alerted_at",
		// Sensor-device-lifecycle: dedup state for the "삭제 후 재출현" alert. A
		// nullable timestamp set once (NULL→now) via a rowcount-guarded UPDATE when a
		// soft-deleted device signals again; reset to NULL on reactivation so the next
		// delete→reappear cycle can alert once more. A dedicated column (not a
		// last_seen≤deleted_at edge) is required because an explicitly-registered then
		// deleted device has last_seen IS NULL, which no timestamp edge can express.
		sql: `ALTER TABLE devices ADD COLUMN reappear_alerted_at DATETIME;`,
	},
}

// migrationTimeout bounds each individual migration (and the bookkeeping steps)
// on its own deadline, rather than sharing a single short deadline across the
// whole batch. A future data-backfill migration can take longer than the 5s
// per-statement dbCtx; giving each migration a fresh, generous deadline keeps
// startup from failing spuriously as more migrations accumulate.
const migrationTimeout = 5 * time.Minute

func runMigrations(db *sql.DB) error {
	// Create migrations tracking table (its own short-lived context).
	setupCtx, setupCancel := dbCtx(context.Background())
	_, err := db.ExecContext(setupCtx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`)
	setupCancel()
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range migrations {
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}

	return nil
}

// applyMigration applies a single migration under its own deadline so one
// long-running migration cannot exhaust a batch-wide timeout.
func applyMigration(db *sql.DB, m migration) error {
	// Each migration gets a fresh, generous deadline of its own.
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()

	var exists int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM _migrations WHERE version = ?", m.version).Scan(&exists); err != nil {
		return fmt.Errorf("check migration %d: %w", m.version, err)
	}
	if exists > 0 {
		return nil
	}

	log.Printf("applying migration %d: %s", m.version, m.name)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		tx.Rollback()
		return fmt.Errorf("execute migration %d: %w", m.version, err)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO _migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		tx.Rollback()
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}

	log.Printf("migration %d applied successfully", m.version)
	return nil
}
