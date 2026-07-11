package main

import (
	"strings"
	"testing"
	"time"
)

// TestBuildPlaylistRealDurations guards #78: contiguous segments get EXTINF from
// the real inter-segment gap (not a flat 10.0), and a gap larger than tolerance
// inserts #EXT-X-DISCONTINUITY.
func TestBuildPlaylistRealDurations(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	segs := []playlistSeg{
		{t: base, name: "a.ts"},                      // 10s to next
		{t: base.Add(10 * time.Second), name: "b.ts"}, // 9s to next (short segment)
		{t: base.Add(19 * time.Second), name: "c.ts"}, // big gap after this
		{t: base.Add(5 * time.Minute), name: "d.ts"},  // last (nominal)
	}
	out := buildPlaylist("cam1", segs)

	// Discontinuity must appear exactly once, before d.ts (the post-gap segment).
	if n := strings.Count(out, "#EXT-X-DISCONTINUITY"); n != 1 {
		t.Errorf("expected 1 discontinuity marker, got %d\n%s", n, out)
	}
	dIdx := strings.Index(out, "d.ts")
	discIdx := strings.Index(out, "#EXT-X-DISCONTINUITY")
	if discIdx == -1 || discIdx > dIdx {
		t.Errorf("discontinuity must precede d.ts\n%s", out)
	}

	// EXTINF values must reflect real durations, not a flat 10.0 for every entry.
	if !strings.Contains(out, "#EXTINF:9.000,") {
		t.Errorf("expected a 9.000s EXTINF for the short segment\n%s", out)
	}
	if strings.Count(out, "#EXTINF:10.000,") == 0 {
		t.Errorf("expected at least one 10.000s EXTINF\n%s", out)
	}
	// The pre-gap segment (c.ts) must NOT claim the whole 5-minute gap as its
	// duration; it falls back to nominal 10s.
	if strings.Contains(out, "#EXTINF:281") || strings.Contains(out, "#EXTINF:290") {
		t.Errorf("gap must not be absorbed into a segment's EXTINF\n%s", out)
	}
	if !strings.HasPrefix(out, "#EXTM3U") || !strings.Contains(out, "#EXT-X-ENDLIST") {
		t.Errorf("malformed playlist envelope\n%s", out)
	}
}

// TestBuildPlaylistContiguousNoDiscontinuity ensures a clean contiguous run emits
// no discontinuity markers.
func TestBuildPlaylistContiguousNoDiscontinuity(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	var segs []playlistSeg
	for i := 0; i < 5; i++ {
		segs = append(segs, playlistSeg{t: base.Add(time.Duration(i) * 10 * time.Second), name: "s.ts"})
	}
	out := buildPlaylist("cam1", segs)
	if strings.Contains(out, "#EXT-X-DISCONTINUITY") {
		t.Errorf("contiguous run should have no discontinuity\n%s", out)
	}
}
