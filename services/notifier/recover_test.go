package main

import (
	"sync"
	"testing"
	"time"
)

// TestRecoverGoroutinePreventsCrash proves the recover() guard (#58) actually
// contains a panic: each of these background goroutine bodies panics in a way
// that would otherwise tear down the whole notifier process. If recoverGoroutine
// did NOT recover, the panic would crash the test binary (a hard failure), so a
// clean completion is genuine evidence the guard works.
func TestRecoverGoroutinePreventsCrash(t *testing.T) {
	cases := []struct {
		name    string
		explode func()
	}{
		{"nil map write", func() { var m map[string]int; m["x"] = 1 }},
		{"nil pointer deref", func() { var p *int; _ = *p }},
		{"explicit panic", func() { panic("boom") }},
		{"index out of range", func() { s := []int{}; _ = s[5] }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				// Ordered like the real dispatch goroutines: recover runs during
				// unwind (LIFO), then close(done) signals normal completion.
				defer close(done)
				defer recoverGoroutine(tc.name)
				tc.explode()
				t.Errorf("explode() returned without panicking — test is not exercising recover")
			}()
			select {
			case <-done:
				// survived: the panic was recovered
			case <-time.After(2 * time.Second):
				t.Fatal("goroutine did not complete — panic was not recovered")
			}
		})
	}
}

// TestRecoverGoroutineCooperatesWithDrain verifies the guard cooperates with the
// inflightDispatch WaitGroup drain (#40): even when the dispatch goroutine
// panics, its deferred WaitGroup.Done() must still run so graceful shutdown does
// not hang waiting for a goroutine that already died.
func TestRecoverGoroutineCooperatesWithDrain(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		// Same defer stack shape as handleNotify's dispatch goroutine:
		// Done() registered first (runs last), recover registered second.
		defer wg.Done()
		defer recoverGoroutine("drain cooperation")
		panic("simulated dispatch panic")
	}()

	if !waitTimeout(&wg, 2*time.Second) {
		t.Fatal("WaitGroup.Done() did not run after a recovered panic — drain would hang")
	}
}
