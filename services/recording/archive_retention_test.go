package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Archive retention policy — assertion gate A·B·C·D·E·F·F2·G·H
// (docs/spec/archive-retention-policy.md "검증 단언").
//
// Selection is a pure function selectEvictions(archives, maxBytes, retentionDays,
// now) with zero side effects; A·B·C·D·E·G and the union-dedup property are
// judged directly on it. F/F2/H drive the thin wrapper EvictArchives/evictIDs
// against a t.TempDir()-seeded ArchiveManager (metadata.json + directories) so
// the DeleteArchive round-trip, idempotency and single-cycle self-heal are
// judged in-process (no Docker/HTTP), mirroring cleanup_test.go /
// archive_evidence_preservation_test.go precedent.
// ---------------------------------------------------------------------------

var retBase = time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)

func rfc(ts time.Time) string { return ts.Format(time.RFC3339) }

// mkTarget builds an eviction TARGET: completed + IncidentTime!="" + parseable
// CreatedAt (the server-controlled, unforgeable auto-incident classifier).
func mkTarget(id string, created time.Time, size int64) ArchiveMetadata {
	return ArchiveMetadata{
		ID:           id,
		StreamKey:    "", // empty → DeleteArchive skips unprotectSegments (no recordings touched)
		CreatedAt:    rfc(created),
		SizeBytes:    size,
		Status:       "completed",
		IncidentTime: rfc(created),
	}
}

func idSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// targetTotal sums SizeBytes over the target set (completed ∧ IncidentTime!="")
// as observed in the manager's current list — the capacity invariant's subject.
func targetTotal(archives []ArchiveMetadata) int64 {
	var sum int64
	for _, a := range archives {
		if a.Status == "completed" && a.IncidentTime != "" {
			sum += a.SizeBytes
		}
	}
	return sum
}

// --- A: capacity cap ---------------------------------------------------------
func TestSelectEvictions_A_CapacityCap(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-3*time.Hour), 60),
		mkTarget("a2", retBase.Add(-2*time.Hour), 60),
		mkTarget("a3", retBase.Add(-1*time.Hour), 60),
	}
	ids := selectEvictions(archives, 100, 0, retBase)
	got := idSet(ids)
	if !got["a1"] || !got["a2"] || got["a3"] || len(ids) != 2 {
		t.Fatalf("A: expected eviction of oldest {a1,a2}, kept newest a3; got %v", ids)
	}
	// Post-cycle target total must be within the cap.
	survivors := applyEviction(archives, got)
	if total := targetTotal(survivors); total > 100 {
		t.Fatalf("A: post-cycle target total %d exceeds cap 100", total)
	}
}

// --- B: non-target preservation (IncidentTime is the classifier, not ID prefix) ---
func TestSelectEvictions_B_NonTargetPreserved(t *testing.T) {
	archives := []ArchiveMetadata{
		// completed but IncidentTime=="" → manual, NOT a target (huge size ignored).
		{ID: "manual_1", CreatedAt: rfc(retBase.Add(-5 * time.Hour)), SizeBytes: 1000, Status: "completed", IncidentTime: ""},
		// ID prefix says "incident_" but IncidentTime=="" → still NOT a target
		// (prefix does not dominate classification; no mis-deletion hole).
		{ID: "incident_x", CreatedAt: rfc(retBase.Add(-4 * time.Hour)), SizeBytes: 1000, Status: "completed", IncidentTime: ""},
		mkTarget("g1", retBase.Add(-3*time.Hour), 60),
		mkTarget("g2", retBase.Add(-2*time.Hour), 60),
		mkTarget("g3", retBase.Add(-1*time.Hour), 60),
	}
	ids := selectEvictions(archives, 100, 0, retBase)
	got := idSet(ids)
	if got["manual_1"] || got["incident_x"] {
		t.Fatalf("B: a non-target (IncidentTime==\"\") was selected for eviction: %v", ids)
	}
	if len(ids) == 0 {
		t.Fatalf("B: expected some target eviction (targets exceed cap), got none")
	}
	for _, id := range ids {
		if id != "g1" && id != "g2" && id != "g3" {
			t.Fatalf("B: eviction id %q is not one of the auto-incident targets", id)
		}
	}
}

// --- C: oldest-first ordering + (CreatedAt, ID) tie-break ---------------------
func TestSelectEvictions_C_OldestFirst(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-5*time.Hour), 50),
		mkTarget("a2", retBase.Add(-4*time.Hour), 50),
		mkTarget("a3", retBase.Add(-3*time.Hour), 50),
		mkTarget("a4", retBase.Add(-2*time.Hour), 50),
		mkTarget("a5", retBase.Add(-1*time.Hour), 50),
	}
	// total 250, cap 120 → delete oldest a1,a2,a3 (250→200→150→100≤120), keep a4,a5.
	ids := selectEvictions(archives, 120, 0, retBase)
	got := idSet(ids)
	if len(ids) != 3 || !got["a1"] || !got["a2"] || !got["a3"] || got["a4"] || got["a5"] {
		t.Fatalf("C: expected oldest {a1,a2,a3} deleted, newest {a4,a5} kept; got %v", ids)
	}

	// Tie-break: same CreatedAt second → lower ID evicted first.
	sameSec := retBase.Add(-10 * time.Hour)
	tie := []ArchiveMetadata{
		mkTarget("z", sameSec, 60),
		mkTarget("a", sameSec, 60),
		mkTarget("newest", retBase.Add(-1*time.Hour), 60),
	}
	// total 180, cap 130 → delete exactly one oldest; (CreatedAt,ID) asc ⇒ "a".
	tieIDs := selectEvictions(tie, 130, 0, retBase)
	if len(tieIDs) != 1 || tieIDs[0] != "a" {
		t.Fatalf("C: tie-break expected [a] (ID asc within same second), got %v", tieIDs)
	}
}

// --- D: age cap (retention days), floor ignored -------------------------------
func TestSelectEvictions_D_AgeCap(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("old", retBase.Add(-10*24*time.Hour), 5),   // age 10d > 7d → evict
		mkTarget("recent", retBase.Add(-1*24*time.Hour), 5), // age 1d ≤ 7d → keep
	}
	ids := selectEvictions(archives, 0, 7, retBase) // capacity OFF (0), age 7
	got := idSet(ids)
	if len(ids) != 1 || !got["old"] || got["recent"] {
		t.Fatalf("D: expected age eviction {old}, keep recent; got %v", ids)
	}

	// Age ignores the capacity "keep newest 1" floor: a sole, newest target that
	// is over-retention IS still evicted.
	solo := []ArchiveMetadata{mkTarget("solo", retBase.Add(-10*24*time.Hour), 5)}
	soloIDs := selectEvictions(solo, 0, 7, retBase)
	if len(soloIDs) != 1 || soloIDs[0] != "solo" {
		t.Fatalf("D: age policy must ignore the single-archive floor, got %v", soloIDs)
	}
}

// --- E: no-op (within cap, none over-retention) -------------------------------
func TestSelectEvictions_E_NoOp(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-2*time.Hour), 40),
		mkTarget("a2", retBase.Add(-1*time.Hour), 40),
	}
	ids := selectEvictions(archives, 100, 30, retBase) // total 80 ≤ 100, ages ≤ 30d
	if len(ids) != 0 {
		t.Fatalf("E: expected no-op (nothing deleted), got %v", ids)
	}
}

// --- G: disabled semantics (0/unset ⇒ policy off) -----------------------------
func TestSelectEvictions_G_Disabled(t *testing.T) {
	big := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-3*time.Hour), 100000),
		mkTarget("a2", retBase.Add(-2*time.Hour), 100000),
	}
	if ids := selectEvictions(big, 0, 0, retBase); len(ids) != 0 {
		t.Fatalf("G: ARCHIVE_MAX_BYTES=0 must disable capacity eviction, got %v", ids)
	}
	old := []ArchiveMetadata{
		mkTarget("o1", retBase.Add(-100*24*time.Hour), 5),
		mkTarget("o2", retBase.Add(-200*24*time.Hour), 5),
	}
	if ids := selectEvictions(old, 0, 0, retBase); len(ids) != 0 {
		t.Fatalf("G: ARCHIVE_RETENTION_DAYS=0 must disable age eviction, got %v", ids)
	}
}

// --- Union dedup: an id qualifying for BOTH policies appears once -------------
func TestSelectEvictions_UnionDedup(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("old1", retBase.Add(-10*24*time.Hour), 60), // age AND capacity
		mkTarget("old2", retBase.Add(-9*24*time.Hour), 60),  // age (and capacity)
		mkTarget("recent", retBase.Add(-1*24*time.Hour), 60),
	}
	ids := selectEvictions(archives, 100, 7, retBase)
	seen := map[string]int{}
	for _, id := range ids {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("union-dedup: id %q returned %d times (must be deduped)", id, n)
		}
	}
	got := idSet(ids)
	if !got["old1"] || !got["old2"] || got["recent"] || len(ids) != 2 {
		t.Fatalf("union-dedup: expected {old1,old2} once each, kept recent; got %v", ids)
	}
}

// applyEviction returns the archives that survive removing the given id set —
// a pure test helper for reasoning about post-cycle totals.
func applyEviction(archives []ArchiveMetadata, evicted map[string]bool) []ArchiveMetadata {
	var out []ArchiveMetadata
	for _, a := range archives {
		if !evicted[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// seedManager builds an ArchiveManager over t.TempDir() with the given archives
// materialized both in the in-memory list AND on disk (metadata.json + one
// directory-with-dummy-file per archive), so DeleteArchive's directory removal
// and metadata SSOT can be observed. Returns the manager and the archives dir.
func seedManager(t *testing.T, archives []ArchiveMetadata) (*ArchiveManager, string) {
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
	rm := NewRecordingManager("rtmp://x", recDir, time.Minute)
	am := NewArchiveManager(arcDir, recDir, rm)
	am.mu.Lock()
	am.archives = append([]ArchiveMetadata(nil), archives...)
	am.mu.Unlock()
	am.saveMetadata()
	for _, a := range archives {
		dir := filepath.Join(arcDir, a.ID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("dummy"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return am, arcDir
}

func dirExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func listContains(am *ArchiveManager, id string) bool {
	for _, a := range am.ListArchives() {
		if a.ID == id {
			return true
		}
	}
	return false
}

func metadataContains(t *testing.T, arcDir, id string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(arcDir, "metadata.json"))
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var archives []ArchiveMetadata
	if err := json.Unmarshal(data, &archives); err != nil {
		t.Fatalf("unmarshal metadata.json: %v", err)
	}
	for _, a := range archives {
		if a.ID == id {
			return true
		}
	}
	return false
}

// --- F: integrity — EvictArchives→DeleteArchive round-trip, non-vacuous -------
func TestEvictArchives_F_Integrity(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-3*time.Hour), 60),
		mkTarget("a2", retBase.Add(-2*time.Hour), 60),
		mkTarget("a3", retBase.Add(-1*time.Hour), 60),
	}
	am, arcDir := seedManager(t, archives)
	am.evictMaxBytes = 100
	am.evictRetentionDays = 0

	// Non-vacuity control: every seeded directory MUST exist before eviction, so
	// a missing-path os.RemoveAll cannot make "removed" vacuously true.
	for _, id := range []string{"a1", "a2", "a3"} {
		if !dirExists(t, filepath.Join(arcDir, id)) {
			t.Fatalf("F(pre): seeded dir for %s missing before eviction", id)
		}
	}

	am.EvictArchives(retBase)

	// Deleted ids: absent from directory AND from list SSOT AND metadata.json.
	for _, id := range []string{"a1", "a2"} {
		if dirExists(t, filepath.Join(arcDir, id)) {
			t.Errorf("F: directory for evicted %s still present (RemoveAll not exercised)", id)
		}
		if listContains(am, id) {
			t.Errorf("F: evicted %s still in list SSOT", id)
		}
		if metadataContains(t, arcDir, id) {
			t.Errorf("F: evicted %s still in metadata.json", id)
		}
	}
	// Survivor present on both sides (dangling-free correspondence).
	if !dirExists(t, filepath.Join(arcDir, "a3")) {
		t.Errorf("F: survivor a3 directory was removed")
	}
	if !listContains(am, "a3") || !metadataContains(t, arcDir, "a3") {
		t.Errorf("F: survivor a3 missing from SSOT")
	}
}

// --- F2: idempotent deletion (concurrent DELETE race absorbed) ----------------
func TestEvictArchives_F2_Idempotent(t *testing.T) {
	// Direct not-found absorption: evict loop is handed an id already gone from
	// the list (models a snapshot whose target was concurrently DELETEd before
	// its DeleteArchive ran). The loop must absorb it and STILL delete the real
	// target that follows — cycle not interrupted.
	archives := []ArchiveMetadata{mkTarget("real", retBase.Add(-1*time.Hour), 60)}
	am, arcDir := seedManager(t, archives)

	am.evictIDs([]string{"ghost_absent", "real"})

	if dirExists(t, filepath.Join(arcDir, "real")) {
		t.Errorf("F2: loop stopped at not-found; 'real' after the ghost was not deleted")
	}
	if listContains(am, "real") {
		t.Errorf("F2: 'real' still in list after evictIDs continued past ghost")
	}

	// Full-cycle self-heal with a pre-deleted (concurrently removed) target: the
	// remaining over-cap targets are still evicted and the cycle completes.
	archives2 := []ArchiveMetadata{
		mkTarget("b1", retBase.Add(-3*time.Hour), 60),
		mkTarget("b2", retBase.Add(-2*time.Hour), 60),
		mkTarget("b3", retBase.Add(-1*time.Hour), 60),
	}
	am2, arcDir2 := seedManager(t, archives2)
	am2.evictMaxBytes = 100
	// Simulate a concurrent DELETE of b1 committing before the cycle.
	if err := am2.DeleteArchive("b1"); err != nil {
		t.Fatalf("F2: pre-delete of b1 failed: %v", err)
	}
	am2.EvictArchives(retBase) // must complete without interruption
	if total := targetTotal(am2.ListArchives()); total > 100 {
		t.Errorf("F2: after cycle target total %d exceeds cap 100", total)
	}
	if dirExists(t, filepath.Join(arcDir2, "b1")) {
		t.Errorf("F2: b1 directory should be gone after pre-delete")
	}
}

// --- H: periodicity/self-heal — one ticker-body EvictArchives restores cap ----
func TestEvictArchives_H_SelfHeal(t *testing.T) {
	archives := []ArchiveMetadata{
		mkTarget("a1", retBase.Add(-4*time.Hour), 60),
		mkTarget("a2", retBase.Add(-3*time.Hour), 60),
		mkTarget("a3", retBase.Add(-2*time.Hour), 60),
		mkTarget("a4", retBase.Add(-1*time.Hour), 60),
	}
	am, _ := seedManager(t, archives)
	am.evictMaxBytes = 100
	am.evictRetentionDays = 0

	if before := targetTotal(am.ListArchives()); before <= 100 {
		t.Fatalf("H(pre): seed must exceed cap to be non-vacuous, got %d", before)
	}
	// One direct ticker-body invocation, no HTTP/API request.
	am.EvictArchives(retBase)
	if after := targetTotal(am.ListArchives()); after > 100 {
		t.Fatalf("H: capacity invariant not restored by one cycle, total %d > 100", after)
	}
}
