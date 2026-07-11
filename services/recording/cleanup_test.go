package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCleanupZeroByteGrace guards #80: a recently-created 0-byte segment (an
// actively-writing file) must be kept, while a long-stale 0-byte segment is
// reaped.
func TestCleanupZeroByteGrace(t *testing.T) {
	rec := t.TempDir()
	rm := NewRecordingManager("rtmp://x", rec, time.Minute)

	dir := filepath.Join(rec, "cam1")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// Fresh 0-byte segment (just created, mtime ~= now): must survive.
	fresh := filepath.Join(dir, "20250101_120000.ts")
	if err := os.WriteFile(fresh, nil, 0644); err != nil {
		t.Fatal(err)
	}

	// Stale 0-byte segment (mtime well past the grace): must be deleted.
	stale := filepath.Join(dir, "20250101_130000.ts")
	if err := os.WriteFile(stale, nil, 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	rm.CleanupOldSegments(365 * 24 * time.Hour) // huge window: isolate the 0-byte path

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh 0-byte segment was deleted; active recording could be lost: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale 0-byte segment should have been reaped, stat err=%v", err)
	}
}
