package main

import (
	"testing"
)

// TestStreamStateStopIdempotent guards #70: closing stopCh twice must not panic.
func TestStreamStateStopIdempotent(t *testing.T) {
	s := &streamState{stopCh: make(chan struct{})}
	s.stop()
	s.stop() // must be a no-op, not a double close panic
	select {
	case <-s.stopCh:
	default:
		t.Fatal("stopCh should be closed after stop()")
	}
}

// TestStopAllThenReloadNoPanic reproduces the double-close scenario at the
// manager level: StopAll closes every stream, then a Reload that removes the
// same stream would close it again. Must not panic.
func TestStopAllThenReloadNoPanic(t *testing.T) {
	src := YouTubeSource{ID: "s1", YouTubeURL: "https://youtu.be/abc123", StreamKey: "k1"}
	m := NewStreamManager([]YouTubeSource{src}, "rtmp://x/live", defaultEncodeParams())
	// Insert a stream state directly (no manageStream goroutine / ffmpeg needed).
	m.streams[src.ID] = &streamState{status: "running", stopCh: make(chan struct{})}

	m.StopAll()      // closes s1's stopCh
	m.Reload(nil)    // removes s1 → would close again without the guard
}
