package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDispatchDeviceSeenCapsConcurrency locks the fan-out cap (#55): no more
// than deviceSeenMaxConcurrent device-seen sends may be in flight at once, and
// dispatches beyond the cap are dropped (fire-and-forget) instead of spawning
// unbounded goroutines. The test substitutes a blocking sender so the semaphore
// stays saturated, then asserts the overflow calls never started.
func TestDispatchDeviceSeenCapsConcurrency(t *testing.T) {
	// Save & restore package state so the test is self-contained.
	origSender := deviceSeenSender
	origSem := deviceSeenSem
	t.Cleanup(func() {
		deviceSeenSender = origSender
		deviceSeenSem = origSem
	})
	// Fresh semaphore so leftover slots from other paths can't skew the count.
	deviceSeenSem = make(chan struct{}, deviceSeenMaxConcurrent)

	var started int64            // number of sender invocations that began
	var concurrent int64         // current in-flight sends
	var maxConcurrent int64      // high-water mark of in-flight sends
	release := make(chan struct{}) // closed to let blocked senders finish

	deviceSeenSender = func(_, _, _, _ string) {
		atomic.AddInt64(&started, 1)
		cur := atomic.AddInt64(&concurrent, 1)
		for {
			m := atomic.LoadInt64(&maxConcurrent)
			if cur <= m || atomic.CompareAndSwapInt64(&maxConcurrent, m, cur) {
				break
			}
		}
		<-release // block, holding the semaphore slot
		atomic.AddInt64(&concurrent, -1)
	}

	const overflow = 20
	total := deviceSeenMaxConcurrent + overflow
	for i := 0; i < total; i++ {
		dispatchDeviceSeen("http://web", "site", "dev", "none")
	}

	// Give the accepted goroutines time to enter the blocking sender.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&started) >= int64(deviceSeenMaxConcurrent) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Exactly the cap should have started; the overflow must have been dropped.
	if got := atomic.LoadInt64(&started); got != int64(deviceSeenMaxConcurrent) {
		t.Fatalf("started = %d, want %d (overflow should be dropped, not started)",
			got, deviceSeenMaxConcurrent)
	}
	if got := atomic.LoadInt64(&maxConcurrent); got > int64(deviceSeenMaxConcurrent) {
		t.Fatalf("max concurrent = %d, exceeds cap %d", got, deviceSeenMaxConcurrent)
	}

	// Releasing must free slots so subsequent dispatches can run again.
	close(release)
	// Wait for all in-flight sends to drain the semaphore.
	drained := time.Now().Add(2 * time.Second)
	for time.Now().Before(drained) {
		if atomic.LoadInt64(&concurrent) == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if c := atomic.LoadInt64(&concurrent); c != 0 {
		t.Fatalf("in-flight sends did not drain: %d still running", c)
	}
}

// TestDispatchDeviceSeenRunsAndReleases verifies the happy path: a dispatch
// actually invokes the sender and releases its semaphore slot so the cap does
// not leak capacity over time.
func TestDispatchDeviceSeenRunsAndReleases(t *testing.T) {
	origSender := deviceSeenSender
	origSem := deviceSeenSem
	t.Cleanup(func() {
		deviceSeenSender = origSender
		deviceSeenSem = origSem
	})
	deviceSeenSem = make(chan struct{}, deviceSeenMaxConcurrent)

	var wg sync.WaitGroup
	var calls int64
	// Fire well more than the cap sequentially, releasing each immediately.
	const n = deviceSeenMaxConcurrent * 4
	wg.Add(n)
	deviceSeenSender = func(_, _, _, _ string) {
		atomic.AddInt64(&calls, 1)
		wg.Done()
	}
	for i := 0; i < n; i++ {
		// Wait for a slot to free between fires so nothing is dropped.
		for len(deviceSeenSem) > 0 {
			time.Sleep(time.Millisecond)
		}
		dispatchDeviceSeen("http://web", "site", "dev", "none")
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("only %d/%d sends completed — slots leaked", atomic.LoadInt64(&calls), n)
	}
}
