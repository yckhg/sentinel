package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Gates for docs/spec/archive-download-ux.md — 단위 A (제공자 측).
//
// These tests drive the recording.md API-contract delta declared in the spec's
// "(횡단) 접합부 — API 계약 델타":
//   - ArchiveMetadata gains a `completedAt` field (RFC3339, UTC) recorded
//     ATOMICALLY with the completed transition (A3 + 단위A 핵심로직 "동시적 불변식":
//     status·sizeBytes·completedAt·다운로드가능성 중 하나만 참인 중간 상태 금지).
//   - the failure-reason field is exposed as JSON `lastError` and is non-empty
//     for every `failed` archive (A4 — 모든 failed 종단 전이).
//   - terminal states (completed/failed) never revert (A7 종단 단조성).
//   - download gate: completed→2xx · non-completed(미완료 4종 및 failed)→409 ·
//     부재→404 (A5/A6/A8 다운로드 게이트 확장).
//
// Implementation seams these tests EXPECT the implementer to provide (구현 대상 —
// this file is a red TDD gate; the package will not compile until they exist):
//
//   * ArchiveMetadata serializes a `completedAt` field (JSON key "completedAt",
//     RFC3339 UTC; non-null when status=="completed", null/absent otherwise).
//   * ArchiveMetadata serializes its failure reason under JSON key "lastError"
//     (spec unifies the prior "error"/"reason" naming to recording's lastError).
//   * func (am *ArchiveManager) markCompleted(archiveID string, sizeBytes int64, filePath string)
//       — atomically sets status="completed", sizeBytes, filePath AND completedAt
//         (now, UTC) under one lock; the inline processArchive completion block
//         delegates to it. It is a no-op if the archive is already terminal
//         (monotonicity: a `failed` archive must not become `completed`).
//   * updateStatus refuses to move an already-terminal archive to a different
//       status (a `completed` archive must not fall back to an in-progress state).
//   * func downloadGateCode(am *ArchiveManager, id string) int
//       — pure download-gate decision returning the HTTP status the
//         GET /api/archives/{id}/download handler must use: 200 (completed),
//         409 (exists but non-completed, incl. failed), 404 (absent). The inline
//         handler delegates to it.

// newTestAM builds a self-contained ArchiveManager backed by a temp metadata
// file so saveMetadata() writes are isolated (no host state touched).
func newTestAM(t *testing.T, archives ...ArchiveMetadata) *ArchiveManager {
	t.Helper()
	dir := t.TempDir()
	return &ArchiveManager{
		archives:     archives,
		archivesDir:  dir,
		metadataPath: filepath.Join(dir, "metadata.json"),
	}
}

// archiveJSON returns the marshalled JSON object for the archive with id. Reading
// through JSON (not the Go struct field) pins the *wire contract* field names
// (completedAt / lastError) that 단위 B and §계약8 proxy consume.
func archiveJSON(t *testing.T, am *ArchiveManager, id string) map[string]any {
	t.Helper()
	for _, a := range am.ListArchives() {
		if a.ID != id {
			continue
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("marshal archive %q: %v", id, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal archive %q: %v", id, err)
		}
		return m
	}
	t.Fatalf("archive %q not found in list", id)
	return nil
}

func statusOf(t *testing.T, am *ArchiveManager, id string) string {
	t.Helper()
	for _, a := range am.ListArchives() {
		if a.ID == id {
			return a.Status
		}
	}
	t.Fatalf("archive %q not found", id)
	return ""
}

// A3 — completed 진입 시 completedAt(RFC3339, UTC)가 status·sizeBytes와 원자적으로
// 기록되고, 미완료 항목은 completedAt이 null/부재다.
func TestArchiveCompletedAtRecordedAtomically(t *testing.T) {
	// A real (small) file so a completed archive points at existing media.
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(mp4, []byte("\x00\x00\x00 ftypmp42fake"), 0644); err != nil {
		t.Fatal(err)
	}

	am := newTestAM(t,
		ArchiveMetadata{ID: "prog", IncidentID: "i1", StreamKey: "cam1", Status: "processing"},
	)

	// Before completion: 미완료 항목의 completedAt은 null/부재여야 한다.
	if v, ok := archiveJSON(t, am, "prog")["completedAt"]; ok && v != nil && v != "" {
		t.Errorf("in-progress archive must not carry completedAt, got %v", v)
	}

	am.markCompleted("prog", int64(len("\x00\x00\x00 ftypmp42fake")), mp4)

	m := archiveJSON(t, am, "prog")
	if got := m["status"]; got != "completed" {
		t.Fatalf("status after markCompleted = %v, want completed", got)
	}
	if got := m["sizeBytes"]; got == nil || got.(float64) == 0 {
		t.Errorf("sizeBytes must be recorded with completion, got %v", got)
	}
	ct, _ := m["completedAt"].(string)
	if ct == "" {
		t.Fatalf("completed archive must carry non-empty completedAt (A3), got %v", m["completedAt"])
	}
	tm, err := time.Parse(time.RFC3339, ct)
	if err != nil {
		t.Fatalf("completedAt %q is not RFC3339-parseable (A3)", ct)
	}
	if _, off := tm.Zone(); off != 0 {
		t.Errorf("completedAt must be UTC (zero offset), got %q (offset %ds)", ct, off)
	}

	// 동시적 불변식: 목록의 어떤 completed 항목도 completedAt 없이 관측되지 않는다.
	for _, a := range am.ListArchives() {
		mm := archiveJSON(t, am, a.ID)
		if mm["status"] == "completed" {
			if c, _ := mm["completedAt"].(string); c == "" {
				t.Errorf("archive %s is completed but has no completedAt (중간 상태 관측 금지)", a.ID)
			}
		}
	}
}

// A4 — status=="failed" 항목은 비어있지 않은 실패 사유를 JSON `lastError`로 노출한다
// (finalize-직접실패 / recovery 실패 등 모든 failed 종단 전이).
func TestFailedArchiveExposesLastError(t *testing.T) {
	am := newTestAM(t,
		ArchiveMetadata{ID: "f1", IncidentID: "i1", StreamKey: "cam1", Status: "processing"}, // finalize/merge 실패 경로
		ArchiveMetadata{ID: "f2", IncidentID: "i2", StreamKey: "cam2", Status: "finalizing"}, // recovery 실패 경로
	)

	am.updateStatus("f1", "failed", "ffmpeg merge: exit status 1")
	am.updateStatus("f2", "failed", "recovery: archive has invalid from/to range")

	for _, id := range []string{"f1", "f2"} {
		m := archiveJSON(t, am, id)
		if m["status"] != "failed" {
			t.Fatalf("%s status = %v, want failed", id, m["status"])
		}
		le, ok := m["lastError"].(string)
		if !ok || le == "" {
			t.Errorf("failed archive %s must expose non-empty JSON `lastError` (A4), got %v", id, m["lastError"])
		}
	}

	// 순회 근거: 모든 failed 항목이 non-empty lastError를 가져야 한다.
	for _, a := range am.ListArchives() {
		if a.Status != "failed" {
			continue
		}
		if le, _ := archiveJSON(t, am, a.ID)["lastError"].(string); le == "" {
			t.Errorf("failed archive %s has empty lastError (모든 failed 종단 전이 근거 위반)", a.ID)
		}
	}
}

// A7 — 종단 상태(completed/failed)는 이후 뒤집히지 않는다(단조성).
func TestTerminalStateMonotonic(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(mp4, []byte("media"), 0644); err != nil {
		t.Fatal(err)
	}

	am := newTestAM(t,
		ArchiveMetadata{ID: "c", IncidentID: "i1", StreamKey: "cam1", Status: "processing"},
		ArchiveMetadata{ID: "f", IncidentID: "i2", StreamKey: "cam2", Status: "processing"},
	)

	// completed 는 미완료로 되돌아가지 않는다.
	am.markCompleted("c", 5, mp4)
	completedAtBefore, _ := archiveJSON(t, am, "c")["completedAt"].(string)
	am.updateStatus("c", "protecting", "")
	if s := statusOf(t, am, "c"); s != "completed" {
		t.Errorf("completed archive reverted to %q (A7 단조성 위반)", s)
	}
	if got, _ := archiveJSON(t, am, "c")["completedAt"].(string); got != completedAtBefore {
		t.Errorf("completedAt mutated on a terminal archive: %q -> %q", completedAtBefore, got)
	}

	// failed 는 completed 로 바뀌지 않는다.
	am.updateStatus("f", "failed", "boom")
	am.markCompleted("f", 5, mp4)
	if s := statusOf(t, am, "f"); s != "failed" {
		t.Errorf("failed archive became %q (A7 단조성 위반 — failed→completed 금지)", s)
	}
}

// A5/A6/A8 — 다운로드 게이트: completed→200 · 미완료(4종)/failed→409 · 부재→404.
func TestDownloadGateStatus(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "done.mp4")
	if err := os.WriteFile(mp4, []byte("\x00\x00\x00 ftypmp42media-body"), 0644); err != nil {
		t.Fatal(err)
	}

	am := newTestAM(t,
		ArchiveMetadata{ID: "done", Status: "completed", FilePath: mp4, SizeBytes: 19},
		ArchiveMetadata{ID: "prot", Status: "protecting"},
		ArchiveMetadata{ID: "pend", Status: "pending"},
		ArchiveMetadata{ID: "fina", Status: "finalizing"},
		ArchiveMetadata{ID: "proc", Status: "processing"},
		ArchiveMetadata{ID: "fail", Status: "failed"},
	)

	cases := []struct {
		id   string
		want int
	}{
		{"done", 200}, // A6 — completed 서빙
		{"prot", 409}, // A5 — 미완료 거절
		{"pend", 409}, // A5
		{"fina", 409}, // A5
		{"proc", 409}, // A5
		{"fail", 409}, // A8 — failed 도 미디어 미반환
		{"ghost", 404}, // 부재
	}
	for _, c := range cases {
		if got := downloadGateCode(am, c.id); got != c.want {
			t.Errorf("downloadGateCode(%q) = %d, want %d", c.id, got, c.want)
		}
	}
}
