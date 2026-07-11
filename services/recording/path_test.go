package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestIsValidPathComponent guards #73: identifiers used to build filesystem
// paths must reject traversal and separators.
func TestIsValidPathComponent(t *testing.T) {
	valid := []string{"cam-1", "incident_123", "abc", "A-b_C-9", "manual_20060102_150405"}
	for _, s := range valid {
		if !isValidPathComponent(s) {
			t.Errorf("isValidPathComponent(%q) = false; want true", s)
		}
	}
	invalid := []string{
		"..", "../../etc", "a/b", "a\\b", "../../..",
		"cam/../../evil", "", "foo.bar", "a b", "évil",
	}
	for _, s := range invalid {
		if isValidPathComponent(s) {
			t.Errorf("isValidPathComponent(%q) = true; want false", s)
		}
	}
}

// TestCreateArchiveRejectsTraversal ensures traversal-bearing incidentId/streamKey
// are rejected before any path is built. (#73)
func TestCreateArchiveRejectsTraversal(t *testing.T) {
	tmp := t.TempDir()
	am := &ArchiveManager{
		archivesDir:  tmp,
		metadataPath: filepath.Join(tmp, "metadata.json"),
	}
	now := time.Now()
	cases := [][2]string{
		{"../../../etc", "cam1"},
		{"inc1", "../../etc/passwd"},
		{"inc/../..", "cam1"},
	}
	for _, c := range cases {
		if _, err := am.CreateArchive(c[0], c[1], now, now.Add(time.Minute)); err == nil {
			t.Errorf("CreateArchive(%q,%q) = nil error; want rejection", c[0], c[1])
		}
	}
	if len(am.archives) != 0 {
		t.Fatalf("expected no archives after rejected creates, got %d", len(am.archives))
	}
}
