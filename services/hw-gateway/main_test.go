package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- #51 healthz: SUBACK/health-state computation (isHealthy) ---

func TestIsHealthy(t *testing.T) {
	allGranted := map[string]byte{
		topicAlert:     2,
		topicHeartbeat: 1,
		topicResolved:  1,
		topicCandidate: 0,
	}
	tests := []struct {
		name      string
		connected bool
		grants    map[string]byte
		want      bool
	}{
		{"connected + all 3 required granted", true, allGranted, true},
		{"connected, candidate missing (excluded) still healthy", true, map[string]byte{
			topicAlert: 2, topicHeartbeat: 1, topicResolved: 1,
		}, true},
		{"not connected", false, allGranted, false},
		{"connected but alert missing", true, map[string]byte{
			topicHeartbeat: 1, topicResolved: 1,
		}, false},
		{"connected but heartbeat SUBACK failure 0x80", true, map[string]byte{
			topicAlert: 2, topicHeartbeat: subackFailure, topicResolved: 1,
		}, false},
		{"connected but resolved missing", true, map[string]byte{
			topicAlert: 2, topicHeartbeat: 1,
		}, false},
		{"connected, only candidate granted (all required missing)", true, map[string]byte{
			topicCandidate: 0,
		}, false},
		{"connected, no grants", true, map[string]byte{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHealthy(tt.connected, tt.grants); got != tt.want {
				t.Errorf("isHealthy(%v, %v) = %v, want %v", tt.connected, tt.grants, got, tt.want)
			}
		})
	}
}

// candidate SUBACK failure must NOT degrade health (it is excluded).
func TestIsHealthyCandidateFailureIgnored(t *testing.T) {
	grants := map[string]byte{
		topicAlert: 2, topicHeartbeat: 1, topicResolved: 1,
		topicCandidate: subackFailure,
	}
	if !isHealthy(true, grants) {
		t.Errorf("candidate SUBACK failure must not degrade health")
	}
}

func TestHealthStateSetConnectedClearsGrants(t *testing.T) {
	h := newHealthState()
	h.setConnected(true)
	h.setGrant(topicAlert, 2)
	h.setGrant(topicHeartbeat, 1)
	h.setGrant(topicResolved, 1)
	if c, g := h.snapshot(); !isHealthy(c, g) {
		t.Fatalf("expected healthy after grants recorded")
	}
	// A disconnect must clear all grants and report degraded.
	h.setConnected(false)
	c, g := h.snapshot()
	if isHealthy(c, g) {
		t.Errorf("expected degraded after disconnect")
	}
	if len(g) != 0 {
		t.Errorf("expected grants cleared on disconnect, got %v", g)
	}
}

// --- #53 startup invariant: evictTTL must exceed heartbeatTimeout ---

func TestResolveEvictTTL(t *testing.T) {
	def := 86400 * time.Second
	tests := []struct {
		name       string
		ttl        time.Duration
		hbTimeout  time.Duration
		wantTTL    time.Duration
		wantForced bool
	}{
		{"valid ttl > hb", 86400 * time.Second, 30 * time.Second, 86400 * time.Second, false},
		{"valid small ttl > hb (R config)", 10 * time.Second, 3 * time.Second, 10 * time.Second, false},
		{"violation ttl == hb", 30 * time.Second, 30 * time.Second, def, true},
		{"violation ttl < hb (T config)", 10 * time.Second, 30 * time.Second, def, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, forced := resolveEvictTTL(tt.ttl, tt.hbTimeout, def)
			if got != tt.wantTTL || forced != tt.wantForced {
				t.Errorf("resolveEvictTTL(%v,%v) = (%v,%v), want (%v,%v)",
					tt.ttl, tt.hbTimeout, got, forced, tt.wantTTL, tt.wantForced)
			}
		})
	}
}

func TestParsePositiveIntEnv(t *testing.T) {
	tests := []struct {
		raw      string
		fallback int
		want     int
	}{
		{"", 1000, 1000},
		{"5", 1000, 5},
		{"0", 1000, 1000},
		{"-3", 1000, 1000},
		{"abc", 1000, 1000},
		{"86400", 86400, 86400},
	}
	for _, tt := range tests {
		if got := parsePositiveIntEnv(tt.raw, tt.fallback); got != tt.want {
			t.Errorf("parsePositiveIntEnv(%q,%d) = %d, want %d", tt.raw, tt.fallback, got, tt.want)
		}
	}
}

// --- #53 LRU cap eviction ---

func dev(id string, seen time.Time) *DeviceStatus {
	return &DeviceStatus{DeviceID: id, SiteID: "site1", Alive: true, lastSeen: seen}
}

func TestLruVictim(t *testing.T) {
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	devices := map[string]*DeviceStatus{
		"site1:A": dev("A", base.Add(30*time.Second)),
		"site1:B": dev("B", base.Add(10*time.Second)), // oldest
		"site1:C": dev("C", base.Add(20*time.Second)),
	}
	if got := lruVictim(devices); got != "site1:B" {
		t.Errorf("lruVictim = %q, want site1:B (least-recently-seen)", got)
	}
	if got := lruVictim(map[string]*DeviceStatus{}); got != "" {
		t.Errorf("lruVictim(empty) = %q, want empty string", got)
	}
}

// Simulate the cap-eviction loop used in handleHeartbeat: adding beyond the cap
// keeps length <= max and removes least-recently-seen first.
func TestLruCapKeepsMostRecent(t *testing.T) {
	max := 5
	base := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)
	devices := map[string]*DeviceStatus{}

	addKey := func(key string, seen time.Time) {
		if _, exists := devices[key]; !exists {
			for len(devices) >= max {
				victim := lruVictim(devices)
				if victim == "" {
					break
				}
				delete(devices, victim)
			}
		}
		devices[key] = dev(key, seen)
	}

	// Add 20 distinct devices with increasing lastSeen.
	for i := 0; i < 20; i++ {
		addKey(string(rune('A'+i)), base.Add(time.Duration(i)*time.Second))
	}

	if len(devices) > max {
		t.Fatalf("store length %d exceeds cap %d", len(devices), max)
	}
	// The 5 most-recently-seen (indices 15..19 → 'P'..'T') must survive.
	for i := 15; i < 20; i++ {
		key := string(rune('A' + i))
		if _, ok := devices[key]; !ok {
			t.Errorf("expected most-recent device %q retained", key)
		}
	}
	// An early (least-recently-seen) device must be gone.
	if _, ok := devices["A"]; ok {
		t.Errorf("expected least-recently-seen device 'A' evicted")
	}
}

// --- #53 TTL eviction vs dead-marking isolation ---

func TestTtlExpiredKeys(t *testing.T) {
	now := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	ttl := 10 * time.Second
	devices := map[string]*DeviceStatus{
		"site1:fresh":   dev("fresh", now.Add(-5*time.Second)),  // within TTL
		"site1:expired": dev("expired", now.Add(-20*time.Second)), // past TTL
		"site1:edge":    dev("edge", now.Add(-10*time.Second)),   // exactly TTL (not > ttl)
	}
	expired := ttlExpiredKeys(devices, now, ttl)
	if len(expired) != 1 || expired[0] != "site1:expired" {
		t.Errorf("ttlExpiredKeys = %v, want [site1:expired]", expired)
	}
}

// C vs R isolation: with a large TTL (default), a device unseen past the dead
// timeout is NOT TTL-removed — it would only be dead-marked (retained). With a
// small TTL > timeout, the same unseen device IS removed.
func TestTtlIsolationFromDeadMarking(t *testing.T) {
	now := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	// Device unseen for 40s. heartbeatTimeout=30 → dead-marked.
	devices := map[string]*DeviceStatus{
		"site1:X": dev("X", now.Add(-40*time.Second)),
	}
	// C config: TTL = 86400 >> 30. Not TTL-expired → stays (only dead-marked).
	if got := ttlExpiredKeys(devices, now, 86400*time.Second); len(got) != 0 {
		t.Errorf("with large TTL, device must be retained (dead-marked, not removed); got %v", got)
	}
	// R config: TTL = 10 (> hbTimeout 3). 40s > 10s → removed.
	if got := ttlExpiredKeys(devices, now, 10*time.Second); len(got) != 1 {
		t.Errorf("with small TTL, unseen device must be removed; got %v", got)
	}
}

// TestTruncatePayloadShort verifies a payload shorter than the cap is returned
// verbatim with no truncation marker.
func TestTruncatePayloadShort(t *testing.T) {
	in := []byte(`{"deviceId":"dev-1","siteId":"site-1"}`)
	got := truncatePayload(in)
	if got != string(in) {
		t.Fatalf("short payload should be returned verbatim: got %q, want %q", got, string(in))
	}
	if strings.Contains(got, "[truncated,") {
		t.Fatalf("short payload should not contain truncation marker: got %q", got)
	}
}

// TestTruncatePayloadAtCap verifies a payload exactly at the cap (256 bytes) is
// returned verbatim with no truncation marker.
func TestTruncatePayloadAtCap(t *testing.T) {
	in := []byte(strings.Repeat("a", maxLoggedPayloadBytes))
	if len(in) != 256 {
		t.Fatalf("test setup: expected 256-byte input, got %d", len(in))
	}
	got := truncatePayload(in)
	if got != string(in) {
		t.Fatalf("at-cap payload should be returned verbatim: got len %d, want len %d", len(got), len(in))
	}
	if strings.Contains(got, "[truncated,") {
		t.Fatalf("at-cap payload should not contain truncation marker: got %q", got)
	}
}

// TestTruncatePayloadLong verifies a payload longer than the cap is truncated:
// result begins with the first 256 bytes, contains the truncation marker with
// the correct total length, and is not the full payload.
func TestTruncatePayloadLong(t *testing.T) {
	total := maxLoggedPayloadBytes + 100 // 356 bytes
	in := []byte(strings.Repeat("b", total))
	got := truncatePayload(in)

	prefix := string(in[:maxLoggedPayloadBytes])
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("truncated result should begin with the first %d bytes", maxLoggedPayloadBytes)
	}
	if !strings.Contains(got, "[truncated,") {
		t.Fatalf("truncated result should contain truncation marker: got %q", got)
	}
	wantMarker := fmt.Sprintf("[truncated, %d bytes total]", total)
	if !strings.Contains(got, wantMarker) {
		t.Fatalf("truncated result should report correct total length %d: got %q", total, got)
	}
	if got == string(in) {
		t.Fatalf("truncated result must not equal the full payload")
	}
}
