package main

import (
	"database/sql"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// TestIncidentDateFilterNormalizesISO8601 locks the date-filter comparison fix
// (#86). occurred_at is stored as "YYYY-MM-DD HH:MM:SS", but clients send
// ISO-8601 ("2026-04-13T00:00:00Z"). A raw lexicographic comparison mis-orders
// the boundary because 'T'(0x54) > ' '(0x20); routing both sides through
// datetime() coerces them to a common form so the boundary is correct.
func TestIncidentDateFilterNormalizesISO8601(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE incidents (id INTEGER PRIMARY KEY, occurred_at TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	// id -> occurred_at (SQLite datetime format, as actually stored)
	seed := map[int]string{
		1: "2026-04-12 23:59:59", // day before  → excluded
		2: "2026-04-13 00:00:00", // lower boundary → included
		3: "2026-04-13 12:30:00", // mid-day → included
		4: "2026-04-13 23:59:59", // upper boundary → included
		5: "2026-04-14 00:00:01", // day after → excluded
	}
	for id, ts := range seed {
		if _, err := db.Exec(`INSERT INTO incidents (id, occurred_at) VALUES (?, ?)`, id, ts); err != nil {
			t.Fatalf("insert %d: %v", id, err)
		}
	}

	// Client sends ISO-8601 boundaries.
	from := "2026-04-13T00:00:00Z"
	to := "2026-04-13T23:59:59Z"

	query := func(where string) []int {
		rows, err := db.Query("SELECT id FROM incidents WHERE "+where+" ORDER BY id", from, to)
		if err != nil {
			t.Fatalf("query %q: %v", where, err)
		}
		defer rows.Close()
		var ids []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan: %v", err)
			}
			ids = append(ids, id)
		}
		sort.Ints(ids)
		return ids
	}

	// Fixed comparison: must return exactly the three in-range rows.
	got := query("datetime(occurred_at) >= datetime(?) AND datetime(occurred_at) <= datetime(?)")
	want := []int{2, 3, 4}
	if !equalInts(got, want) {
		t.Errorf("fixed filter: got %v, want %v", got, want)
	}

	// Buggy raw comparison must differ (it drops the 00:00:00 boundary because
	// ' ' < 'T'). This documents that the fix is load-bearing, not cosmetic.
	buggy := query("occurred_at >= ? AND occurred_at <= ?")
	if equalInts(buggy, want) {
		t.Errorf("raw lexicographic filter unexpectedly correct (got %v); test no longer proves the fix", buggy)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
