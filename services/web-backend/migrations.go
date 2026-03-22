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
