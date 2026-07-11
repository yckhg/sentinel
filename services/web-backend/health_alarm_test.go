package main

import (
	"fmt"
	"testing"
	"time"
)

// TestUnhealthySnapshotForReplay verifies the admin-reconnect replay is
// newest-transition-first, deduped (one per entity), and capped — so a
// just-made-unhealthy target is always included even when the unhealthy set is far
// larger than the cap (assertion O4, robust to a polluted set).
func TestUnhealthySnapshotForReplay(t *testing.T) {
	m := &HealthMonitor{entries: make(map[string]*HealthEntry)}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Many old stale unhealthy sensors (simulate the polluted live set).
	const stale = maxReplaySnapshotFrames * 10
	for i := 0; i < stale; i++ {
		id := fmt.Sprintf("site:stale-%04d", i)
		m.entries[KindSensor+"|"+id] = &HealthEntry{
			Kind: KindSensor, ID: id, Status: StatusUnhealthy,
			Since: base.Add(time.Duration(i) * time.Second), // all well before the fresh one
		}
	}
	// A few healthy entries that must never be replayed.
	m.entries[KindService+"|healthy-svc"] = &HealthEntry{
		Kind: KindService, ID: "healthy-svc", Status: StatusHealthy, Since: base.Add(time.Hour),
	}
	// The freshly-unhealthy target — newest transition of all.
	freshID := "site:FRESH-01"
	m.entries[KindSensor+"|"+freshID] = &HealthEntry{
		Kind: KindSensor, ID: freshID, Status: StatusUnhealthy,
		Since: base.Add(24 * time.Hour),
	}

	out := m.unhealthySnapshotForReplay(maxReplaySnapshotFrames)

	if len(out) != maxReplaySnapshotFrames {
		t.Fatalf("replay len=%d want cap %d", len(out), maxReplaySnapshotFrames)
	}
	// Newest-first: the fresh target must be first.
	if out[0].ID != freshID {
		t.Fatalf("newest-first violated: first=%q want %q", out[0].ID, freshID)
	}
	// Every frame is unhealthy, deduped, and in non-increasing Since order.
	seen := map[string]bool{}
	for i, e := range out {
		if e.Status != StatusUnhealthy {
			t.Fatalf("healthy entity leaked into replay: %v", e)
		}
		if seen[e.Kind+"|"+e.ID] {
			t.Fatalf("duplicate entity in replay: %s", e.ID)
		}
		seen[e.Kind+"|"+e.ID] = true
		if i > 0 && e.Since.After(out[i-1].Since) {
			t.Fatalf("not newest-first at %d", i)
		}
	}
}

// TestHealthAlarmPayload checks the fixed health-sourced system_alarm sub-schema
// (contract 14): envelope {type,message,details} with details
// {entityKind,entityId,status} (assertions O2/O3).
func TestHealthAlarmPayload(t *testing.T) {
	p := healthAlarmPayload(KindSensor, "site1:VOICE-01", StatusUnhealthy)
	if p["type"] != "system_alarm" {
		t.Fatalf("type=%v", p["type"])
	}
	details, ok := p["details"].(map[string]any)
	if !ok {
		t.Fatalf("details wrong type: %T", p["details"])
	}
	if details["entityKind"] != "sensor" || details["entityId"] != "site1:VOICE-01" || details["status"] != "unhealthy" {
		t.Fatalf("details mismatch: %v", details)
	}
}

// TestBroadcastSystemAlarmAdminOnly verifies system_alarm reaches admin WS
// clients only — user/temp receive nothing (assertions O2/O3, contract 14).
func TestBroadcastSystemAlarmAdminOnly(t *testing.T) {
	admin := &wsClient{role: "admin", send: make(chan []byte, 4)}
	user := &wsClient{role: "user", send: make(chan []byte, 4)}
	temp := &wsClient{role: "temp", send: make(chan []byte, 4)}
	for _, c := range []*wsClient{admin, user, temp} {
		hub.register(c)
		defer hub.unregister(c)
	}

	BroadcastSystemAlarm(healthAlarmPayload(KindService, "hw-gateway", StatusUnhealthy))

	if len(admin.send) != 1 {
		t.Fatalf("admin should receive exactly 1 system_alarm, got %d", len(admin.send))
	}
	if len(user.send) != 0 {
		t.Fatalf("user should receive 0 system_alarm, got %d", len(user.send))
	}
	if len(temp.send) != 0 {
		t.Fatalf("temp should receive 0 system_alarm, got %d", len(temp.send))
	}
}
