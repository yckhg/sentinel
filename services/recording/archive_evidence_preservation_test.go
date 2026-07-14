package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Assertion B2 (camera-change-propagation.md, ~line 88) — 삭제 아카이브 증거 보존.
//
// Invariant: a camera's protected+finalized (Status=="completed") recording
// archive — the merged MP4 + its metadata.json entry — MUST REMAIN on disk and
// in the archive list after that camera is deleted and the recording reconcile
// stops its recorder. The reconcile only performs recorder teardown
// (stopCh · SIGTERM · states delete) and never touches archiveManager /
// metadata.json / archive files (recording.md §핵심 로직 6 "증거 보존 불변식").
//
// This is the Tier-1, in-process realization of the load-bearing SKIP kept as a
// skeleton in services/web-backend/camera_change_propagation_test.go
// (TestDeleteCamera_ArchiveEvidencePreserved_B2). It runs inside the recording
// service's ffmpeg-base image, so ffmpeg is on PATH for fixture generation and
// the finalize merge.
// ---------------------------------------------------------------------------

// generateTSFixture shells to ffmpeg to write a valid MPEG-TS segment at path.
// It uses ffmpeg's default mpegts video encoder (mpeg2video, native — no libx264
// dependency) so it works in the plain ffmpeg-base image, matching how the
// service produces .ts segments (MPEG-TS container) and lets the finalize merge
// run with `-c copy`.
func generateTSFixture(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH — run this test inside the recording ffmpeg-base image (%v)", err)
	}
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=d=2:size=320x240:rate=25",
		"-f", "mpegts", "-y", path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture generation failed for %s: %v\n%s", path, err, out)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("ffmpeg produced no/empty fixture at %s (size err=%v)", path, err)
	}
}

func fileExistsNonEmpty(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// TestArchiveEvidencePreserved_OnCameraDeleteReconcile_B2 drives the full B2
// fixture protocol in-process: seed a valid MPEG-TS segment inside the incident
// window → protect → finalize → poll to completed → simulate the camera-delete
// reconcile (real fetchCameras + real RecordingManager.Reload against a mock
// web-backend that no longer lists the camera) → assert the archive evidence
// (list entry + merged MP4 + metadata.json) survives, with a non-vacuity control
// proving the files existed before reconcile too.
func TestArchiveEvidencePreserved_OnCameraDeleteReconcile_B2(t *testing.T) {
	base := t.TempDir()
	recDir := filepath.Join(base, "recordings") // ARCHIVES_DIR / RECORDINGS_DIR at t.TempDir()
	arcDir := filepath.Join(base, "archives")
	if err := os.MkdirAll(recDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(arcDir, 0755); err != nil {
		t.Fatal(err)
	}

	rm := NewRecordingManager("rtmp://streaming:1935/live", recDir, 60*time.Second)
	am := NewArchiveManager(arcDir, recDir, rm)

	const (
		streamKey  = "camB2"
		incidentID = "incB2"
	)

	// Time window: place the seeded segment inside [incidentTime-1h, resolvedAt+30min].
	now := time.Now().UTC().Truncate(time.Second)
	incidentTime := now.Add(-30 * time.Minute) // protectFrom = incidentTime-1h = now-90min
	resolvedAt := now                           // finalizeTo   = resolvedAt+30min = now+30min
	segTime := now.Add(-5 * time.Minute)        // comfortably inside the window

	// 2) Seed a VALID MPEG-TS fixture named YYYYMMDD_HHMMSS.ts (UTC).
	segDir := filepath.Join(recDir, streamKey)
	if err := os.MkdirAll(segDir, 0755); err != nil {
		t.Fatal(err)
	}
	segPath := filepath.Join(segDir, segTime.Format("20060102_150405")+".ts")
	generateTSFixture(t, segPath)

	// 3) protect → finalize → poll until completed && sizeBytes>0.
	created := am.ProtectIncidentSegments(incidentID, []string{streamKey}, incidentTime)
	if len(created) != 1 || created[0].Status != "protecting" {
		t.Fatalf("protect did not create a protecting archive: %+v", created)
	}
	if _, err := am.FinalizeIncidentArchives(incidentID, resolvedAt); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}

	var completed *ArchiveMetadata
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range am.ListArchives() {
			if a.StreamKey != streamKey {
				continue
			}
			if a.Status == "failed" {
				// A "failed" status here means a bad fixture / out-of-window segment,
				// not the invariant under test — surface the ffmpeg/merge reason.
				t.Fatalf("archive %s went to 'failed' (bad fixture?): lastError=%q", a.ID, a.Error)
			}
			if a.Status == "completed" && a.SizeBytes > 0 {
				c := a
				completed = &c
			}
		}
		if completed != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if completed == nil {
		t.Fatalf("archive for %s never reached completed && sizeBytes>0 within timeout", streamKey)
	}

	// FilePath is the merged MP4 at {ARCHIVES_DIR}/{archiveId}/{streamKey}.mp4.
	mp4Path := completed.FilePath
	if mp4Path == "" {
		mp4Path = filepath.Join(arcDir, completed.ID, streamKey+".mp4")
	}
	metaPath := filepath.Join(arcDir, "metadata.json")

	// --- Non-vacuity control: the evidence MUST exist right after finalize, so a
	// broken fixture cannot make the post-reconcile assertions vacuously green. ---
	if !fileExistsNonEmpty(t, mp4Path) {
		t.Fatalf("PRE-reconcile: merged MP4 missing/empty at %s — fixture never produced evidence", mp4Path)
	}
	if !fileExistsNonEmpty(t, metaPath) {
		t.Fatalf("PRE-reconcile: metadata.json missing/empty at %s", metaPath)
	}

	// 4) Simulate the camera-delete reconcile. This reproduces the recording
	// reload handler body (main.go: cameras, _ := fetchCameras(...); manager.Reload(cameras))
	// using the REAL fetchCameras + REAL Reload, driven by a mock web-backend that
	// no longer lists the deleted camera (empty /internal/cameras).
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/cameras" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{}) // camera deleted → empty list
			return
		}
		http.NotFound(w, r)
	}))
	defer wb.Close()

	// Make the reconcile take the real "removed camera" stop branch (close stopCh,
	// delete states, terminateProcesses) — the exact stopCh·SIGTERM·states-delete
	// path the invariant claims never touches archiveManager. A nil cmd means no
	// live ffmpeg to signal (invariant is independent of recorder presence).
	rm.mu.Lock()
	rm.cameras = []CameraInfo{{StreamKey: streamKey, Enabled: true}}
	rm.states[streamKey] = &recorderState{status: "recording", stopCh: make(chan struct{})}
	rm.mu.Unlock()

	cams, err := fetchCameras(&http.Client{Timeout: 5 * time.Second}, wb.URL)
	if err != nil {
		t.Fatalf("fetchCameras (reconcile) failed: %v", err)
	}
	rm.Reload(cams)

	// The reconcile must actually have stopped the deleted recorder — otherwise the
	// "survives reconcile" claim would be vacuous (nothing was reconciled away).
	rm.mu.RLock()
	_, stillRunning := rm.states[streamKey]
	rm.mu.RUnlock()
	if stillRunning {
		t.Fatalf("reconcile did not stop recorder for deleted streamKey %q", streamKey)
	}

	// 5) ASSERT evidence preservation AFTER reconcile.
	// (a) archive list still contains a completed entry for the deleted stream_key.
	foundCompleted := false
	for _, a := range am.ListArchives() {
		if a.StreamKey == streamKey && a.Status == "completed" {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Errorf("(a) archive list no longer has a completed entry for deleted streamKey %q after reconcile", streamKey)
	}
	// (b) merged MP4 still exists with size>0.
	if !fileExistsNonEmpty(t, mp4Path) {
		t.Errorf("(b) merged MP4 evidence was purged by reconcile: %s", mp4Path)
	}
	// (c) metadata.json still exists.
	if !fileExistsNonEmpty(t, metaPath) {
		t.Errorf("(c) metadata.json was purged by reconcile: %s", metaPath)
	}
}
