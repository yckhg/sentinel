package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Sensor-device-lifecycle assertion gates (docs/spec/sensor-device-lifecycle.md).
//
// Always-on (isolated web-backend + static): A, B, C, C2, H2, I, I2, J.
// SKIP (mutating — isolated stack + admin JWT + INTERNAL_TOKEN + WS observer):
//   A2, D, E, E2, F, F2, G, H1.
// SKIP (needs-browser): K.
// L (hw-gateway static header) lives in tests/spec/sensor-device-lifecycle/ because
// it inspects ../hw-gateway/main.go, outside this module's build mount.

// roleReq builds a request carrying an authenticated AuthUser of the given role in
// context, the optional {id} path value, and an optional body.
func roleReq(role, method, target, id, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if id != "" {
		r.SetPathValue("id", id)
	}
	ctx := context.WithValue(r.Context(), userContextKey, AuthUser{UserID: 1, Role: role})
	return r.WithContext(ctx)
}

func decodeDevices(t *testing.T, body []byte) []deviceResponse {
	t.Helper()
	var out []deviceResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode devices: %v (body=%s)", err, body)
	}
	return out
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }

// --- migration integrity: rebuild (21) preserves data + UNIQUE, adds nullable ---
// Proves the last_seen-nullable table rebuild copies existing rows (ids + all
// columns), keeps UNIQUE(site_id,device_id), allows NULL last_seen, and that
// migration 22 adds reappear_alerted_at.

func TestSensorLifecycle_MigrationRebuildPreservesData(t *testing.T) {
	db := newBareDB(t)
	setupMigrationsTable(t, db)

	// Apply everything BEFORE the rebuild (devices table exists at v12; alert_state
	// at v18) so we can seed rows under the pre-rebuild schema.
	applyMigrationsWhere(t, db, func(v int) bool { return v <= 20 })

	// Seed two rows: one live, one soft-deleted — with distinct alert_state/last_seen.
	if _, err := db.Exec(`INSERT INTO devices (id, site_id, device_id, alias, first_seen, last_seen, deleted_at, alert_state)
		VALUES (101, 'site-A', 'dev-1', 'live-alias', '2026-01-01 00:00:00', '2026-01-02 03:04:05', NULL, 'active'),
		       (102, 'site-A', 'dev-2', '', '2026-01-01 00:00:00', '2026-01-03 00:00:00', '2026-01-04 00:00:00', 'none')`); err != nil {
		t.Fatalf("seed pre-rebuild rows: %v", err)
	}

	// Apply the rebuild + reappear column.
	applyMigrationsWhere(t, db, func(v int) bool { return v >= 21 })

	// Row count + values preserved (ids, alias, timestamps, deleted_at, alert_state).
	var siteID, deviceID, alias, firstSeen, lastSeen, alertState string
	var deletedAt *string
	if err := db.QueryRow(`SELECT site_id, device_id, alias, datetime(first_seen), datetime(last_seen), datetime(deleted_at), alert_state FROM devices WHERE id=101`).
		Scan(&siteID, &deviceID, &alias, &firstSeen, &lastSeen, &deletedAt, &alertState); err != nil {
		t.Fatalf("read preserved row 101: %v", err)
	}
	if siteID != "site-A" || deviceID != "dev-1" || alias != "live-alias" || lastSeen != "2026-01-02 03:04:05" || alertState != "active" || deletedAt != nil {
		t.Errorf("row 101 not preserved: site=%s dev=%s alias=%s last=%s alert=%s deleted=%v", siteID, deviceID, alias, lastSeen, alertState, deletedAt)
	}
	var deleted102 *string
	if err := db.QueryRow(`SELECT datetime(deleted_at) FROM devices WHERE id=102`).Scan(&deleted102); err != nil {
		t.Fatalf("read row 102: %v", err)
	}
	if deleted102 == nil || *deleted102 != "2026-01-04 00:00:00" {
		t.Errorf("row 102 deleted_at not preserved, got %v", deleted102)
	}

	// last_seen is now NULLABLE: an explicit NULL insert must succeed.
	if _, err := db.Exec(`INSERT INTO devices (site_id, device_id, alias, last_seen, alert_state) VALUES ('site-B','dev-3','',NULL,'none')`); err != nil {
		t.Fatalf("NULL last_seen insert must succeed after rebuild: %v", err)
	}
	var nullLast *string
	if err := db.QueryRow(`SELECT datetime(last_seen) FROM devices WHERE site_id='site-B' AND device_id='dev-3'`).Scan(&nullLast); err != nil {
		t.Fatalf("read null last_seen: %v", err)
	}
	if nullLast != nil {
		t.Errorf("expected NULL last_seen, got %v", *nullLast)
	}

	// UNIQUE(site_id, device_id) still enforced.
	if _, err := db.Exec(`INSERT INTO devices (site_id, device_id, last_seen, alert_state) VALUES ('site-A','dev-1',datetime('now'),'none')`); err == nil {
		t.Errorf("UNIQUE(site_id,device_id) must reject a duplicate after rebuild")
	}

	// reappear_alerted_at column exists (migration 22) and is writable.
	if _, err := db.Exec(`UPDATE devices SET reappear_alerted_at = datetime('now') WHERE id=102`); err != nil {
		t.Errorf("reappear_alerted_at column must exist after migration 22: %v", err)
	}
}

// --- assertion A: explicit register → 201, offline 대기 (lastSeen null) ----------

func TestSensorLifecycle_A_ExplicitRegister(t *testing.T) {
	db := newTestDB(t)

	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-new-01","alias":"북문 음성센서"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("A: expected 201, got %d body=%s", w.Code, w.Body.String())
	}

	lw := httptest.NewRecorder()
	handleListDevices(db)(lw, roleReq("admin", http.MethodGet, "/api/devices", "", ""))
	devices := decodeDevices(t, lw.Body.Bytes())
	found := false
	for _, d := range devices {
		if d.SiteID == "site-001" && d.DeviceID == "vs-new-01" {
			found = true
			if d.LastSeen != nil {
				t.Errorf("A: expected lastSeen null (offline 대기), got %v", *d.LastSeen)
			}
			if d.DeletedAt != nil {
				t.Errorf("A: expected no deletedAt, got %v", *d.DeletedAt)
			}
		}
	}
	if !found {
		t.Fatalf("A: registered device not present in GET /api/devices")
	}
}

// --- assertion B: duplicate registration of a live device → 409 ------------------

func TestSensorLifecycle_B_DuplicateConflict(t *testing.T) {
	db := newTestDB(t)
	body := `{"siteId":"site-001","deviceId":"vs-dup"}`

	w1 := httptest.NewRecorder()
	handleCreateDevice(db)(w1, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w1.Code != http.StatusCreated {
		t.Fatalf("B: first create expected 201, got %d", w1.Code)
	}
	w2 := httptest.NewRecorder()
	handleCreateDevice(db)(w2, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w2.Code != http.StatusConflict {
		t.Fatalf("B: duplicate create expected 409, got %d body=%s", w2.Code, w2.Body.String())
	}
}

// --- assertion C: reactivate a soft-deleted device via POST /api/devices → 200 ---
// last_seen unchanged, deleted_at cleared, reappear_alerted_at reset, alias updated.

func TestSensorLifecycle_C_ReactivateSinglePath(t *testing.T) {
	db := newTestDB(t)

	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-react","alias":"old"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("C: create expected 201, got %d", w.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// stamp a known last_seen + a prior reappear alert, then soft-delete
	if _, err := db.Exec(
		`UPDATE devices SET last_seen='2026-01-01 00:00:00', reappear_alerted_at='2026-01-02 00:00:00' WHERE id=?`,
		created.ID); err != nil {
		t.Fatalf("C: stamp last_seen: %v", err)
	}
	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("C: delete expected 204, got %d", dw.Code)
	}

	// reactivate with a new alias
	rw := httptest.NewRecorder()
	handleCreateDevice(db)(rw, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-react","alias":"new"}`))
	if rw.Code != http.StatusOK {
		t.Fatalf("C: reactivate expected 200, got %d body=%s", rw.Code, rw.Body.String())
	}

	var lastSeen, deletedAt, reappear, alias *string
	if err := db.QueryRow(
		`SELECT datetime(last_seen), datetime(deleted_at), datetime(reappear_alerted_at), alias FROM devices WHERE id=?`,
		created.ID).Scan(&lastSeen, &deletedAt, &reappear, &alias); err != nil {
		t.Fatalf("C: read-back: %v", err)
	}
	if lastSeen == nil || *lastSeen != "2026-01-01 00:00:00" {
		t.Errorf("C: last_seen must be unchanged, got %v", lastSeen)
	}
	if deletedAt != nil {
		t.Errorf("C: deleted_at must be cleared, got %v", *deletedAt)
	}
	if reappear != nil {
		t.Errorf("C: reappear_alerted_at must be reset to NULL, got %v", *reappear)
	}
	if alias == nil || *alias != "new" {
		t.Errorf("C: alias must update to 'new', got %v", alias)
	}
}

// --- F1: once-only reappearance — ALWAYS-ON, non-vacuous gate ---------------------
// Proves EXACTLY-ONE device_reappeared per delete→reappear cycle, independent of
// clock resolution. The vacuity hazard (a back-to-back same-second 2nd seen produces
// an identical timestamp, so a naive equality check can't tell "not re-stamped" from
// "re-stamped to the same second") is closed by forcing a DISTINCT past sentinel
// between the two seens and asserting it stays unchanged, PLUS counting broadcasts.
// EXPERIMENT (verified, see report): deleting `AND reappear_alerted_at IS NULL` from
// guardReappear's WHERE turns this test RED (2nd seen re-stamps sentinel→now AND
// broadcast count becomes 2).
func TestSensorLifecycle_ReappearGuardOnceOnly(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	var broadcasts int32
	origB := BroadcastDeviceReappeared
	BroadcastDeviceReappeared = func(_, _ string, _ *string) { atomic.AddInt32(&broadcasts, 1) }
	t.Cleanup(func() { BroadcastDeviceReappeared = origB })

	body := `{"siteId":"site-001","deviceId":"vs-guard"}`
	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", w.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete expected 204, got %d", dw.Code)
	}

	// First seen after delete → reappear_alerted_at set, broadcast once.
	callSeen(db, "sekret", body)
	if reappearAt(t, db, created.ID) == nil {
		t.Fatalf("first seen after delete must set reappear_alerted_at")
	}

	// Force a DISTINCT past sentinel so a spurious re-stamp is detectable even at the
	// same wall-clock second — this is what makes the gate non-vacuous.
	const sentinel = "2000-01-01 00:00:00"
	if _, err := db.Exec(`UPDATE devices SET reappear_alerted_at=? WHERE id=?`, sentinel, created.ID); err != nil {
		t.Fatalf("stamp sentinel: %v", err)
	}

	// Second seen (same cycle) → must NOT re-stamp and must NOT re-broadcast.
	callSeen(db, "sekret", body)
	if after := reappearAt(t, db, created.ID); after == nil || *after != sentinel {
		t.Errorf("once-only: 2nd seen must not re-stamp reappear_alerted_at (want %q, got %v)", sentinel, after)
	}
	// Still sticky-deleted after both re-signals.
	var deletedAt *string
	if err := db.QueryRow(`SELECT datetime(deleted_at) FROM devices WHERE id=?`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("device must remain soft-deleted after re-signals (sticky)")
	}
	if got := atomic.LoadInt32(&broadcasts); got != 1 {
		t.Errorf("once-only: exactly 1 device_reappeared per cycle, got %d", got)
	}

	// Reactivate → reset; delete + seen → alerts AGAIN (new cycle re-armed).
	rw := httptest.NewRecorder()
	handleCreateDevice(db)(rw, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if rw.Code != http.StatusOK {
		t.Fatalf("reactivate expected 200, got %d", rw.Code)
	}
	if reappearAt(t, db, created.ID) != nil {
		t.Errorf("reactivation must reset reappear_alerted_at")
	}
	dw2 := httptest.NewRecorder()
	handleDeleteDevice(db)(dw2, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	callSeen(db, "sekret", body)
	if got := atomic.LoadInt32(&broadcasts); got != 2 {
		t.Errorf("new cycle after reactivation must alert again (want 2 total), got %d", got)
	}
}

// --- F2: hot-path zero-write — ALWAYS-ON gate ------------------------------------
// Asserts a live-device seen does NOT invoke the reappearance guard at all (the
// RETURNING deleted_at gate skips it). Counts guard INVOCATIONS via the guardReappearFn
// seam, because a row-count metric (total_changes) cannot distinguish "guard skipped"
// from "guard ran but matched 0 rows" — a 0-row UPDATE changes nothing.
// EXPERIMENT (verified, see report): changing the gate to `if true` (always call the
// guard) turns this test RED (live seen → guardCalls==1).
func TestSensorLifecycle_HotPathZeroWrite(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	var guardCalls int32
	orig := guardReappearFn
	guardReappearFn = func(ctx context.Context, d *sql.DB, s, dev string) (*reappearBroadcast, error) {
		atomic.AddInt32(&guardCalls, 1)
		return orig(ctx, d, s, dev)
	}
	t.Cleanup(func() { guardReappearFn = orig })

	body := `{"siteId":"site-001","deviceId":"vs-hot"}`
	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", w.Code)
	}

	// Hot path: seen on a LIVE device must not invoke the guard.
	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("seen(live) expected 200, got %d", code)
	}
	if got := atomic.LoadInt32(&guardCalls); got != 0 {
		t.Errorf("hot path: live-device seen must not invoke the reappear guard, got %d call(s)", got)
	}

	// Gate opens after delete: a deleted-device seen invokes the guard exactly once.
	var id int64
	if err := db.QueryRow(`SELECT id FROM devices WHERE site_id='site-001' AND device_id='vs-hot'`).Scan(&id); err != nil {
		t.Fatalf("read id: %v", err)
	}
	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(id), idStr(id), ""))
	callSeen(db, "sekret", body)
	if got := atomic.LoadInt32(&guardCalls); got != 1 {
		t.Errorf("deleted-device seen must invoke the guard exactly once, got %d", got)
	}
}

func reappearAt(t *testing.T, db *sql.DB, id int64) *string {
	t.Helper()
	var ra *string
	if err := db.QueryRow(`SELECT datetime(reappear_alerted_at) FROM devices WHERE id=?`, id).Scan(&ra); err != nil {
		t.Fatalf("read reappear_alerted_at: %v", err)
	}
	return ra
}

// --- assertion C2 (static): no POST /api/devices/{id}/restore route/handler -------

func TestSensorLifecycle_C2_NoRestoreRoute(t *testing.T) {
	for _, f := range []string{"main.go", "devices.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("C2: read %s: %v", f, err)
		}
		s := string(src)
		if strings.Contains(s, "handleRestoreDevice") {
			t.Errorf("C2: %s still references handleRestoreDevice", f)
		}
		if strings.Contains(s, "/restore") {
			t.Errorf("C2: %s still references a /restore route", f)
		}
	}
}

// --- assertion H2 (static): handleCreateIncident body has no deleted_at=NULL SET --

func TestSensorLifecycle_H2_IncidentPresenceSticky(t *testing.T) {
	src, err := os.ReadFile("incidents.go")
	if err != nil {
		t.Fatalf("H2: read incidents.go: %v", err)
	}
	s := string(src)
	// Scope = handleCreateIncident body AND the delegated presence-upsert helper
	// (upsertIncidentPresence). The spec requires following the delegation so the
	// gate never becomes vacuous when the upsert moves out of the handler body.
	scope := funcBodyBySig(t, s, "func handleCreateIncident") + funcBodyBySig(t, s, "func upsertIncidentPresence")
	// Normalize: strip all whitespace, lowercase. The forbidden form is the
	// ASSIGNMENT `deleted_at = NULL` (a SET). A read like `deleted_at IS NOT NULL`
	// or `RETURNING datetime(deleted_at)` is allowed. After stripping spaces the
	// assignment collapses to "deleted_at=null"; the reads do not contain that
	// substring.
	norm := strings.ToLower(strings.Join(strings.Fields(scope), ""))
	if strings.Contains(norm, "deleted_at=null") {
		t.Errorf("H2: incident presence upsert must not assign deleted_at = NULL (silent revive)")
	}
	// Non-vacuity guard: the scope must actually contain the presence upsert, so a
	// refactor that removes it cannot make this gate pass trivially.
	if !strings.Contains(norm, "insertintodevices") {
		t.Errorf("H2: scope no longer contains the device presence upsert — gate would be vacuous")
	}
}

// --- assertion I: /api/devices/seen internal-token fail-closed gate ---------------

func TestSensorLifecycle_I_SeenInternalGate(t *testing.T) {
	db := newTestDB(t)
	body := `{"siteId":"s","deviceId":"d"}`

	internalToken = "" // server secret unset → fail-closed
	if code := callSeen(db, "anything", body); code != http.StatusUnauthorized {
		t.Errorf("I: unset server secret must 401, got %d", code)
	}

	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })
	if code := callSeen(db, "", body); code != http.StatusUnauthorized {
		t.Errorf("I: missing header must 401, got %d", code)
	}
	if code := callSeen(db, "wrong", body); code != http.StatusUnauthorized {
		t.Errorf("I: mismatched header must 401, got %d", code)
	}
	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Errorf("I: valid secret must pass (200), got %d", code)
	}
}

// --- assertion I2: /api/incidents internal-token fail-closed gate -----------------

func TestSensorLifecycle_I2_IncidentInternalGate(t *testing.T) {
	db := newTestDB(t)
	body := `{"siteId":"site-001","deviceId":"d","description":"crisis"}`

	internalToken = ""
	if code := callIncident(db, "anything", body); code != http.StatusUnauthorized {
		t.Errorf("I2: unset server secret must 401, got %d", code)
	}

	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })
	if code := callIncident(db, "", body); code != http.StatusUnauthorized {
		t.Errorf("I2: missing header must 401, got %d", code)
	}
	if code := callIncident(db, "wrong", body); code != http.StatusUnauthorized {
		t.Errorf("I2: mismatched header must 401, got %d", code)
	}
	if code := callIncident(db, "sekret", body); code != http.StatusCreated {
		t.Errorf("I2: valid secret must create (201), got %d", code)
	}
}

// --- assertion J: all lifecycle mutations are admin-only -------------------------

func TestSensorLifecycle_J_AdminOnly(t *testing.T) {
	db := newTestDB(t)

	for _, role := range []string{"user", "temp"} {
		w := httptest.NewRecorder()
		handleCreateDevice(db)(w, roleReq(role, http.MethodPost, "/api/devices", "",
			`{"siteId":"s","deviceId":"d"}`))
		if w.Code != http.StatusForbidden {
			t.Errorf("J: POST as %s expected 403, got %d", role, w.Code)
		}
		dw := httptest.NewRecorder()
		handleDeleteDevice(db)(dw, roleReq(role, http.MethodDelete, "/api/devices/1", "1", ""))
		if dw.Code != http.StatusForbidden {
			t.Errorf("J: DELETE as %s expected 403, got %d", role, dw.Code)
		}
		pw := httptest.NewRecorder()
		handleUpdateDeviceAlias(db)(pw, roleReq(role, http.MethodPatch, "/api/devices/1", "1", `{"alias":"x"}`))
		if pw.Code != http.StatusForbidden {
			t.Errorf("J: PATCH as %s expected 403, got %d", role, pw.Code)
		}
	}

	gw := httptest.NewRecorder()
	handleListDevices(db)(gw, roleReq("user", http.MethodGet, "/api/devices", "", ""))
	if gw.Code != http.StatusOK {
		t.Errorf("J: GET as user expected 200, got %d", gw.Code)
	}
}

// -----------------------------------------------------------------------------
// Mutating, load-bearing assertions — REAL always-on in-process gates.
//
// A prior scout confirmed these are judgeable in-process without a compose stack:
// handlers are factory funcs, admin is injected via the request context (roleReq),
// the internal-token gate reads the internalToken package var, and the WS broadcast
// is a func-var seam (BroadcastDeviceReappeared) whose backfill counterpart
// (sendReappearedSnapshot) drains into a wsClient.send channel. Each test resets any
// package var it mutates in t.Cleanup, and is non-vacuous (a pre-assertion or a
// negative control means it fails if the behavior regresses).
//
// The broadcast callbacks fire SYNCHRONOUSLY inside the handler call (handleSeenDevice
// / upsertIncidentPresence invoke BroadcastDeviceReappeared inline, not in a
// goroutine), and callSeen/callIncident run the handler synchronously — so capture
// slices need no mutex.
// -----------------------------------------------------------------------------

// --- assertion A2: null last_seen aggregates as OFFLINE, sum invariant holds ------
// newHealthMonitor(db) + handleGetHealthSummary. A null-last_seen device (explicitly
// registered, never-seen) must land in OFFLINE (not healthy/abnormal) and must not be
// silently dropped from the counts. Non-vacuity: the sum invariant (healthy+abnormal+
// offline == 미삭제 총수) fails if null falls through all three CASEs, and offline==2
// (null + stale) pins it to the offline bucket. Uses EXPLICIT past timestamps
// (datetime('now','-120 seconds')) rather than sleeps, respecting the 60s default.
func TestSensorLifecycle_A2_NullLastSeenHealthSummary(t *testing.T) {
	db := newTestDB(t)

	// null-last_seen device via the real create handler (offline 대기).
	cw := httptest.NewRecorder()
	handleCreateDevice(db)(cw, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-null"}`))
	if cw.Code != http.StatusCreated {
		t.Fatalf("A2: create null-device expected 201, got %d body=%s", cw.Code, cw.Body.String())
	}

	// 1 healthy (recent, non-active), 1 abnormal (recent, active), 1 stale-offline
	// (>60s past) via direct SQL with explicit timestamps.
	if _, err := db.Exec(`
		INSERT INTO devices (site_id, device_id, alias, last_seen, alert_state) VALUES
		  ('site-001','vs-healthy',  '', datetime('now'),               'none'),
		  ('site-001','vs-abnormal', '', datetime('now'),               'active'),
		  ('site-001','vs-stale',    '', datetime('now','-120 seconds'),'none')
	`); err != nil {
		t.Fatalf("A2: seed devices: %v", err)
	}

	mon := newHealthMonitor(db)
	hw := httptest.NewRecorder()
	handleGetHealthSummary(mon)(hw, roleReq("user", http.MethodGet, "/api/health/summary", "", ""))
	if hw.Code != http.StatusOK {
		t.Fatalf("A2: expected 200 (no 5xx), got %d body=%s", hw.Code, hw.Body.String())
	}
	var resp healthSummaryResponse
	if err := json.Unmarshal(hw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("A2: decode summary: %v", err)
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM devices WHERE deleted_at IS NULL`).Scan(&total); err != nil {
		t.Fatalf("A2: count non-deleted: %v", err)
	}
	if sum := resp.Summary.Healthy + resp.Summary.Abnormal + resp.Summary.Offline; sum != total {
		t.Errorf("A2: sum invariant broken: healthy+abnormal+offline=%d != 미삭제 총수 %d (null last_seen dropped?)", sum, total)
	}
	if resp.Summary.Offline != 2 { // vs-null + vs-stale
		t.Errorf("A2: expected offline==2 (null + stale), got %d", resp.Summary.Offline)
	}
	if resp.Summary.Healthy != 1 {
		t.Errorf("A2: expected healthy==1, got %d", resp.Summary.Healthy)
	}
	if resp.Summary.Abnormal != 1 {
		t.Errorf("A2: expected abnormal==1, got %d", resp.Summary.Abnormal)
	}
	// The null device must appear as an offline exception (consistent with the count).
	foundNull := false
	for _, e := range resp.Exceptions {
		if e.ID == "site-001:vs-null" {
			foundNull = true
			if e.Category != "offline" {
				t.Errorf("A2: null last_seen device must be offline category, got %q", e.Category)
			}
		}
	}
	if !foundNull {
		t.Errorf("A2: null last_seen device missing from exceptions (should surface as offline)")
	}
}

// --- assertion D: unknown (siteId,deviceId) seen auto-registers online ------------
// Non-vacuity: assert the device is ABSENT before the seen.
func TestSensorLifecycle_D_AutoDiscover(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	body := `{"siteId":"site-001","deviceId":"vs-auto"}`

	pre := httptest.NewRecorder()
	handleListDevices(db)(pre, roleReq("admin", http.MethodGet, "/api/devices", "", ""))
	for _, d := range decodeDevices(t, pre.Body.Bytes()) {
		if d.SiteID == "site-001" && d.DeviceID == "vs-auto" {
			t.Fatalf("D: device must be ABSENT before seen (vacuity guard)")
		}
	}

	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("D: seen expected 200, got %d", code)
	}

	lw := httptest.NewRecorder()
	handleListDevices(db)(lw, roleReq("admin", http.MethodGet, "/api/devices", "", ""))
	found := false
	for _, d := range decodeDevices(t, lw.Body.Bytes()) {
		if d.SiteID == "site-001" && d.DeviceID == "vs-auto" {
			found = true
			if d.DeletedAt != nil {
				t.Errorf("D: auto-discovered device must not be deleted, got %v", *d.DeletedAt)
			}
			if d.LastSeen == nil {
				t.Errorf("D: auto-discovered device must have lastSeen (online), got nil")
			}
		}
	}
	if !found {
		t.Fatalf("D: seen did not auto-register the unknown device")
	}
}

// --- assertion E: sticky delete via seen -----------------------------------------
// create → delete(204) → seen same pair → ABSENT from default list; direct SQL shows
// the row with deleted_at NOT NULL. Non-vacuity: a broken sticky (seen revives) puts
// deleted_at back to NULL, which both surfaces the device in the list and trips the
// SQL check.
func TestSensorLifecycle_E_StickyDeleteSeen(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	body := `{"siteId":"site-001","deviceId":"vs-sticky"}`
	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("E: create expected 201, got %d", w.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("E: delete expected 204, got %d", dw.Code)
	}

	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("E: seen expected 200, got %d", code)
	}

	lw := httptest.NewRecorder()
	handleListDevices(db)(lw, roleReq("admin", http.MethodGet, "/api/devices", "", ""))
	for _, d := range decodeDevices(t, lw.Body.Bytes()) {
		if d.DeviceID == "vs-sticky" {
			t.Errorf("E: device must stay ABSENT (sticky) after seen re-signal")
		}
	}

	var deletedAt *string
	if err := db.QueryRow(`SELECT datetime(deleted_at) FROM devices WHERE id=?`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("E: read deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("E: row must remain soft-deleted (deleted_at NOT NULL) after seen")
	}
}

// --- assertion E2: incident-path sticky + exactly ONE reappear --------------------
// create → delete → POST /api/incidents on the deleted pair → 201, device absent,
// and exactly ONE device_reappeared with the matching deviceId. Non-vacuity: a silent
// revive surfaces the device; a missing/duplicate reappear trips the count.
func TestSensorLifecycle_E2_IncidentStickyReappear(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	type cap struct{ siteID, deviceID string }
	var caps []cap
	origB := BroadcastDeviceReappeared
	BroadcastDeviceReappeared = func(s, d string, _ *string) { caps = append(caps, cap{s, d}) }
	t.Cleanup(func() { BroadcastDeviceReappeared = origB })

	cw := httptest.NewRecorder()
	handleCreateDevice(db)(cw, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-e2"}`))
	if cw.Code != http.StatusCreated {
		t.Fatalf("E2: create expected 201, got %d", cw.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("E2: delete expected 204, got %d", dw.Code)
	}

	if code := callIncident(db, "sekret", `{"siteId":"site-001","deviceId":"vs-e2","description":"crisis"}`); code != http.StatusCreated {
		t.Fatalf("E2: incident on deleted pair expected 201, got %d", code)
	}

	lw := httptest.NewRecorder()
	handleListDevices(db)(lw, roleReq("admin", http.MethodGet, "/api/devices", "", ""))
	for _, d := range decodeDevices(t, lw.Body.Bytes()) {
		if d.DeviceID == "vs-e2" {
			t.Errorf("E2: device must stay deleted after incident (sticky)")
		}
	}

	if len(caps) != 1 {
		t.Fatalf("E2: expected exactly 1 device_reappeared, got %d", len(caps))
	}
	if caps[0].deviceID != "vs-e2" || caps[0].siteID != "site-001" {
		t.Errorf("E2: reappear payload mismatch, got %+v", caps[0])
	}
}

// --- assertion F: reappear exactly once via seen ----------------------------------
// Captures the broadcast payload deviceId, asserts exactly 1 per cycle even with
// back-to-back same-second re-signals, the device stays deleted with last_seen
// updated, and the alert re-arms after reactivate→redelete→seen.
func TestSensorLifecycle_F_ReappearOnceSeen(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	var payloads []string // captured deviceIds
	origB := BroadcastDeviceReappeared
	BroadcastDeviceReappeared = func(_, deviceID string, _ *string) { payloads = append(payloads, deviceID) }
	t.Cleanup(func() { BroadcastDeviceReappeared = origB })

	body := `{"siteId":"site-001","deviceId":"vs-f"}`
	cw := httptest.NewRecorder()
	handleCreateDevice(db)(cw, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if cw.Code != http.StatusCreated {
		t.Fatalf("F: create expected 201, got %d", cw.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("F: delete expected 204, got %d", dw.Code)
	}

	// Two back-to-back re-signals (same wall-clock second) → still exactly one.
	callSeen(db, "sekret", body)
	callSeen(db, "sekret", body)

	if len(payloads) != 1 {
		t.Fatalf("F: exactly 1 device_reappeared per cycle, got %d", len(payloads))
	}
	if payloads[0] != "vs-f" {
		t.Errorf("F: reappear payload deviceId mismatch, got %q", payloads[0])
	}

	var deletedAt, lastSeen *string
	if err := db.QueryRow(`SELECT datetime(deleted_at), datetime(last_seen) FROM devices WHERE id=?`, created.ID).
		Scan(&deletedAt, &lastSeen); err != nil {
		t.Fatalf("F: read-back: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("F: device must stay deleted after re-signal (sticky)")
	}
	if lastSeen == nil {
		t.Errorf("F: last_seen must be updated by seen")
	}

	// Reactivate → redelete → seen: a new cycle re-arms and alerts again.
	rw := httptest.NewRecorder()
	handleCreateDevice(db)(rw, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if rw.Code != http.StatusOK {
		t.Fatalf("F: reactivate expected 200, got %d", rw.Code)
	}
	dw2 := httptest.NewRecorder()
	handleDeleteDevice(db)(dw2, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw2.Code != http.StatusNoContent {
		t.Fatalf("F: redelete expected 204, got %d", dw2.Code)
	}
	callSeen(db, "sekret", body)

	if len(payloads) != 2 {
		t.Errorf("F: new cycle after reactivation must alert again (want 2 total), got %d", len(payloads))
	}
	if len(payloads) == 2 && payloads[1] != "vs-f" {
		t.Errorf("F: second-cycle payload deviceId mismatch, got %q", payloads[1])
	}
}

// --- assertion F2: reappear backfill on admin (re)connect -------------------------
// delete → seen (stamps reappear_alerted_at) with the broadcast stubbed to a no-op
// (no observer) → build an admin wsClient and drain sendReappearedSnapshot's frames:
// one device_reappeared for that deviceId. Negative control: a user client gets none.
func TestSensorLifecycle_F2_ReappearBackfill(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	// No observer at reappear time: stub the live broadcast to a no-op.
	origB := BroadcastDeviceReappeared
	BroadcastDeviceReappeared = func(_, _ string, _ *string) {}
	t.Cleanup(func() { BroadcastDeviceReappeared = origB })

	body := `{"siteId":"site-001","deviceId":"vs-f2"}`
	cw := httptest.NewRecorder()
	handleCreateDevice(db)(cw, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if cw.Code != http.StatusCreated {
		t.Fatalf("F2: create expected 201, got %d", cw.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("F2: delete expected 204, got %d", dw.Code)
	}

	callSeen(db, "sekret", body) // stamps reappear_alerted_at; no observer present
	if reappearAt(t, db, created.ID) == nil {
		t.Fatalf("F2: seen after delete must stamp reappear_alerted_at")
	}

	// A newly connecting admin receives the backfill frame.
	admin := &wsClient{role: "admin", db: db, send: make(chan []byte, 64)}
	sendReappearedSnapshot(admin, db)
	if frames := drainWSFrames(admin.send); !containsReappearFor(frames, "site-001", "vs-f2") {
		t.Errorf("F2: newly connected admin must receive a device_reappeared backfill for vs-f2, got %d frame(s)", len(frames))
	}

	// Negative control: a non-admin (user) client receives nothing.
	user := &wsClient{role: "user", db: db, send: make(chan []byte, 64)}
	sendReappearedSnapshot(user, db)
	if uf := drainWSFrames(user.send); len(uf) != 0 {
		t.Errorf("F2: non-admin client must receive no backfill frames, got %d", len(uf))
	}
}

// --- assertion G: presence update, no revive, no new row --------------------------
// create + seed last_seen → seen twice → COUNT==1 for the pair, last_seen advanced,
// deleted_at IS NULL. Non-vacuity: the seed-then-advance check fails if presence isn't
// updated; the deleted_at check fails on an accidental delete.
func TestSensorLifecycle_G_PresenceUpdate(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	body := `{"siteId":"site-001","deviceId":"vs-presence"}`
	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("G: create expected 201, got %d", w.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	const seed = "2020-01-01 00:00:00"
	if _, err := db.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, seed, created.ID); err != nil {
		t.Fatalf("G: seed last_seen: %v", err)
	}

	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("G: first seen expected 200, got %d", code)
	}
	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("G: second seen expected 200, got %d", code)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM devices WHERE site_id='site-001' AND device_id='vs-presence'`).Scan(&count); err != nil {
		t.Fatalf("G: count: %v", err)
	}
	if count != 1 {
		t.Errorf("G: seen must not create a new row (same PK), got count=%d", count)
	}

	var lastSeen, deletedAt *string
	if err := db.QueryRow(`SELECT datetime(last_seen), datetime(deleted_at) FROM devices WHERE id=?`, created.ID).
		Scan(&lastSeen, &deletedAt); err != nil {
		t.Fatalf("G: read-back: %v", err)
	}
	if lastSeen == nil || *lastSeen == seed {
		t.Errorf("G: last_seen must advance from seed, got %v", lastSeen)
	}
	if deletedAt != nil {
		t.Errorf("G: deleted_at must stay NULL for a live device, got %v", *deletedAt)
	}
}

// --- assertion H1: delete ≠ safety stop (runtime) ---------------------------------
// create → delete → POST /api/incidents (internal token) → 201, incident row exists,
// device stays deleted. Non-vacuity: an incident-gating regression would 4xx/skip the
// insert (no row); a silent revive would clear deleted_at.
func TestSensorLifecycle_H1_DeleteNotSafetyStop(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	origB := BroadcastDeviceReappeared
	BroadcastDeviceReappeared = func(_, _ string, _ *string) {}
	t.Cleanup(func() { BroadcastDeviceReappeared = origB })

	cw := httptest.NewRecorder()
	handleCreateDevice(db)(cw, roleReq("admin", http.MethodPost, "/api/devices", "",
		`{"siteId":"site-001","deviceId":"vs-h1"}`))
	if cw.Code != http.StatusCreated {
		t.Fatalf("H1: create expected 201, got %d", cw.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("H1: delete expected 204, got %d", dw.Code)
	}

	if code := callIncident(db, "sekret", `{"siteId":"site-001","deviceId":"vs-h1","description":"crisis"}`); code != http.StatusCreated {
		t.Fatalf("H1: incident for a deleted device must still be created (201), got %d", code)
	}

	var incCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM incidents WHERE site_id='site-001' AND description='crisis'`).Scan(&incCount); err != nil {
		t.Fatalf("H1: count incidents: %v", err)
	}
	if incCount != 1 {
		t.Errorf("H1: expected exactly 1 incident for the deleted device, got %d", incCount)
	}

	var deletedAt *string
	if err := db.QueryRow(`SELECT datetime(deleted_at) FROM devices WHERE id=?`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("H1: read deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("H1: device must remain deleted after incident (sticky)")
	}
}

func TestSensorLifecycle_K_ManagementUI(t *testing.T) {
	t.Skip("K: SKIP (needs-browser, load-bearing) — Playwright: '장치 추가' action + POST /api/devices, sticky delete copy, device_reappeared → one-click reactivate, lastSeen==null offline render")
}

// drainWSFrames non-blockingly drains all buffered frames from a wsClient send
// channel (safeSend is a non-blocking buffered send, so the frames are already
// enqueued by the time sendReappearedSnapshot returns).
func drainWSFrames(ch chan []byte) [][]byte {
	var out [][]byte
	for {
		select {
		case f := <-ch:
			out = append(out, f)
		default:
			return out
		}
	}
}

// containsReappearFor reports whether any frame is a device_reappeared for the given
// (siteId, deviceId).
func containsReappearFor(frames [][]byte, siteID, deviceID string) bool {
	for _, f := range frames {
		var m WSMessage
		if json.Unmarshal(f, &m) != nil {
			continue
		}
		if m.Type != "device_reappeared" {
			continue
		}
		p, ok := m.Payload.(map[string]any)
		if !ok {
			continue
		}
		if p["siteId"] == siteID && p["deviceId"] == deviceID {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func callSeen(db *sql.DB, token, body string) int {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/devices/seen", strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-Internal-Token", token)
	}
	handleSeenDevice(db)(w, r)
	return w.Code
}

func callIncident(db *sql.DB, token, body string) int {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/incidents", strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-Internal-Token", token)
	}
	handleCreateIncident(db)(w, r)
	return w.Code
}

// funcBodyBySig extracts the body of the first top-level func whose declaration
// starts with sig, from its opening `{` to the matching close brace.
func funcBodyBySig(t *testing.T, src, sig string) string {
	t.Helper()
	i := strings.Index(src, sig)
	if i < 0 {
		t.Fatalf("funcBody: %q not found", sig)
	}
	rest := src[i:]
	open := strings.Index(rest, "{")
	if open < 0 {
		t.Fatalf("funcBody: no opening brace after %q", sig)
	}
	depth := 0
	for j := open; j < len(rest); j++ {
		switch rest[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[open : j+1]
			}
		}
	}
	t.Fatalf("funcBody: unbalanced braces for %q", sig)
	return ""
}
