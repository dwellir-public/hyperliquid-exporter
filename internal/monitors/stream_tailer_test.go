package monitors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// lineSink collects lines a streamTailer hands out and lets tests wait for a
// given count.
type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *lineSink) add(line []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, string(line))
}

func (s *lineSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func (s *lineSink) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d lines, got %v", n, s.snapshot())
	return nil
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// startTailer runs a tailer on dir and returns a channel that receives each
// path as it is opened, so tests can wait until the tailer is positioned.
func startTailer(t *testing.T, dir string, fn func([]byte) error) <-chan string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	opened := make(chan string, 8)
	tl := streamTailer{
		component: "test",
		dir:       dir,
		pause:     time.Millisecond,
		rescan:    5 * time.Millisecond,
		opened:    func(p string) { opened <- p },
	}
	go func() {
		defer close(done)
		tl.run(ctx, fn)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return opened
}

func waitOpened(t *testing.T, opened <-chan string) string {
	t.Helper()
	select {
	case p := <-opened:
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("tailer did not open a file")
		return ""
	}
}

func TestStreamTailerSeeksToEndThenStreamsWholeLines(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "1")
	appendFile(t, path, "old\n")

	var sink lineSink
	opened := startTailer(t, dir, func(b []byte) error { sink.add(b); return nil })
	waitOpened(t, opened)

	appendFile(t, path, "one\ntw")
	sink.waitFor(t, 1)
	// give the tailer several more polls to prove the fragment stays back
	time.Sleep(20 * time.Millisecond)
	if got := sink.snapshot(); len(got) != 1 || got[0] != "one" {
		t.Fatalf("partial line must not be delivered, got %v", got)
	}

	appendFile(t, path, "o\n\nthree\r\n")
	got := sink.waitFor(t, 3)
	want := []string{"one", "two", "three"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("blank line must be skipped, got %v", got)
	}
}

func TestStreamTailerDrainsOldFileOnRollover(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "10")
	appendFile(t, first, "")

	var sink lineSink
	opened := startTailer(t, dir, func(b []byte) error { sink.add(b); return nil })
	waitOpened(t, opened)

	// hour rolls: the old file gets its last line and the new file appears
	// in the same instant; both lines must arrive, old first
	appendFile(t, first, "last-of-10\n")
	appendFile(t, filepath.Join(dir, "11"), "first-of-11\n")

	got := sink.waitFor(t, 2)
	if got[0] != "last-of-10" || got[1] != "first-of-11" {
		t.Fatalf("unexpected order or content: %v", got)
	}
}

func TestStreamTailerContinuesPastRejectedLines(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "1")
	appendFile(t, path, "")

	var sink lineSink
	bad := errors.New("bad")
	opened := startTailer(t, dir, func(b []byte) error {
		if string(b) == "reject" {
			return bad
		}
		sink.add(b)
		return nil
	})
	waitOpened(t, opened)

	appendFile(t, path, "reject\nkeep\n")
	got := sink.waitFor(t, 1)
	if len(got) != 1 || got[0] != "keep" {
		t.Fatalf("got %v", got)
	}
}

func TestStreamTailerWaitsForMissingDir(t *testing.T) {
	initTestMetrics(t)
	dir := filepath.Join(t.TempDir(), "later")

	var sink lineSink
	opened := startTailer(t, dir, func(b []byte) error { sink.add(b); return nil })
	select {
	case p := <-opened:
		t.Fatalf("opened %s before dir existed", p)
	case <-time.After(30 * time.Millisecond):
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("got %v before dir existed", got)
	}
}

func TestParseStage(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{parseStageProbeJSON(), "json"},
		{parseStageProbeTime(), "timestamp"},
		{errors.New("height not found"), "shape"},
	}
	for _, c := range cases {
		if got := parseStage(c.err); got != c.want {
			t.Errorf("parseStage(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
