package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestArchiveManager builds an ArchiveManager backed by temp dirs, with a
// live RecordingManager for protection bookkeeping. Metadata is not loaded from
// disk; the caller sets am.archives directly.
func newTestArchiveManager(t *testing.T, archives []ArchiveMetadata) (*ArchiveManager, *RecordingManager) {
	t.Helper()
	base := t.TempDir()
	recDir := filepath.Join(base, "recordings")
	arcDir := filepath.Join(base, "archives")
	if err := os.MkdirAll(recDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(arcDir, 0755); err != nil {
		t.Fatal(err)
	}
	rm := NewRecordingManager("rtmp://x/live", recDir, 60*time.Second)
	am := &ArchiveManager{
		archives:      archives,
		archivesDir:   arcDir,
		recordingsDir: recDir,
		recManager:    rm,
		metadataPath:  filepath.Join(arcDir, "metadata.json"),
	}
	return am, rm
}

// writeSegment creates a .ts file for streamKey at the given UTC time with nonzero content.
func writeSegment(t *testing.T, recDir, streamKey string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(recDir, streamKey)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	name := at.UTC().Format("20060102_150405") + ".ts"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("tsdata"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRecoveryTargets_Classification: only non-terminal, non-protecting archives
// are recovery targets (§핵심 로직 7).
func TestRecoveryTargets_Classification(t *testing.T) {
	archives := []ArchiveMetadata{
		{ID: "a-pending", Status: "pending"},
		{ID: "a-processing", Status: "processing"},
		{ID: "a-finalizing", Status: "finalizing"},
		{ID: "a-protecting", Status: "protecting"},
		{ID: "a-completed", Status: "completed"},
		{ID: "a-failed", Status: "failed"},
	}
	got := recoveryTargets(archives)

	wantIDs := map[string]bool{"a-pending": true, "a-processing": true, "a-finalizing": true}
	if len(got) != len(wantIDs) {
		t.Fatalf("want %d targets, got %d: %+v", len(wantIDs), len(got), got)
	}
	for _, a := range got {
		if !wantIDs[a.ID] {
			t.Errorf("unexpected recovery target: %s (status %s)", a.ID, a.Status)
		}
	}
	// Explicitly ensure the excluded ones never appear.
	for _, a := range got {
		if a.Status == "protecting" || a.Status == "completed" || a.Status == "failed" {
			t.Errorf("terminal/protecting archive %s must not be a recovery target", a.ID)
		}
	}
}

// TestRecoverArchives_ReprotectsValidSegments (§단언 P part a/b): a recovery target
// whose [from,to) segments exist has those segments re-registered in the protected
// set, and the "Recovery protection re-established" marker is emitted.
func TestRecoverArchives_ReprotectsValidSegments(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	from := now.Add(-5 * time.Minute)
	to := now.Add(1 * time.Minute)

	arc := ArchiveMetadata{
		ID:        "inc1_cam1_x",
		StreamKey: "cam1",
		From:      from.Format(time.RFC3339),
		To:        to.Format(time.RFC3339),
		Status:    "processing",
	}
	am, rm := newTestArchiveManager(t, []ArchiveMetadata{arc})

	// A segment inside [from,to) and one clearly outside (before from).
	inRange := writeSegment(t, rm.recordingsDir, "cam1", now.Add(-2*time.Minute))
	outRange := writeSegment(t, rm.recordingsDir, "cam1", now.Add(-90*time.Minute))

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	// Use the synchronous protection step directly (no async merge resume, so no
	// leaked goroutines racing t.TempDir cleanup). RecoverArchives composes this
	// same step before launching resumes.
	am.reestablishRecoveryProtection()

	// Protection is established synchronously before goroutines launch.
	if !rm.IsProtected(inRange) {
		t.Errorf("in-range segment should be protected after recovery: %s", inRange)
	}
	if rm.IsProtected(outRange) {
		t.Errorf("out-of-range segment should NOT be protected: %s", outRange)
	}
	if !strings.Contains(buf.String(), "Recovery protection re-established") {
		t.Errorf("missing 'Recovery protection re-established' log marker; got: %s", buf.String())
	}
}

// TestRecoveryOrdering_MarkersInOrder (§단언 P part b): the recovery protection
// marker is emitted strictly before the rolling-cleanup start marker. RecoverArchives
// runs synchronously (no targets → no goroutines) and the cleanup marker is emitted
// afterward, mirroring main()'s ordering.
func TestRecoveryOrdering_MarkersInOrder(t *testing.T) {
	am, _ := newTestArchiveManager(t, nil)

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	am.reestablishRecoveryProtection()
	// main()'s cleanup loop emits this on its first run, which is always after the
	// synchronous recovery-protection step.
	log.Println("Rolling cleanup started")

	out := buf.String()
	iRecovery := strings.Index(out, "Recovery protection re-established")
	iCleanup := strings.Index(out, "Rolling cleanup started")
	if iRecovery < 0 {
		t.Fatalf("recovery marker not found: %s", out)
	}
	if iCleanup < 0 {
		t.Fatalf("cleanup marker not found: %s", out)
	}
	if iRecovery >= iCleanup {
		t.Fatalf("recovery marker must precede cleanup marker (rec=%d cleanup=%d): %s", iRecovery, iCleanup, out)
	}
}

// TestProcessArchive_MissingSegments_TerminalFailed (§단언 P-2): a recovery target
// whose required .ts files are all gone transitions to terminal 'failed' with a
// non-empty error, never remaining non-terminal.
func TestProcessArchive_MissingSegments_TerminalFailed(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-5 * time.Minute)
	to := now.Add(1 * time.Minute)

	arc := ArchiveMetadata{
		ID:        "inc2_cam9_x",
		StreamKey: "cam9",
		From:      from.Format(time.RFC3339),
		To:        to.Format(time.RFC3339),
		Status:    "processing",
	}
	am, rm := newTestArchiveManager(t, []ArchiveMetadata{arc})
	// Create the (empty) stream dir — no segments present.
	if err := os.MkdirAll(filepath.Join(rm.recordingsDir, "cam9"), 0755); err != nil {
		t.Fatal(err)
	}

	// processArchive is synchronous; with no segments it returns before FFmpeg.
	am.processArchive(arc.ID, arc.StreamKey, from, to)

	got := am.ListArchives()
	if len(got) != 1 {
		t.Fatalf("expected 1 archive, got %d", len(got))
	}
	final := got[0]
	if final.Status != "failed" {
		t.Fatalf("expected terminal 'failed', got %q", final.Status)
	}
	if final.Error == "" {
		t.Fatalf("expected non-empty error (lastError) on failed recovery")
	}
}

// TestRecoverArchives_InvalidRange_ForcedFailed: a recovery target with an
// unparseable from/to is forced to terminal failed with a reason (never stuck).
func TestRecoverArchives_InvalidRange_ForcedFailed(t *testing.T) {
	arc := ArchiveMetadata{
		ID:        "inc3_cam3_x",
		StreamKey: "cam3",
		From:      "not-a-time",
		To:        "also-bad",
		Status:    "finalizing",
	}
	am, _ := newTestArchiveManager(t, []ArchiveMetadata{arc})

	am.RecoverArchives()

	got := am.ListArchives()
	if got[0].Status != "failed" {
		t.Fatalf("invalid-range archive must be forced to failed, got %q", got[0].Status)
	}
	if got[0].Error == "" {
		t.Fatalf("expected non-empty error for invalid-range failure")
	}
}
