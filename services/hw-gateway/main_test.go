package main

import (
	"fmt"
	"strings"
	"testing"
)

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
