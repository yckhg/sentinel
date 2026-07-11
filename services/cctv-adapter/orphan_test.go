package main

import (
	"os/exec"
	"sync"
	"testing"
	"time"
)

// fakeCmd returns a long-lived stand-in process (sleep) instead of ffmpeg so the
// lifecycle/teardown paths can be exercised under the race detector without a
// real ffmpeg binary.
func fakeCmd(_ CameraConfig, _ string) *exec.Cmd {
	return exec.Command("sleep", "30")
}

func waitConnected(t *testing.T, cm *CameraManager, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connected := 0
		for _, s := range cm.GetStatuses() {
			if s.Status == "connected" {
				connected++
			}
		}
		if connected >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d cameras to connect", n)
}

// TestReloadNoRaceNoOrphan drives Reload concurrently with status reads while
// real child processes are running. It guards #66: state.cmd must only be read
// under the lock (run with -race), and Reload/Stop must terminate every child
// (no orphan) without panicking or hanging.
func TestReloadNoRaceNoOrphan(t *testing.T) {
	cams := []CameraConfig{
		{CameraID: "cam-a", Name: "A", RtspURL: "rtsp://a"},
		{CameraID: "cam-b", Name: "B", RtspURL: "rtsp://b"},
	}
	// Large timeout so the output watchdog does not fire during the test.
	cm := NewCameraManager(cams, "rtmp://x/live", 30*time.Second)
	cm.newCmd = fakeCmd
	cm.Start()

	waitConnected(t, cm, 2)

	// Concurrent readers to give the race detector something to observe against
	// the Reload teardown that snapshots state.cmd.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = cm.GetStatuses()
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		cm.Reload(nil) // remove all cameras → tears down both children
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Reload(nil) hung; teardown did not complete")
	}
	close(stop)
	wg.Wait()

	if got := len(cm.GetStatuses()); got != 0 {
		t.Fatalf("expected 0 cameras after Reload(nil), got %d", got)
	}
}

// TestStopTerminates ensures Stop terminates running children and returns.
func TestStopTerminates(t *testing.T) {
	cams := []CameraConfig{{CameraID: "cam-a", Name: "A", RtspURL: "rtsp://a"}}
	cm := NewCameraManager(cams, "rtmp://x/live", 30*time.Second)
	cm.newCmd = fakeCmd
	cm.Start()
	waitConnected(t, cm, 1)

	done := make(chan struct{})
	go func() {
		cm.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop hung")
	}
}
