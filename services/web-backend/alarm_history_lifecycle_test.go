package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// --- TDD gates for docs/spec/alarm-history-lifecycle.md (assertions B, E, M, N,
// + sensor-fallback predicate). These are backend logic gates that run without a
// live stack (in-process handlers + temp file SQLite). RED until the alarm-history
// lifecycle change is implemented; do NOT green-wash.
//
// Coverage map:
//   M  → TestMigration_LegacyAcknowledgedPromoted
//   E  → TestListIncidents_StatusSetOpenResolvedOnly (post-migration status set)
//   N  → TestListIncidents_StatusWhitelist
//   B  → TestResolveIncident_NoteOptional_SixVariants
//   I (predicate) → TestSensorFallback_MatchesOpenOnly / TestSensorResolve_OpenDirectTransition
//
// Assertions A·C·D·F·G·I(live)·L are live-API gates under
// tests/spec/alarm-history-lifecycle/ (SKIP-guarded). K·O are needs-browser
// (vitest / Playwright).

// legacyPrePromotionMaxVersion is the highest migration version that exists
// *before* the alarm-history-lifecycle promotion migration is added. The
// promotion migration (legacy acknowledged→open + confirmed_* NULL) must be a
// NEW migration with a version strictly greater than this. Splitting the apply
// here lets assertion M seed an `acknowledged` row against the pre-promotion
// schema and then apply only the promotion migration — a reversible gate that is
// RED today (no promotion migration exists) and GREEN once it is added.
const legacyPrePromotionMaxVersion = 19

// newBareDB opens a fresh file-backed SQLite DB WITHOUT running migrations, so a
// test can drive the migration runner in stages.
func newBareDB(t *testing.T) *sql.DB {
	t.Helper()
	if len(jwtSecret) == 0 {
		jwtSecret = []byte("test-secret-key-for-unit-tests")
	}
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open bare db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// setupMigrationsTable creates the _migrations bookkeeping table (same DDL as
// runMigrations) so applyMigration can be driven directly.
func setupMigrationsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS _migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		t.Fatalf("create _migrations: %v", err)
	}
}

// applyMigrationsWhere applies, in order, every registered migration whose
// version satisfies pred.
func applyMigrationsWhere(t *testing.T, db *sql.DB, pred func(version int) bool) {
	t.Helper()
	for _, m := range migrations {
		if pred(m.version) {
			if err := applyMigration(db, m); err != nil {
				t.Fatalf("apply migration %d (%s): %v", m.version, m.name, err)
			}
		}
	}
}

// seedTestUser inserts an admin user with a known id for handlers that look up
// username/name by AuthUser.UserID.
func seedTestUser(t *testing.T, db *sql.DB, id int64, username, role string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO users (id, username, password_hash, name, role, status) VALUES (?, ?, 'h', ?, ?, 'active')",
		id, username, username, role); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// adminReq builds a request carrying an authenticated admin AuthUser in context
// and the {id} path value set (Go 1.22 SetPathValue).
func adminReq(t *testing.T, method, target, id string, body string) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "__ABSENT__" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if id != "" {
		r.SetPathValue("id", id)
	}
	ctx := context.WithValue(r.Context(), userContextKey, AuthUser{UserID: 1, Role: "admin"})
	return r.WithContext(ctx)
}

// seedIncident inserts an incident with the given status and returns its id.
func seedIncident(t *testing.T, db *sql.DB, siteID, desc, occurredAt, status string) int64 {
	t.Helper()
	res, err := db.Exec(
		"INSERT INTO incidents (site_id, description, occurred_at, is_test, status) VALUES (?, ?, ?, 0, ?)",
		siteID, desc, occurredAt, status)
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// ---------------------------------------------------------------------------
// Assertion M — legacy migration gate: promote + preserve + null-out.
// ---------------------------------------------------------------------------
func TestMigration_LegacyAcknowledgedPromoted(t *testing.T) {
	db := newBareDB(t)
	setupMigrationsTable(t, db)

	// (1) Pre-promotion schema only (through the legacy max version).
	applyMigrationsWhere(t, db, func(v int) bool { return v <= legacyPrePromotionMaxVersion })

	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM incidents").Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	// Seed a legacy acknowledged row with NON-NULL attribution so the null-out is
	// falsifiable (a no-op migration would leave these set → NOK).
	res, err := db.Exec(`INSERT INTO incidents (site_id, description, occurred_at, status, confirmed_at, confirmed_by)
		VALUES ('site-mtest', 'M-gate seed', '2026-01-01 00:00:00', 'acknowledged', '2026-01-01 00:01:00', 'legacy-user')`)
	if err != nil {
		t.Fatalf("seed acknowledged: %v", err)
	}
	sid, _ := res.LastInsertId()

	// (2) Apply ONLY the promotion migration(s) (version > legacy max).
	applyMigrationsWhere(t, db, func(v int) bool { return v > legacyPrePromotionMaxVersion })

	// (3) Same row survived, promoted to open, attribution nulled.
	var status string
	var confirmedAt, confirmedBy sql.NullString
	if err := db.QueryRow("SELECT status, confirmed_at, confirmed_by FROM incidents WHERE id = ?", sid).
		Scan(&status, &confirmedAt, &confirmedBy); err != nil {
		if err == sql.ErrNoRows {
			t.Fatalf("row id=%d lost by migration (row deletion is NOK)", sid)
		}
		t.Fatalf("query seeded row: %v", err)
	}
	if status != "open" {
		t.Errorf("legacy acknowledged row not promoted: status=%q, want open", status)
	}
	if confirmedAt.Valid {
		t.Errorf("confirmed_at not nulled: %q", confirmedAt.String)
	}
	if confirmedBy.Valid {
		t.Errorf("confirmed_by not nulled: %q", confirmedBy.String)
	}

	// Row count preserved (a delete-migration would drop below before+1).
	var after int
	if err := db.QueryRow("SELECT COUNT(*) FROM incidents").Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before+1 {
		t.Errorf("row count not preserved: after=%d, want %d (before+1)", after, before+1)
	}

	// (4) Globally zero acknowledged rows.
	var ackCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM incidents WHERE status = 'acknowledged'").Scan(&ackCount); err != nil {
		t.Fatalf("count acknowledged: %v", err)
	}
	if ackCount != 0 {
		t.Errorf("acknowledged rows still present after migration: %d", ackCount)
	}
}

// ---------------------------------------------------------------------------
// Assertion E — status set is {open, resolved} after migration (no acknowledged
// value survives in any GET /api/incidents element).
// ---------------------------------------------------------------------------
func TestListIncidents_StatusSetOpenResolvedOnly(t *testing.T) {
	db := newBareDB(t)
	setupMigrationsTable(t, db)
	applyMigrationsWhere(t, db, func(v int) bool { return v <= legacyPrePromotionMaxVersion })

	// Seed a mix incl. a legacy acknowledged row before promotion.
	seedIncident(t, db, "s1", "open one", "2026-07-11 10:00:00", "open")
	seedIncident(t, db, "s1", "ack one", "2026-07-11 11:00:00", "acknowledged")
	seedIncident(t, db, "s1", "resolved one", "2026-07-11 12:00:00", "resolved")

	applyMigrationsWhere(t, db, func(v int) bool { return v > legacyPrePromotionMaxVersion })

	req := httptest.NewRequest(http.MethodGet, "/api/incidents?limit=100", nil)
	rec := httptest.NewRecorder()
	handleListIncidents(db)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("no incidents returned (vacuous)")
	}
	for _, d := range resp.Data {
		if d.Status != "open" && d.Status != "resolved" {
			t.Errorf("status %q not in {open, resolved}", d.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Assertion N — status filter whitelist: only {open, resolved} accepted; any
// other value (incl. acknowledged) → 400.
// ---------------------------------------------------------------------------
func TestListIncidents_StatusWhitelist(t *testing.T) {
	db := newTestDB(t)

	cases := []struct {
		status string
		want   int
	}{
		{"open", http.StatusOK},
		{"resolved", http.StatusOK},
		{"acknowledged", http.StatusBadRequest},
		{"bogus", http.StatusBadRequest},
		{"OPEN", http.StatusBadRequest}, // case-sensitive whitelist
	}
	for _, c := range cases {
		t.Run(c.status, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/incidents?status="+c.status, nil)
			rec := httptest.NewRecorder()
			handleListIncidents(db)(rec, req)
			if rec.Code != c.want {
				t.Errorf("status=%q → code=%d, want %d (body=%s)", c.status, rec.Code, c.want, rec.Body.String())
			}
		})
	}

	// No filter (empty) must still be accepted.
	req := httptest.NewRequest(http.MethodGet, "/api/incidents", nil)
	rec := httptest.NewRecorder()
	handleListIncidents(db)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("no status filter → code=%d, want 200", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Assertion B — resolution note is optional: all six body variants → 200, stored
// status resolved, note empty (or NULL). resolve is terminal → fresh open
// incident per variant.
// ---------------------------------------------------------------------------
func TestResolveIncident_NoteOptional_SixVariants(t *testing.T) {
	db := newTestDB(t)
	seedTestUser(t, db, 1, "admin", "admin")

	variants := []struct {
		name string
		body string
	}{
		{"1_absent_body", "__ABSENT__"},
		{"2_empty_body", ""},
		{"3_empty_object", "{}"},
		{"4_field_omitted", `{"x":1}`},
		{"5_empty_string", `{"resolutionNotes":""}`},
		{"6_whitespace", `{"resolutionNotes":"   "}`},
	}

	for i, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			id := seedIncident(t, db, "s-b", fmt.Sprintf("b-%d", i), "2026-07-11 10:00:00", "open")
			req := adminReq(t, http.MethodPatch, "/api/incidents/"+strconv.FormatInt(id, 10)+"/resolve", strconv.FormatInt(id, 10), v.body)
			rec := httptest.NewRecorder()
			handleResolveIncident(db)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("variant %s → code=%d, want 200 (body=%s)", v.name, rec.Code, rec.Body.String())
			}

			var status string
			var notes sql.NullString
			if err := db.QueryRow("SELECT status, resolution_notes FROM incidents WHERE id = ?", id).Scan(&status, &notes); err != nil {
				t.Fatalf("query resolved incident: %v", err)
			}
			if status != "resolved" {
				t.Errorf("variant %s: stored status=%q, want resolved", v.name, status)
			}
			if notes.Valid && strings.TrimSpace(notes.String) != "" {
				t.Errorf("variant %s: note not empty/null: %q", v.name, notes.String)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Assertion I (predicate) — sensor fallback matches status='open' only. With the
// corrected predicate, a leftover `acknowledged` row (should never exist post-M,
// but proves the predicate is `= 'open'` not `!= 'resolved'`) is NOT matched by
// the id=0 fallback → 404.
// ---------------------------------------------------------------------------
func TestSensorFallback_MatchesOpenOnly(t *testing.T) {
	db := newTestDB(t)

	// Only an acknowledged row exists for this site (no open). A `!= 'resolved'`
	// predicate would wrongly resolve it (200); the spec's `= 'open'` predicate
	// must not match → 404.
	seedIncident(t, db, "site-fallback", "stale ack", "2026-07-11 10:00:00", "acknowledged")

	body := `{"incidentId":0,"siteId":"site-fallback","resolvedBy":{"label":"L"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/0/resolve-from-sensor", strings.NewReader(body))
	req.SetPathValue("id", "0")
	rec := httptest.NewRecorder()
	handleResolveIncidentFromSensor(db)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("acknowledged row matched by fallback → code=%d, want 404 (predicate must be status='open') body=%s", rec.Code, rec.Body.String())
	}
}

// TestSensorResolve_OpenDirectTransition — assertion I main path: an open
// incident resolves directly via the sensor endpoint (open → resolved, no
// intermediate ack), kind=sensor_button. This is a preservation gate.
func TestSensorResolve_OpenDirectTransition(t *testing.T) {
	db := newTestDB(t)
	id := seedIncident(t, db, "site-sensor", "open one", "2026-07-11 10:00:00", "open")

	body := fmt.Sprintf(`{"incidentId":%d,"siteId":"site-sensor","resolvedBy":{"label":"L","id":"btn-1"}}`, id)
	req := httptest.NewRequest(http.MethodPost, "/api/incidents/"+strconv.FormatInt(id, 10)+"/resolve-from-sensor", strings.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	handleResolveIncidentFromSensor(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["resolvedByKind"] != "sensor_button" {
		t.Errorf("resolvedByKind=%v, want sensor_button", resp["resolvedByKind"])
	}
	var status string
	if err := db.QueryRow("SELECT status FROM incidents WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "resolved" {
		t.Errorf("status=%q, want resolved", status)
	}
}
