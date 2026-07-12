package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// -----------------------------------------------------------------------------
// Offline-gated coverage for two load-bearing sub-clauses that previously had only
// live-stack shell scripts (SKIPPED, since production must not be driven):
//
//   - E (part b): drilldown isolation — GET /api/health/events?entity_id=<siteId:deviceId>
//     returns ONLY that device's transitions; a second device's events are excluded.
//   - D (404):    GET /api/devices/{id} → 404 for a missing/soft-deleted device id,
//                 200 for an existing one.
//
// These use the same in-package helpers as health_summary_test.go (newTestDB,
// seenDevice) and do NOT modify any committed gate assertion.
// -----------------------------------------------------------------------------

// insertHealthEvent writes a transition row directly (mirrors HealthMonitor.recordEvent).
func insertHealthEvent(t *testing.T, mon *HealthMonitor, kind, entityID, status, detail string) {
	t.Helper()
	if _, err := mon.db.Exec(
		`INSERT INTO health_events (entity_kind, entity_id, status, detail) VALUES (?, ?, ?, ?)`,
		kind, entityID, status, detail,
	); err != nil {
		t.Fatalf("insert health_event %s/%s→%s: %v", kind, entityID, status, err)
	}
}

// E (part b). 이력 드릴다운 — entity_id 필터는 그 장비의 전이만 반환하고
// 다른 장비의 전이는 하나도 포함하지 않는다 (WHERE entity_id = ? cross-device isolation).
func TestHealthEvents_EntityIDFilter_CrossDeviceIsolation(t *testing.T) {
	db := newTestDB(t)
	mon := summaryMonitor(db)

	const target = "s:DEV-A"
	const other = "s:DEV-B"

	// DEV-A: several online/offline transitions.
	insertHealthEvent(t, mon, KindSensor, target, StatusUnhealthy, "no heartbeat")
	insertHealthEvent(t, mon, KindSensor, target, StatusHealthy, "recovered")
	insertHealthEvent(t, mon, KindSensor, target, StatusUnhealthy, "no heartbeat again")
	// DEV-B: its own transitions that must NOT leak into DEV-A's drilldown.
	insertHealthEvent(t, mon, KindSensor, other, StatusUnhealthy, "no heartbeat")
	insertHealthEvent(t, mon, KindSensor, other, StatusHealthy, "recovered")

	req := httptest.NewRequest(http.MethodGet, "/api/health/events?entity_id="+target, nil)
	rec := httptest.NewRecorder()
	handleListHealthEvents(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var events []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode events: %v (body=%s)", err, rec.Body.String())
	}
	if len(events) != 3 {
		t.Fatalf("entity_id=%s returned %d events, want 3 (only target device's transitions)", target, len(events))
	}
	for _, ev := range events {
		id, _ := ev["entityId"].(string)
		if id != target {
			t.Fatalf("drilldown leaked a foreign transition: entityId=%q, want only %q", id, target)
		}
	}
}

// getDevice drives GET /api/devices/{id} with an explicit path value and returns
// (statusCode, decoded body).
func getDevice(t *testing.T, db *sql.DB, id string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/devices/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	handleGetDevice(db)(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
	}
	return rec.Code, m
}

// D (404). GET /api/devices/{id} → 200 for an existing device, 404 for a missing
// id and for a soft-deleted device (계약 6 규약: 미등록/삭제 → 404).
func TestGetDevice_NotFoundOnMissingOrSoftDeleted(t *testing.T) {
	db := newTestDB(t)

	seenDevice(t, db, "s", "DEV-1", "none", 5)
	var id int64
	if err := db.QueryRow(`SELECT id FROM devices WHERE site_id='s' AND device_id='DEV-1'`).Scan(&id); err != nil {
		t.Fatalf("lookup device id: %v", err)
	}
	existing := strconvI64(id)

	// existing → 200
	code, body := getDevice(t, db, existing)
	if code != http.StatusOK {
		t.Fatalf("existing device: status=%d want 200 (body=%v)", code, body)
	}
	if did, _ := body["deviceId"].(string); did != "DEV-1" {
		t.Fatalf("existing device: deviceId=%q want DEV-1", did)
	}

	// missing id → 404
	if code, _ := getDevice(t, db, "999999"); code != http.StatusNotFound {
		t.Fatalf("missing device: status=%d want 404", code)
	}

	// soft-delete the existing device → now 404
	if _, err := db.Exec(`UPDATE devices SET deleted_at=datetime('now') WHERE id=?`, id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if code, _ := getDevice(t, db, existing); code != http.StatusNotFound {
		t.Fatalf("soft-deleted device: status=%d want 404", code)
	}
}

func strconvI64(v int64) string {
	return strconv.FormatInt(v, 10)
}
