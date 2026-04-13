package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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
}

func runMigrations(db *sql.DB) error {
	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	// Create migrations tracking table
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range migrations {
		var exists int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM _migrations WHERE version = ?", m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if exists > 0 {
			continue
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
	}

	return nil
}
