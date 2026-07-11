package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestResolveStreamURLCancelledByStop guards #69: closing stopCh must cancel an
// in-flight resolve promptly instead of blocking up to the 30s timeout (and
// leaving the child running). A sleep stands in for a slow yt-dlp.
func TestResolveStreamURLCancelledByStop(t *testing.T) {
	orig := resolveCommand
	defer func() { resolveCommand = orig }()
	resolveCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}

	stopCh := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(stopCh)
	}()

	start := time.Now()
	_, err := resolveStreamURL("https://youtu.be/abc123", stopCh)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when resolve is cancelled by stop")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("resolveStreamURL blocked %v after stop; expected prompt cancellation", elapsed)
	}
}
