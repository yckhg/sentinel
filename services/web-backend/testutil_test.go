package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB opens a fresh file-backed SQLite database in a temp dir and runs all
// migrations. A file (not :memory:) is used so the connection pool shares one
// database. jwtSecret is set so JWT helpers work in tests.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	if len(jwtSecret) == 0 {
		jwtSecret = []byte("test-secret-key-for-unit-tests")
	}
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}
