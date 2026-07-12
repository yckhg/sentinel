package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestActiveIncidents verifies GET /api/incidents/active (contract 2): only
// status 'open' (resolved excluded; the intermediate 'acknowledged' state was
// removed), occurred_at DESC, each element isomorphic to the crisis_alert payload
// (incidentId + nested site) plus status.
func TestActiveIncidents(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		"INSERT INTO sites (address, manager_name, manager_phone) VALUES ('123 Main', 'Kim', '010-1234-5678')"); err != nil {
		t.Fatalf("seed site: %v", err)
	}
	// Three incidents at distinct times; resolved must be excluded.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO incidents (site_id, description, occurred_at, is_test, status) VALUES
		('s1','older open','2026-07-11 10:00:00',0,'open'),
		('s1','newer open','2026-07-11 11:00:00',0,'open'),
		('s1','resolved one','2026-07-11 12:00:00',0,'resolved')`); err != nil {
		t.Fatalf("seed incidents: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/active", nil)
	rec := httptest.NewRecorder()
	handleActiveIncidents(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}

	if len(arr) != 2 {
		t.Fatalf("expected 2 unresolved incidents, got %d", len(arr))
	}
	// Ordered occurred_at DESC → newer open first.
	if arr[0]["description"] != "newer open" || arr[1]["description"] != "older open" {
		t.Fatalf("wrong order: %v, %v", arr[0]["description"], arr[1]["description"])
	}

	for _, e := range arr {
		// only 'open' appears (resolved excluded; acknowledged no longer exists)
		st, _ := e["status"].(string)
		if st != "open" {
			t.Fatalf("unexpected status %q", st)
		}
		// crisis_alert isomorphism: identifier is incidentId (not id)
		for _, k := range []string{"incidentId", "siteId", "description", "occurredAt", "isTest", "site"} {
			if _, ok := e[k]; !ok {
				t.Fatalf("missing key %q in %v", k, e)
			}
		}
		if _, hasID := e["id"]; hasID {
			t.Fatalf("payload must use incidentId, not id: %v", e)
		}
		site, ok := e["site"].(map[string]any)
		if !ok {
			t.Fatalf("site not nested object: %T", e["site"])
		}
		for _, k := range []string{"address", "managerName", "managerPhone"} {
			if _, ok := site[k]; !ok {
				t.Fatalf("missing site.%s in %v", k, site)
			}
		}
	}
}

// TestActiveIncidentsCapped verifies the backfill returns at most
// activeIncidentsLimit rows (most-recent first) instead of streaming an unbounded
// (polluted) unresolved set, while preserving DESC order and the isomorphic keys.
func TestActiveIncidentsCapped(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO sites (address, manager_name, manager_phone) VALUES ('a','m','010-1234-5678')"); err != nil {
		t.Fatalf("seed site: %v", err)
	}

	// Insert more open incidents than the cap, with strictly increasing occurred_at.
	total := activeIncidentsLimit + 25
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < total; i++ {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05")
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO incidents (site_id, description, occurred_at, is_test, status) VALUES ('s1', ?, ?, 0, 'open')",
			fmt.Sprintf("inc-%d", i), ts); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/incidents/active", nil)
	rec := httptest.NewRecorder()
	handleActiveIncidents(db)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != activeIncidentsLimit {
		t.Fatalf("len=%d want cap %d", len(arr), activeIncidentsLimit)
	}
	// DESC: the newest incident (inc-%d with largest index) must be first.
	if arr[0]["description"] != fmt.Sprintf("inc-%d", total-1) {
		t.Fatalf("not DESC: first=%v", arr[0]["description"])
	}
	// Isomorphic key set preserved under the cap.
	for _, k := range []string{"incidentId", "siteId", "description", "occurredAt", "isTest", "site"} {
		if _, ok := arr[0][k]; !ok {
			t.Fatalf("missing key %q", k)
		}
	}
}
