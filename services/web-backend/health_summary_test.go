package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// -----------------------------------------------------------------------------
// Gate for docs/spec/system-status-aggregate.md — assertions A, B, C, F, H, I, K
// plus the exceptions-cap/overflow and "abnormal ≡ alive && alertState active"
// boundary invariants, and the transition-log-absence invariant (E part a).
//
// Contract this gate defines for the implementer (GET /api/health/summary):
//
//	func handleGetHealthSummary(mon *HealthMonitor) http.HandlerFunc
//
// mirrors handleGetHealth(mon) — the monitor already carries both the device DB
// (mon.db) used for the SQL COUNT/GROUP-BY aggregate and the in-memory service
// health (mon.entries, seeded by newHealthMonitor from serviceTargets). The
// response JSON shape (spec "출력 (계약)"):
//
//	{
//	  "summary":  {"healthy": N, "abnormal": M, "offline": K},
//	  "services": [ {"id": "<name>", "status": "healthy"|"unhealthy"}, ... ],  // full fixed set
//	  "exceptions": [ { ...id/displayName, category|status, age, reason... }, ... ], // cap 50
//	  "exceptionsOverflow": <remaining exceptions beyond the cap, else 0>
//	}
//
// These tests decode into map[string]any so they depend only on the JSON
// contract (top-level keys named by the spec) and the handler constructor name —
// not on any internal Go struct. They are RED until the endpoint exists.
// -----------------------------------------------------------------------------

// summaryExceptionsCapDefault is the spec default cap (기본 50건). The gate uses
// this literal so it does not couple to any implementation constant name.
const summaryExceptionsCapDefault = 50

// summaryMonitor builds a HealthMonitor bound to db with every serviceTargets
// entry seeded healthy (exactly what newHealthMonitor does), but does NOT start
// the polling goroutine — the aggregate must derive device categories directly
// from the devices table, not from monitor-populated sensor entries.
func summaryMonitor(db *sql.DB) *HealthMonitor {
	return newHealthMonitor(db)
}

// seenDevice inserts (or is expected to conflict-update) a non-deleted device
// whose last_seen is ageSec seconds in the past, with the given alert_state.
func seenDevice(t *testing.T, db *sql.DB, site, dev, alertState string, ageSec int) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO devices (site_id, device_id, alias, last_seen, alert_state)
		 VALUES (?, ?, '', datetime('now', ?), ?)`,
		site, dev, fmt.Sprintf("-%d seconds", ageSec), alertState,
	)
	if err != nil {
		t.Fatalf("seed device %s:%s: %v", site, dev, err)
	}
}

// setThreshold updates the runtime sensor-alive threshold (contract 11 key),
// simulating a PUT /api/settings without restart.
func setThreshold(t *testing.T, db *sql.DB, sec int) {
	t.Helper()
	if _, err := db.Exec(
		`UPDATE system_settings SET value=? WHERE key='health.sensor_alive_threshold_sec'`,
		fmt.Sprintf("%d", sec),
	); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
}

// getSummary drives GET /api/health/summary and returns (statusCode, decoded).
func getSummary(t *testing.T, mon *HealthMonitor) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/health/summary", nil)
	rec := httptest.NewRecorder()
	handleGetHealthSummary(mon)(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode summary: %v (body=%s)", err, rec.Body.String())
		}
	}
	return rec.Code, m
}

func summaryCounts(t *testing.T, m map[string]any) (healthy, abnormal, offline int) {
	t.Helper()
	s, ok := m["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary missing or wrong type: %#v", m["summary"])
	}
	get := func(k string) int {
		f, ok := s[k].(float64)
		if !ok {
			t.Fatalf("summary.%s missing/not number: %#v", k, s[k])
		}
		return int(f)
	}
	return get("healthy"), get("abnormal"), get("offline")
}

func exceptionsArr(t *testing.T, m map[string]any) []any {
	t.Helper()
	arr, ok := m["exceptions"].([]any)
	if !ok {
		t.Fatalf("exceptions missing or not array: %#v", m["exceptions"])
	}
	return arr
}

func exceptionsOverflow(t *testing.T, m map[string]any) int {
	t.Helper()
	f, ok := m["exceptionsOverflow"].(float64)
	if !ok {
		t.Fatalf("exceptionsOverflow missing/not number: %#v", m["exceptionsOverflow"])
	}
	return int(f)
}

// jsonContainsString reports whether needle appears as any string value anywhere
// in v (used to check whether a device identifier is present in the exceptions
// list without coupling to a specific field name).
func jsonContainsString(v any, needle string) bool {
	switch t := v.(type) {
	case string:
		return t == needle
	case []any:
		for _, e := range t {
			if jsonContainsString(e, needle) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if jsonContainsString(e, needle) {
				return true
			}
		}
	}
	return false
}

// A. 집계 = 예외만 개별, 합 불변식 (핵심).
func TestSummary_A_ExceptionsOnly_SumInvariant(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)

	// 3 healthy, 2 abnormal (alive + active), 1 offline (stale). All ≤ cap.
	for i := 0; i < 3; i++ {
		seenDevice(t, db, "s", fmt.Sprintf("H-%d", i), "none", 5)
	}
	seenDevice(t, db, "s", "AB-0", "active", 5)
	seenDevice(t, db, "s", "AB-1", "active", 5)
	seenDevice(t, db, "s", "OFF-0", "none", 3600) // stale → offline

	code, m := getSummary(t, mon)
	if code != http.StatusOK {
		t.Fatalf("status=%d want 200", code)
	}

	exc := exceptionsArr(t, m)
	if len(exc) != 3 { // D_abnormal + D_offline = 2 + 1
		t.Fatalf("exceptions len=%d want 3 (abnormal+offline only)", len(exc))
	}
	if ov := exceptionsOverflow(t, m); ov != 0 {
		t.Fatalf("exceptionsOverflow=%d want 0", ov)
	}
	h, a, o := summaryCounts(t, m)
	if h != 3 || a != 2 || o != 1 {
		t.Fatalf("counts healthy=%d abnormal=%d offline=%d want 3/2/1", h, a, o)
	}
	if h+a+o != 6 {
		t.Fatalf("sum=%d want 6 (== non-deleted total)", h+a+o)
	}

	// Each abnormal/offline device is represented; no healthy device is listed.
	for _, id := range []string{"s:AB-0", "s:AB-1", "s:OFF-0"} {
		if !jsonContainsString(m["exceptions"], id) {
			t.Fatalf("exception device %q missing from exceptions", id)
		}
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("s:H-%d", i)
		if jsonContainsString(m["exceptions"], id) {
			t.Fatalf("healthy device %q must NOT appear in exceptions", id)
		}
	}
}

// C. 정상 장비는 나열되지 않음 (핵심).
func TestSummary_C_HealthyNeverListed(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	for i := 0; i < 4; i++ {
		seenDevice(t, db, "s", fmt.Sprintf("OK-%d", i), "none", 5)
	}
	_, m := getSummary(t, mon)
	if n := len(exceptionsArr(t, m)); n != 0 {
		t.Fatalf("exceptions len=%d want 0 (all healthy)", n)
	}
	h, a, o := summaryCounts(t, m)
	if h != 4 || a != 0 || o != 0 {
		t.Fatalf("counts=%d/%d/%d want 4/0/0", h, a, o)
	}
}

// B. 스케일 불변 스모크 — 예외 고정, 정상 2 vs 200 → 개별 항목 수 동일.
func TestSummary_B_ScaleInvariant(t *testing.T) {
	build := func(healthyN int) int {
		db := newTestDB(t)
		mon := summaryMonitor(db)
		for i := 0; i < healthyN; i++ {
			seenDevice(t, db, "s", fmt.Sprintf("OK-%d", i), "none", 5)
		}
		// exactly 2 exceptions regardless of healthy count
		seenDevice(t, db, "s", "OFF-0", "none", 3600)
		seenDevice(t, db, "s", "OFF-1", "none", 3600)
		_, m := getSummary(t, mon)
		h, _, _ := summaryCounts(t, m)
		if h != healthyN {
			t.Fatalf("healthy count=%d want %d", h, healthyN)
		}
		return len(exceptionsArr(t, m))
	}
	small := build(2)
	large := build(200)
	if small != large {
		t.Fatalf("scale variance: exceptions len small=%d large=%d (must be equal)", small, large)
	}
	if small != 2 {
		t.Fatalf("exceptions len=%d want 2", small)
	}
}

// abnormal ≡ 생존 && alertState=="active"; offline takes precedence for non-alive.
func TestSummary_AbnormalInvariant(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)

	seenDevice(t, db, "s", "ALIVE-ACTIVE", "active", 5)   // → abnormal
	seenDevice(t, db, "s", "ALIVE-NONE", "none", 5)       // → healthy
	seenDevice(t, db, "s", "STALE-ACTIVE", "active", 3600) // stale ⇒ NOT alive ⇒ offline, not abnormal
	seenDevice(t, db, "s", "STALE-NONE", "none", 3600)     // → offline

	_, m := getSummary(t, mon)
	h, a, o := summaryCounts(t, m)
	if h != 1 || a != 1 || o != 2 {
		t.Fatalf("counts healthy=%d abnormal=%d offline=%d want 1/1/2 "+
			"(abnormal requires alive; stale+active is offline)", h, a, o)
	}
}

// exceptions cap (default 50) + overflow marker.
func TestSummary_ExceptionsCapOverflow(t *testing.T) {
	// 55 offline → list capped at 50, overflow 5.
	db := newTestDB(t)
	mon := summaryMonitor(db)
	total := summaryExceptionsCapDefault + 5
	for i := 0; i < total; i++ {
		seenDevice(t, db, "s", fmt.Sprintf("OFF-%03d", i), "none", 3600)
	}
	_, m := getSummary(t, mon)
	if n := len(exceptionsArr(t, m)); n != summaryExceptionsCapDefault {
		t.Fatalf("exceptions len=%d want cap %d", n, summaryExceptionsCapDefault)
	}
	if ov := exceptionsOverflow(t, m); ov != total-summaryExceptionsCapDefault {
		t.Fatalf("exceptionsOverflow=%d want %d", ov, total-summaryExceptionsCapDefault)
	}
	_, _, o := summaryCounts(t, m)
	if o != total {
		t.Fatalf("offline count=%d want %d (count is exact even when list is capped)", o, total)
	}

	// Exactly at cap → overflow 0.
	db2 := newTestDB(t)
	mon2 := summaryMonitor(db2)
	for i := 0; i < summaryExceptionsCapDefault; i++ {
		seenDevice(t, db2, "s", fmt.Sprintf("OFF-%03d", i), "none", 3600)
	}
	_, m2 := getSummary(t, mon2)
	if n := len(exceptionsArr(t, m2)); n != summaryExceptionsCapDefault {
		t.Fatalf("at-cap exceptions len=%d want %d", n, summaryExceptionsCapDefault)
	}
	if ov := exceptionsOverflow(t, m2); ov != 0 {
		t.Fatalf("at-cap exceptionsOverflow=%d want 0", ov)
	}
}

// F. 서비스 목록 항상 완전 — 기대 서비스 집합(serviceTargets) ⊆ 응답 서비스 집합.
func TestSummary_F_ServicesComplete(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	// take one service down
	mon.entries[KindService+"|streaming"].Status = StatusUnhealthy

	_, m := getSummary(t, mon)
	svcArr, ok := m["services"].([]any)
	if !ok {
		t.Fatalf("services missing/not array: %#v", m["services"])
	}
	got := map[string]string{}
	for _, e := range svcArr {
		obj, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("service item not object: %#v", e)
		}
		id, _ := obj["id"].(string)
		st, _ := obj["status"].(string)
		if id == "" {
			t.Fatalf("service item missing id: %#v", obj)
		}
		if st != StatusHealthy && st != StatusUnhealthy {
			t.Fatalf("service %q status=%q not in {healthy,unhealthy}", id, st)
		}
		got[id] = st
	}
	// expected set ⊆ response set
	for _, tgt := range serviceTargets {
		st, present := got[tgt.Name]
		if !present {
			t.Fatalf("expected service %q missing from response set", tgt.Name)
		}
		if tgt.Name == "streaming" {
			if st != StatusUnhealthy {
				t.Fatalf("streaming status=%q want unhealthy", st)
			}
		} else if st != StatusHealthy {
			t.Fatalf("service %q status=%q want healthy (only streaming is down)", tgt.Name, st)
		}
	}
}

// H. 오프라인 임계 경계 + 런타임 반영.
func TestSummary_H_ThresholdRuntimeReflection(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	// last_seen 40s ago; default threshold 60s → alive → healthy.
	seenDevice(t, db, "s", "EDGE-0", "none", 40)

	_, m := getSummary(t, mon)
	h, _, o := summaryCounts(t, m)
	if h != 1 || o != 0 {
		t.Fatalf("with 60s threshold: healthy=%d offline=%d want 1/0", h, o)
	}
	if n := len(exceptionsArr(t, m)); n != 0 {
		t.Fatalf("healthy edge device must not be in exceptions (len=%d)", n)
	}

	// Lower threshold to 30s at runtime → same device is now stale → offline.
	setThreshold(t, db, 30)
	_, m2 := getSummary(t, mon)
	h2, _, o2 := summaryCounts(t, m2)
	if h2 != 0 || o2 != 1 {
		t.Fatalf("after threshold→30s: healthy=%d offline=%d want 0/1 (runtime re-read)", h2, o2)
	}
	if !jsonContainsString(m2["exceptions"], "s:EDGE-0") {
		t.Fatalf("device must appear in exceptions after crossing lowered threshold")
	}
}

// I. soft-delete 제외 + 복원.
func TestSummary_I_SoftDeleteExcluded(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	seenDevice(t, db, "s", "OFF-0", "none", 3600)
	seenDevice(t, db, "s", "OFF-1", "none", 3600)
	seenDevice(t, db, "s", "OFF-DEL", "none", 3600)

	// soft-delete one offline device
	if _, err := db.Exec(`UPDATE devices SET deleted_at=datetime('now') WHERE site_id='s' AND device_id='OFF-DEL'`); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	_, m := getSummary(t, mon)
	h, a, o := summaryCounts(t, m)
	if h != 0 || a != 0 || o != 2 {
		t.Fatalf("after soft-delete counts=%d/%d/%d want 0/0/2", h, a, o)
	}
	if h+a+o != 2 {
		t.Fatalf("sum=%d want 2 (== non-deleted total)", h+a+o)
	}
	if len(exceptionsArr(t, m)) != 2 {
		t.Fatalf("exceptions len want 2 (deleted excluded)")
	}
	if jsonContainsString(m["exceptions"], "s:OFF-DEL") {
		t.Fatalf("soft-deleted device must not appear in exceptions")
	}

	// restore → counted again
	if _, err := db.Exec(`UPDATE devices SET deleted_at=NULL WHERE site_id='s' AND device_id='OFF-DEL'`); err != nil {
		t.Fatalf("restore: %v", err)
	}
	_, m2 := getSummary(t, mon)
	_, _, o2 := summaryCounts(t, m2)
	if o2 != 3 {
		t.Fatalf("after restore offline=%d want 3", o2)
	}
}

// K. Graceful degradation — 장비 상태 출처(seen 유입) 정지 = last_seen 미갱신 = 전부 stale.
// 집계는 5xx가 아니라 200으로 성공하고, 관측 끊긴 장비는 보수적으로 offline로 집계된다.
// (여기서는 모니터 폴링 루프를 돌리지 않아 seen 유입이 없는 상태를 그대로 재현한다.)
func TestSummary_K_GracefulDegradation(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	const n = 5
	for i := 0; i < n; i++ {
		seenDevice(t, db, "s", fmt.Sprintf("D-%d", i), "none", 3600) // no fresh heartbeat
	}
	code, m := getSummary(t, mon)
	if code != http.StatusOK {
		t.Fatalf("status=%d want 200 (source stopped must not 5xx)", code)
	}
	h, a, o := summaryCounts(t, m)
	if o != n || h != 0 || a != 0 {
		t.Fatalf("counts=%d/%d/%d want 0/0/%d (all conservatively offline)", h, a, o, n)
	}
}

// E (part a). 집계 응답에 전이-로그 항목이 없다 — top-level 및 예외 항목 어디에도
// events/history/transitions 키가 없다.
func TestSummary_E_NoTransitionLog(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)
	seenDevice(t, db, "s", "OFF-0", "none", 3600)
	seenDevice(t, db, "s", "OK-0", "none", 5)

	_, m := getSummary(t, mon)
	banned := []string{"events", "history", "transitions", "transitionLog", "healthEvents"}
	for _, k := range banned {
		if _, ok := m[k]; ok {
			t.Fatalf("aggregate response must not carry transition-log key %q", k)
		}
	}
	for _, e := range exceptionsArr(t, m) {
		if obj, ok := e.(map[string]any); ok {
			for _, k := range banned {
				if _, ok := obj[k]; ok {
					t.Fatalf("exception item must not carry transition-log key %q", k)
				}
			}
		}
	}
}
