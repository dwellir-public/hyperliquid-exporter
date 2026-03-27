package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestFile(t *testing.T) {
	dir := t.TempDir()

	// create files with staggered mod times
	names := []string{"old.txt", "mid.txt", "new.txt"}
	base := time.Now().Add(-time.Hour)
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		modTime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	got, err := GetLatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "new.txt" {
		t.Errorf("got %q, want new.txt", got)
	}
}

func TestEmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := GetLatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty string for empty dir, got %q", got)
	}
}

func TestNestedDirs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}

	// older file in root
	old := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(old, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)

	// newer file in subdirectory
	newest := filepath.Join(sub, "newest.txt")
	if err := os.WriteFile(newest, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := GetLatestFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != newest {
		t.Errorf("got %q, want %q", got, newest)
	}
}

func TestNonexistentDir(t *testing.T) {
	_, err := GetLatestFile("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}
