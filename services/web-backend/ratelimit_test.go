package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRateLimiterNoBoundaryDoubleAllow proves the window-boundary reset is
// atomic (#87). Many goroutines hammer a single fresh IP concurrently. The very
// first call finds windowEnd==0 (expired) and must be the ONLY one to run the
// reset branch (count=1). With the previous split atomics, several goroutines
// could each observe now>=windowEnd and independently reset count to 1, letting
// a burst past the limit. Under a mutex, allows must equal exactly maxRequests.
//
// Run with -race to surface any residual unsynchronized access.
func TestRateLimiterNoBoundaryDoubleAllow(t *testing.T) {
	const maxRequests = 5
	// A very long window guarantees a single window for the whole test, so the
	// count of allows is deterministic regardless of scheduling/timing.
	rl := newRateLimiter(maxRequests, time.Hour)

	const goroutines = 64
	const perGoroutine = 200

	var allowed int64
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait() // maximise contention on the window boundary
			for j := 0; j < perGoroutine; j++ {
				if rl.allow("10.0.0.1") {
					atomic.AddInt64(&allowed, 1)
				}
			}
		}()
	}
	start.Done()
	done.Wait()

	if allowed != maxRequests {
		t.Errorf("allowed=%d, want exactly %d (double-allow / TOCTOU at window reset)", allowed, maxRequests)
	}

	// A different IP is independent and still bounded.
	var allowed2 int64
	for j := 0; j < 100; j++ {
		if rl.allow("10.0.0.2") {
			allowed2++
		}
	}
	if allowed2 != maxRequests {
		t.Errorf("second IP allowed=%d, want %d", allowed2, maxRequests)
	}
}

// TestRateLimiterWindowExpiryResets confirms a fresh window is granted once the
// previous one elapses (the reset branch still works after the atomicity fix).
// The limiter tracks windows at unix-second granularity, so the window must be
// at least one second.
func TestRateLimiterWindowExpiryResets(t *testing.T) {
	rl := newRateLimiter(2, time.Second)
	if !rl.allow("a") || !rl.allow("a") {
		t.Fatal("first two requests should be allowed")
	}
	if rl.allow("a") {
		t.Fatal("third request in the same window should be denied")
	}
	time.Sleep(1300 * time.Millisecond)
	if !rl.allow("a") {
		t.Error("request after window expiry should be allowed")
	}
}
