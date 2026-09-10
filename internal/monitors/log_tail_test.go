package monitors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hour rollover: lines appended to the old hour file after the new one
// appears must be read exactly once, and the new file starts at zero.
func TestTailStatePollRollover(t *testing.T) {
	dir := t.TempDir()
	h1 := filepath.Join(dir, "1")
	h2 := filepath.Join(dir, "2")

	var got []string
	read := func(path string, offset int64) (int64, error) {
		return readCommittedLines(path, offset, func(line []byte) { got = append(got, string(line)) })
	}

	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var tail tailState
	write(h1, "a\nb\n")
	if err := tail.poll(h1, read); err != nil {
		t.Fatal(err)
	}

	// old hour gets a late line, new hour file appears with its first lines
	write(h1, "a\nb\nc\n")
	write(h2, "d\ne\n")
	if err := tail.poll(h2, read); err != nil {
		t.Fatal(err)
	}
	if tail.path != h2 {
		t.Errorf("tail.path = %q, want %q", tail.path, h2)
	}

	// steady state on the new file, with a torn trailing line not yet committed
	write(h2, "d\ne\nf\ng")
	if err := tail.poll(h2, read); err != nil {
		t.Fatal(err)
	}

	if want := "a b c d e f"; strings.Join(got, " ") != want {
		t.Errorf("lines = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestTailStatePollDrainFailureStillSwitches(t *testing.T) {
	dir := t.TempDir()
	h2 := filepath.Join(dir, "2")
	if err := os.WriteFile(h2, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var n int
	read := func(path string, offset int64) (int64, error) {
		return readCommittedLines(path, offset, func([]byte) { n++ })
	}
	tail := tailState{path: filepath.Join(dir, "gone"), offset: 7}
	if err := tail.poll(h2, read); err != nil {
		t.Fatal(err)
	}
	if tail.path != h2 || n != 1 {
		t.Errorf("path=%q lines=%d", tail.path, n)
	}
}
