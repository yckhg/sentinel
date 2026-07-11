package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestTerminateProcessesParallel guards the reload teardown fix (#44): SIGTERM is
// sent to every process up front and the grace period is awaited exactly once, so
// stopping N processes must NOT take N× the grace period (the old serial bug).
func TestTerminateProcessesParallel(t *testing.T) {
	const n = 4
	const grace = 300 * time.Millisecond

	cmds := make([]*exec.Cmd, 0, n)
	procs := make([]*os.Process, 0, n)
	for i := 0; i < n; i++ {
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start sleep %d: %v", i, err)
		}
		cmds = append(cmds, cmd)
		procs = append(procs, cmd.Process)
	}

	start := time.Now()
	terminateProcesses(procs, grace)
	elapsed := time.Since(start)

	// Reap children so they do not linger as zombies.
	for _, c := range cmds {
		_ = c.Wait()
	}

	if elapsed >= 2*grace {
		t.Errorf("terminateProcesses took %v for %d processes; expected ~%v (single grace), not serial %v",
			elapsed, n, grace, time.Duration(n)*grace)
	}
}

// TestTerminateProcessesEmpty ensures an empty batch returns immediately without
// sleeping the grace period.
func TestTerminateProcessesEmpty(t *testing.T) {
	start := time.Now()
	terminateProcesses(nil, time.Second)
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Errorf("terminateProcesses(nil) slept %v; expected immediate return", elapsed)
	}
}
