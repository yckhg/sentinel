package main

import (
	"sort"
	"testing"
)

// TestServiceTargets_ExpectedSet is the F-5 guard: it pins serviceTargets (the SSOT
// for assertion F's "expected service set") to an explicit expected list, so the
// aggregate's services[] output cannot silently drift from the intended monitored
// set. Without this, assertion F is self-referential — the gate checks
// serviceTargets ⊆ response while the response is built from serviceTargets, so a
// service added to docker-compose.yml but forgotten in serviceTargets (or removed by
// mistake) would pass unnoticed.
//
// SSOT (spec system-status-aggregate / 계약 12): the monitored set = the
// docker-compose.yml services exposing an HTTP /healthz healthcheck, MINUS the
// non-data-path peers {web-backend (self), mosquitto (no HTTP /healthz),
// web-frontend (SPA static server)}. If a monitored backend peer is added/removed in
// docker-compose.yml, update BOTH serviceTargets (health.go) AND this list together.
func TestServiceTargets_ExpectedSet(t *testing.T) {
	expected := []string{
		"cctv-adapter",
		"hw-gateway",
		"notifier",
		"recording",
		"streaming",
		"youtube-adapter",
	}

	got := make([]string, 0, len(serviceTargets))
	for _, tgt := range serviceTargets {
		if tgt.Name == "" {
			t.Fatalf("serviceTargets entry has empty Name: %#v", tgt)
		}
		if tgt.HealthzURL == "" {
			t.Fatalf("serviceTargets entry %q has empty HealthzURL", tgt.Name)
		}
		got = append(got, tgt.Name)
	}
	sort.Strings(got)

	if len(got) != len(expected) {
		t.Fatalf("serviceTargets size=%d %v, want %d %v (compose healthcheck SSOT drift — "+
			"update serviceTargets AND this test in lockstep)", len(got), got, len(expected), expected)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("serviceTargets set=%v, want=%v (mismatch at %q vs %q)", got, expected, got[i], expected[i])
		}
	}

	// Excluded services must NOT sneak into the monitored set.
	for _, tgt := range serviceTargets {
		switch tgt.Name {
		case "web-backend", "mosquitto", "web-frontend":
			t.Fatalf("service %q must be excluded from serviceTargets (non-data-path peer)", tgt.Name)
		}
	}
}
