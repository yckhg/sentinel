package main

import (
	"strings"
	"testing"
	"time"
)

// TestProgressLivenessSeed ensures a fresh tracker is seeded near "now" so a
// recorder that has not yet emitted any progress is not immediately considered
// stalled.
func TestProgressLivenessSeed(t *testing.T) {
	live := newProgressLiveness()
	if delta := time.Since(live.last()); delta < -2*time.Second || delta > 2*time.Second {
		t.Fatalf("newProgressLiveness must seed liveness at ~now, got %v ago", delta)
	}
}

// TestConsumeProgressMarksLiveness feeds a realistic ffmpeg -progress block and
// verifies each line advances the liveness timestamp (no real ffmpeg needed).
func TestConsumeProgressMarksLiveness(t *testing.T) {
	live := newProgressLiveness()
	// Seed far in the past so we can detect that progress advanced it.
	live.mark(time.Now().Add(-1 * time.Hour))

	input := strings.Join([]string{
		"frame=12",
		"fps=25.00",
		"bitrate=1024.0kbits/s",
		"total_size=65536",
		"out_time_us=480000",
		"out_time=00:00:00.480000",
		"speed=1.00x",
		"progress=continue",
	}, "\n") + "\n"

	fixed := time.Unix(1_700_000_000, 0)
	consumeProgress(strings.NewReader(input), live, func() time.Time { return fixed })

	if !live.last().Equal(fixed) {
		t.Fatalf("expected liveness marked at %v after progress lines, got %v", fixed, live.last())
	}
}

// TestConsumeProgressNoLinesLeavesSeed ensures EOF with no data does not advance
// liveness (an empty progress stream is not a liveness signal).
func TestConsumeProgressNoLinesLeavesSeed(t *testing.T) {
	live := newProgressLiveness()
	seed := live.last()
	consumeProgress(strings.NewReader(""), live, func() time.Time { return seed.Add(time.Hour) })
	if !live.last().Equal(seed) {
		t.Fatalf("empty progress must not advance liveness: seed %v got %v", seed, live.last())
	}
}

// TestConsumeProgressSkipsBlankLines ensures blank lines (the delimiters ffmpeg
// may emit) are not counted as liveness signals.
func TestConsumeProgressSkipsBlankLines(t *testing.T) {
	live := newProgressLiveness()
	seed := live.last()
	consumeProgress(strings.NewReader("\n\n\n"), live, func() time.Time { return seed.Add(time.Hour) })
	if !live.last().Equal(seed) {
		t.Fatalf("blank-only progress must not advance liveness: seed %v got %v", seed, live.last())
	}
}

// TestIsStalled covers the timeout boundary used by the watchdog. Recording's
// default FFMPEG_TIMEOUT is 60s (cctv-adapter uses 30s); the boundary logic is
// timeout-agnostic, so 60s is used here to mirror recording's default.
func TestIsStalled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	timeout := 60 * time.Second
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"fresh progress within timeout", now.Add(-5 * time.Second), false},
		{"exactly at timeout is not stalled", now.Add(-60 * time.Second), false},
		{"just past timeout is stalled", now.Add(-61 * time.Second), true},
	}
	for _, c := range cases {
		if got := isStalled(c.last, now, timeout); got != c.want {
			t.Errorf("%s: isStalled=%v want %v", c.name, got, c.want)
		}
	}
}

// TestStalledAfterProgressFreeze documents the hang-detection regression guard:
// once -progress updates stop arriving (as when the process is SIGSTOPped/
// frozen), the watchdog considers the recorder stalled after FFMPEG_TIMEOUT — so
// the hang detection that the #68 false-positive fix must NOT break still fires
// (spec §C hang path: unpublish/freeze → reconnecting/disconnected within
// 1.5×FFMPEG_TIMEOUT + 15s).
func TestStalledAfterProgressFreeze(t *testing.T) {
	live := newProgressLiveness()
	frozenAt := time.Unix(1_700_000_000, 0)
	live.mark(frozenAt) // last progress observed just before the freeze
	timeout := 60 * time.Second

	// Before the timeout elapses: not yet stalled (do not kill a slow-but-alive
	// recorder).
	if isStalled(live.last(), frozenAt.Add(59*time.Second), timeout) {
		t.Fatal("must not flag a stall before FFMPEG_TIMEOUT elapses")
	}
	// After the timeout with no further progress (frozen): stalled → the watchdog
	// will terminate and replace the process.
	if !isStalled(live.last(), frozenAt.Add(61*time.Second), timeout) {
		t.Fatal("must flag a stall once FFMPEG_TIMEOUT elapses with no progress (§C hang path)")
	}
}
