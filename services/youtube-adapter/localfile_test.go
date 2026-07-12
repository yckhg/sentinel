package main

import "testing"

// TestValidateLocalFile guards #71: only paths that resolve under /media/ are
// accepted; traversal and sibling-prefix tricks are rejected.
func TestValidateLocalFile(t *testing.T) {
	valid := []string{
		"/media/clip.mp4",
		"/media/sub/dir/clip.mp4",
		"/media/./clip.mp4",
	}
	for _, p := range valid {
		if err := validateLocalFile(p); err != nil {
			t.Errorf("validateLocalFile(%q) = %v; want nil", p, err)
		}
	}

	invalid := []string{
		"/media/../etc/passwd",
		"/media/../../etc/passwd",
		"/etc/passwd",
		"../media/clip.mp4",
		"/mediaevil/clip.mp4", // sibling that shares the /media prefix
		"/media",              // the dir itself, not a file
		"",
	}
	for _, p := range invalid {
		if err := validateLocalFile(p); err == nil {
			t.Errorf("validateLocalFile(%q) = nil; want error", p)
		}
	}
}
