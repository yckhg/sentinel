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

// --- MEDIUM-2 regression: RETURNING-gated reappear guard preserves once-only ------
// Proves the perf optimization (gate the guard behind the presence upsert's
// RETURNING deleted_at) preserves the dedup invariants without a WS observer:
//   - live-device seen (hot path) never writes reappear_alerted_at,
//   - first seen after delete flips reappear_alerted_at NULL→now exactly once,
//   - a second (back-to-back) seen leaves it unchanged (once-only, seconds-agnostic),
//   - reactivation resets it (re-arms the next cycle).

func TestSensorLifecycle_ReappearGuardOnceOnly(t *testing.T) {
	db := newTestDB(t)
	internalToken = "sekret"
	t.Cleanup(func() { internalToken = "" })

	body := `{"siteId":"site-001","deviceId":"vs-guard"}`

	// register + capture id
	w := httptest.NewRecorder()
	handleCreateDevice(db)(w, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d", w.Code)
	}
	var created deviceResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Hot path: seen on a LIVE device must not touch reappear_alerted_at.
	if code := callSeen(db, "sekret", body); code != http.StatusOK {
		t.Fatalf("seen(live) expected 200, got %d", code)
	}
	if ra := reappearAt(t, db, created.ID); ra != nil {
		t.Errorf("hot path: reappear_alerted_at must stay NULL for a live device, got %v", *ra)
	}

	// Soft-delete, then first seen → reappear_alerted_at set once.
	dw := httptest.NewRecorder()
	handleDeleteDevice(db)(dw, roleReq("admin", http.MethodDelete, "/api/devices/"+idStr(created.ID), idStr(created.ID), ""))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete expected 204, got %d", dw.Code)
	}
	callSeen(db, "sekret", body)
	first := reappearAt(t, db, created.ID)
	if first == nil {
		t.Fatalf("first seen after delete must set reappear_alerted_at")
	}
	// Second (back-to-back) seen → unchanged (once-only, independent of clock secs).
	callSeen(db, "sekret", body)
	second := reappearAt(t, db, created.ID)
	if second == nil || *second != *first {
		t.Errorf("second seen must not re-alert: first=%v second=%v", first, second)
	}
	// Device still soft-deleted (sticky) after both re-signals.
	var deletedAt *string
	if err := db.QueryRow(`SELECT datetime(deleted_at) FROM devices WHERE id=?`, created.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("device must remain soft-deleted after re-signals (sticky)")
	}

	// Reactivate → reappear_alerted_at reset (next cycle re-armed).
	rw := httptest.NewRecorder()
	handleCreateDevice(db)(rw, roleReq("admin", http.MethodPost, "/api/devices", "", body))
	if rw.Code != http.StatusOK {
		t.Fatalf("reactivate expected 200, got %d", rw.Code)
	}
	if ra := reappearAt(t, db, created.ID); ra != nil {
		t.Errorf("reactivation must reset reappear_alerted_at, got %v", *ra)
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
	scope := funcBody(t, s, "func handleCreateIncident") + funcBody(t, s, "func upsertIncidentPresence")
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
// SKIP skeletons — mutating (isolated stack + admin JWT + INTERNAL_TOKEN + WS
// observer). Surfaced (not silently green): fixture/observation protocol documented
// as the enabling condition. Run under the isolated-stack harness, not this gate.
// -----------------------------------------------------------------------------

func skipMutating(t *testing.T, id, why string) {
	t.Helper()
	t.Skipf("%s: SKIP (mutating, load-bearing) — %s. Enable under isolated stack (web-backend + hw-gateway + INTERNAL_TOKEN + admin JWT + WS observer).", id, why)
}

func TestSensorLifecycle_A2_NullLastSeenHealthSummary(t *testing.T) {
	skipMutating(t, "A2", "register a null-last_seen device, then assert GET /api/health/summary counts it as offline and the sum invariant holds (healthy+abnormal+offline==미삭제 총수)")
}
func TestSensorLifecycle_D_AutoDiscover(t *testing.T) {
	skipMutating(t, "D", "POST /api/devices/seen for an unknown (siteId,deviceId) auto-registers it online in GET /api/devices")
}
func TestSensorLifecycle_E_StickyDeleteSeen(t *testing.T) {
	skipMutating(t, "E", "delete → seen re-signal → device stays absent from GET /api/devices (sticky)")
}
func TestSensorLifecycle_E2_IncidentStickyReappear(t *testing.T) {
	skipMutating(t, "E2", "delete → POST /api/incidents → incident created, device stays deleted, admin WS receives device_reappeared once")
}
func TestSensorLifecycle_F_ReappearOnceSeen(t *testing.T) {
	skipMutating(t, "F", "deleted device seen → admin WS gets device_reappeared once; back-to-back re-signals (same second) emit no extra; reactivate→redelete→signal alerts again")
}
func TestSensorLifecycle_F2_ReappearBackfill(t *testing.T) {
	skipMutating(t, "F2", "deleted device re-signals while no admin connected → a newly connecting admin receives device_reappeared right after `connected`")
}
func TestSensorLifecycle_G_PresenceUpdate(t *testing.T) {
	skipMutating(t, "G", "seen on an existing live device updates last_seen/online, creates no new row, leaves deleted_at NULL")
}
func TestSensorLifecycle_H1_DeleteNotSafetyStop(t *testing.T) {
	skipMutating(t, "H1", "crisis event for a deleted device's (siteId,deviceId) still creates and forwards the incident (runtime)")
}
func TestSensorLifecycle_K_ManagementUI(t *testing.T) {
	t.Skip("K: SKIP (needs-browser, load-bearing) — Playwright: '장치 추가' action + POST /api/devices, sticky delete copy, device_reappeared → one-click reactivate, lastSeen==null offline render")
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

// funcBody extracts the body of the first top-level func whose declaration starts
// with sig, from its opening `{` to the matching close brace.
func funcBody(t *testing.T, src, sig string) string {
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
