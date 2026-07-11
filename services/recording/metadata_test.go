package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveMetadataAtomic guards #74: saveMetadata writes via temp+rename, leaves
// no .tmp behind, and produces a file that reloads to the same content.
func TestSaveMetadataAtomic(t *testing.T) {
	tmp := t.TempDir()
	am := &ArchiveManager{
		archivesDir:  tmp,
		metadataPath: filepath.Join(tmp, "metadata.json"),
	}
	am.archives = []ArchiveMetadata{
		{ID: "a1", IncidentID: "i1", StreamKey: "cam1", Status: "completed"},
		{ID: "a2", IncidentID: "i2", StreamKey: "cam2", Status: "protecting"},
	}
	am.saveMetadata()

	// No leftover temp file.
	if _, err := os.Stat(am.metadataPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no .tmp file after saveMetadata, stat err=%v", err)
	}

	// Reload into a fresh manager and compare.
	am2 := &ArchiveManager{metadataPath: am.metadataPath}
	am2.loadMetadata()
	if len(am2.archives) != 2 || am2.archives[0].ID != "a1" || am2.archives[1].ID != "a2" {
		t.Fatalf("reloaded archives mismatch: %+v", am2.archives)
	}
}

// TestLoadMetadataCorruptBackup guards #74: a truncated/corrupt metadata file is
// moved aside (backed up) rather than silently reused, and load does not crash.
func TestLoadMetadataCorruptBackup(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "metadata.json")
	// Simulate a truncated write (invalid JSON).
	if err := os.WriteFile(path, []byte(`[{"id":"a1","strea`), 0644); err != nil {
		t.Fatal(err)
	}
	am := &ArchiveManager{metadataPath: path}
	am.loadMetadata()

	if len(am.archives) != 0 {
		t.Fatalf("expected 0 archives from corrupt metadata, got %d", len(am.archives))
	}
	// Original corrupt file should have been moved to a .corrupt.* backup.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt metadata.json to be moved aside, stat err=%v", err)
	}
	entries, _ := os.ReadDir(tmp)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "metadata.json.corrupt.") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a metadata.json.corrupt.* backup file")
	}
}
