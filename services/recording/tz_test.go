package main

import "testing"

// TestBuildRecordCmdForcesUTC guards #77: the recorder ffmpeg child must run with
// TZ=UTC so -strftime segment filenames match the UTC parsing/query path
// regardless of the container's timezone.
func TestBuildRecordCmdForcesUTC(t *testing.T) {
	cmd := buildRecordCmd("rtmp://x/live/cam1", "/recordings/cam1/%Y%m%d_%H%M%S.ts")

	found := false
	for _, e := range cmd.Env {
		if e == "TZ=UTC" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recorder ffmpeg command must set TZ=UTC; env=%v", cmd.Env)
	}
}
