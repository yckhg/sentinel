package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupSegments creates .ts files for streamKey at the given wall-clock times and
// returns their full paths.
func setupSegments(t *testing.T, recDir, streamKey string, times []time.Time) []string {
	t.Helper()
	dir := filepath.Join(recDir, streamKey)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, tm := range times {
		p := filepath.Join(dir, tm.Format("20060102_150405")+".ts")
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// TestUnprotectSegmentsReleasesOriginals guards #76: after an archive's merge the
// originals must be released so rolling cleanup can reclaim them (excluding the
// archive itself, which is still in the list).
func TestUnprotectSegmentsReleasesOriginals(t *testing.T) {
	rec := t.TempDir()
	rm := NewRecordingManager("rtmp://x", rec, time.Minute)
	am := &ArchiveManager{recordingsDir: rec, recManager: rm}

	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.Add(10 * time.Second), base.Add(20 * time.Second)}
	paths := setupSegments(t, rec, "cam1", times)

	from := base.Add(-time.Minute)
	to := base.Add(time.Minute)

	// This archive owns the range; it is still present in the list at unprotect time.
	am.archives = []ArchiveMetadata{{
		ID: "self", StreamKey: "cam1", Status: "completed",
		From: from.Format(time.RFC3339), To: to.Format(time.RFC3339),
	}}

	for _, p := range paths {
		rm.ProtectSegment(p)
	}
	am.unprotectSegments("cam1", from, to, "self")

	for _, p := range paths {
		if rm.IsProtected(p) {
			t.Errorf("segment %s still protected after unprotect (self excluded)", filepath.Base(p))
		}
	}
}

// TestUnprotectSegmentsKeepsShared guards that segments referenced by ANOTHER
// active archive are retained (not stolen out from under it). (#76)
func TestUnprotectSegmentsKeepsShared(t *testing.T) {
	rec := t.TempDir()
	rm := NewRecordingManager("rtmp://x", rec, time.Minute)
	am := &ArchiveManager{recordingsDir: rec, recManager: rm}

	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.Add(10 * time.Second)}
	paths := setupSegments(t, rec, "cam1", times)

	from := base.Add(-time.Minute)
	to := base.Add(time.Minute)

	am.archives = []ArchiveMetadata{
		{ID: "self", StreamKey: "cam1", Status: "completed",
			From: from.Format(time.RFC3339), To: to.Format(time.RFC3339)},
		{ID: "other", StreamKey: "cam1", Status: "protecting",
			From: from.Format(time.RFC3339), To: to.Format(time.RFC3339)},
	}

	for _, p := range paths {
		rm.ProtectSegment(p)
	}
	am.unprotectSegments("cam1", from, to, "self")

	for _, p := range paths {
		if !rm.IsProtected(p) {
			t.Errorf("segment %s was unprotected but is still referenced by 'other'", filepath.Base(p))
		}
	}
}
