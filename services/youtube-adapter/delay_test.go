package main

import (
	"testing"
	"time"
)

// TestWaitOrStopWaits verifies the clean-exit throttle actually delays before
// re-resolving (guards #67: no immediate yt-dlp re-invocation on clean EOF).
func TestWaitOrStopWaits(t *testing.T) {
	stopCh := make(chan struct{})
	start := time.Now()
	stopped := waitOrStop(stopCh, 100*time.Millisecond)
	elapsed := time.Since(start)
	if stopped {
		t.Fatal("waitOrStop returned stopped=true without stopCh closed")
	}
	if elapsed < 90*time.Millisecond {
		t.Fatalf("waitOrStop returned after %v; expected to wait ~100ms", elapsed)
	}
}

// TestWaitOrStopEarlyStop verifies a stop request cancels the delay promptly.
func TestWaitOrStopEarlyStop(t *testing.T) {
	stopCh := make(chan struct{})
	close(stopCh)
	start := time.Now()
	stopped := waitOrStop(stopCh, 10*time.Second)
	if !stopped {
		t.Fatal("waitOrStop should return stopped=true when stopCh is closed")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitOrStop blocked %v despite closed stopCh; expected prompt return", elapsed)
	}
}
