package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLatestFileHourlyLayout(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"20260909/23", "20260910/9", "20260910/10", "20260910/2"} {
		touch(t, filepath.Join(dir, p))
	}
	got, err := LatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "20260910", "10"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLatestFileReplicaLayout(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "2026-09-10T15:09:38Z/20260910/1142930000"))
	touch(t, filepath.Join(dir, "2026-09-10T15:09:38Z/20260910/1142940000"))
	touch(t, filepath.Join(dir, "2026-09-09T01:00:00Z/20260909/999999999999"))
	got, err := LatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "2026-09-10T15:09:38Z/20260910/1142940000"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLatestFileSkipsEmptyNewestDir(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "20260909/23"))
	if err := os.Mkdir(filepath.Join(dir, "20260910"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, ".hidden/99"))
	got, err := LatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "20260909", "23"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLatestFileEmptyAndMissing(t *testing.T) {
	got, err := LatestFile(t.TempDir())
	if err != nil || got != "" {
		t.Errorf("empty dir: got (%q, %v)", got, err)
	}
	if _, err := LatestFile("/nonexistent/path"); err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestLatestFileCache(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "20260910/1"))
	c := NewLatestFileCache(dir, time.Hour)
	first, err := c.Get()
	if err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "20260910/2"))
	if again, _ := c.Get(); again != first {
		t.Errorf("cache re-resolved inside interval: %q -> %q", first, again)
	}
	c.next = time.Time{}
	if fresh, _ := c.Get(); fresh != filepath.Join(dir, "20260910", "2") {
		t.Errorf("after expiry got %q", fresh)
	}
}

func TestSortNamesDescMixed(t *testing.T) {
	names := []string{"zz", "9", "10", "abc", "2", "999999999.rmp", "1000000000.rmp"}
	sortNamesDesc(names)
	want := []string{"1000000000.rmp", "999999999.rmp", "10", "9", "2", "zz", "abc"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}
