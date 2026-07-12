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

// TestDeleteArchiveUnprotectsOwnSegments guards #76 through the real DeleteArchive
// caller: deleting an archive must pass its own ID as excludeArchiveID so its
// exclusively-owned, now-unreferenced segments become eligible for rolling cleanup
// immediately (not pinned until a container restart).
func TestDeleteArchiveUnprotectsOwnSegments(t *testing.T) {
	am := newTestArchiveManager(t)
	rm := am.recManager
	rec := am.recordingsDir

	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	times := []time.Time{base, base.Add(10 * time.Second), base.Add(20 * time.Second)}
	paths := setupSegments(t, rec, "cam1", times)

	from := base.Add(-time.Minute)
	to := base.Add(time.Minute)

	am.archives = []ArchiveMetadata{{
		ID: "arc-A", StreamKey: "cam1", Status: "completed",
		From: from.Format(time.RFC3339), To: to.Format(time.RFC3339),
	}}
	for _, p := range paths {
		rm.ProtectSegment(p)
	}

	if err := am.DeleteArchive("arc-A"); err != nil {
		t.Fatalf("DeleteArchive: %v", err)
	}

	for _, p := range paths {
		if rm.IsProtected(p) {
			t.Errorf("segment %s still protected after DeleteArchive (leaked against rolling cleanup)", filepath.Base(p))
		}
	}
}

// TestDeleteArchiveKeepsOverlappingSegments guards that deleting one archive does
// not release segments still covered by ANOTHER overlapping archive. (#76)
func TestDeleteArchiveKeepsOverlappingSegments(t *testing.T) {
	am := newTestArchiveManager(t)
	rm := am.recManager
	rec := am.recordingsDir

	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	// soloA is owned only by A; shared falls inside BOTH A's and B's ranges, so
	// deleting A must scan it yet retain it because B still references it.
	soloA := base
	shared := base.Add(30 * time.Second)
	paths := setupSegments(t, rec, "cam1", []time.Time{soloA, shared})
	soloPath, sharedPaths := paths[0], paths[1:]

	// A: [base-1m, base+40s] covers soloA and shared.
	aFrom := base.Add(-time.Minute)
	aTo := base.Add(40 * time.Second)
	// B: [base+20s, base+1m] covers shared only (not soloA).
	bFrom := base.Add(20 * time.Second)
	bTo := base.Add(time.Minute)

	am.archives = []ArchiveMetadata{
		{ID: "arc-A", StreamKey: "cam1", Status: "completed",
			From: aFrom.Format(time.RFC3339), To: aTo.Format(time.RFC3339)},
		{ID: "arc-B", StreamKey: "cam1", Status: "completed",
			From: bFrom.Format(time.RFC3339), To: bTo.Format(time.RFC3339)},
	}
	for _, p := range paths {
		rm.ProtectSegment(p)
	}

	if err := am.DeleteArchive("arc-A"); err != nil {
		t.Fatalf("DeleteArchive: %v", err)
	}

	// A's exclusive segment is released.
	if rm.IsProtected(soloPath) {
		t.Errorf("segment %s (exclusive to deleted A) still protected", filepath.Base(soloPath))
	}
	// Segments still covered by B remain protected.
	for _, p := range sharedPaths {
		if !rm.IsProtected(p) {
			t.Errorf("segment %s still covered by archive B was wrongly unprotected", filepath.Base(p))
		}
	}
}
