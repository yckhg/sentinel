package main

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestArchiveManager(t *testing.T) *ArchiveManager {
	t.Helper()
	rec := t.TempDir()
	arc := t.TempDir()
	rm := NewRecordingManager("rtmp://x", rec, time.Minute)
	return &ArchiveManager{
		archivesDir:   arc,
		recordingsDir: rec,
		recManager:    rm,
		metadataPath:  filepath.Join(arc, "metadata.json"),
	}
}

// TestCreateArchiveNoDuplicate guards #79: concurrent CreateArchive calls for the
// same incident/stream/time must produce exactly one archive entry (run -race).
func TestCreateArchiveNoDuplicate(t *testing.T) {
	am := newTestArchiveManager(t)
	from := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = am.CreateArchive("inc1", "cam1", from, to)
		}()
	}
	wg.Wait()

	id := "inc1_cam1_" + from.Format("20060102_150405")

	// Exactly one CreateArchive wins the dedup and spawns a background
	// processArchive; wait for it to reach a terminal status so it is not still
	// writing metadata when t.TempDir cleanup runs.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		am.mu.RLock()
		var st string
		for _, a := range am.archives {
			if a.ID == id {
				st = a.Status
			}
		}
		am.mu.RUnlock()
		if st == "failed" || st == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	am.mu.RLock()
	count := 0
	for _, a := range am.archives {
		if a.ID == id {
			count++
		}
	}
	total := len(am.archives)
	am.mu.RUnlock()

	if count != 1 {
		t.Fatalf("expected exactly 1 archive for the id, got %d (total=%d)", count, total)
	}
}
