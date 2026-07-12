package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSizeCacheTTL guards #81: the size walk is memoized within the TTL and only
// recomputed after it expires.
func TestSizeCacheTTL(t *testing.T) {
	fake := time.Unix(1000, 0)
	c := &sizeCache{ttl: 15 * time.Second, now: func() time.Time { return fake }}

	calls := 0
	compute := func() int64 { calls++; return int64(calls) }

	if v := c.get(compute); v != 1 || calls != 1 {
		t.Fatalf("first get: v=%d calls=%d, want 1/1", v, calls)
	}
	// Within TTL: cached, no recompute.
	fake = fake.Add(5 * time.Second)
	if v := c.get(compute); v != 1 || calls != 1 {
		t.Fatalf("within-TTL get: v=%d calls=%d, want 1/1 (cached)", v, calls)
	}
	// After TTL: recompute.
	fake = fake.Add(20 * time.Second)
	if v := c.get(compute); v != 2 || calls != 2 {
		t.Fatalf("post-TTL get: v=%d calls=%d, want 2/2 (recomputed)", v, calls)
	}
}

// TestDirSizeCounts verifies dirSize sums regular file sizes recursively.
func TestDirSizeCounts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b"), []byte("world!"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := dirSize(dir); got != 11 {
		t.Fatalf("dirSize = %d, want 11", got)
	}
}
