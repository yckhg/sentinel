package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunBoundedCapsConcurrency locks the worker-pool bound (#62): no matter how
// many send jobs are queued, at most `limit` run at once, and every job still
// runs exactly once. Each job records the live concurrency high-water mark, so
// a pool that spawned a goroutine per job (the old behavior) would blow past the
// limit and fail.
func TestRunBoundedCapsConcurrency(t *testing.T) {
	cases := []struct {
		name  string
		jobs  int
		limit int
	}{
		{"jobs exceed limit", 200, 16},
		{"jobs equal limit", 16, 16},
		{"jobs below limit", 5, 16},
		{"limit one serializes", 20, 1},
		{"no jobs", 0, 16},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var live int64        // currently-running jobs
			var maxLive int64     // high-water mark
			var ran int64         // total jobs that executed

			jobs := make([]func(), tc.jobs)
			for i := range jobs {
				jobs[i] = func() {
					cur := atomic.AddInt64(&live, 1)
					for {
						m := atomic.LoadInt64(&maxLive)
						if cur <= m || atomic.CompareAndSwapInt64(&maxLive, m, cur) {
							break
						}
					}
					// Hold the slot briefly so genuine overlap can build up.
					time.Sleep(5 * time.Millisecond)
					atomic.AddInt64(&ran, 1)
					atomic.AddInt64(&live, -1)
				}
			}

			runBounded(jobs, tc.limit)

			if got := atomic.LoadInt64(&ran); got != int64(tc.jobs) {
				t.Fatalf("ran = %d, want %d (every job must execute once)", got, tc.jobs)
			}
			effLimit := tc.limit
			if tc.jobs < effLimit {
				effLimit = tc.jobs // pool shrinks to job count
			}
			if got := atomic.LoadInt64(&maxLive); got > int64(effLimit) {
				t.Fatalf("max concurrency = %d, exceeds limit %d", got, effLimit)
			}
			if tc.jobs > 0 && atomic.LoadInt64(&maxLive) == 0 {
				t.Fatalf("no concurrency observed — jobs may not have run")
			}
		})
	}
}

// TestRunBoundedContainsPanic verifies the pool cooperates with recover() (#58):
// a panicking job must not crash the process nor kill its worker (which would
// starve the queue), so all the other jobs still complete.
func TestRunBoundedContainsPanic(t *testing.T) {
	const total = 50
	var ran int64
	jobs := make([]func(), 0, total)
	for i := 0; i < total; i++ {
		i := i
		jobs = append(jobs, func() {
			if i%7 == 0 {
				panic("simulated send panic")
			}
			atomic.AddInt64(&ran, 1)
		})
	}
	// Small pool so panicking jobs and survivors share the same workers.
	done := make(chan struct{})
	go func() { runBounded(jobs, 4); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runBounded hung — a panicking job likely killed a pool worker")
	}

	// Every non-panicking job (i not divisible by 7) must have completed.
	want := int64(0)
	for i := 0; i < total; i++ {
		if i%7 != 0 {
			want++
		}
	}
	if got := atomic.LoadInt64(&ran); got != want {
		t.Fatalf("completed jobs = %d, want %d — a panic starved the queue", got, want)
	}
}

// TestRunBoundedBlocksUntilDone confirms runBounded is synchronous: it returns
// only after all jobs finish, so dispatchNotifications keeps the inflightDispatch
// goroutine alive for graceful-shutdown drain (#40).
func TestRunBoundedBlocksUntilDone(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var finished int64
	jobs := []func(){
		func() { time.Sleep(30 * time.Millisecond); atomic.StoreInt64(&finished, 1) },
	}
	go func() {
		runBounded(jobs, maxConcurrentSends)
		if atomic.LoadInt64(&finished) != 1 {
			t.Errorf("runBounded returned before its job finished")
		}
		wg.Done()
	}()
	wg.Wait()
}
