package monitors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// fakeBinary is a script that answers --version like hl-node and hl-visor.
func fakeBinary(commit string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\necho 'commit %s | 2026-09-01 | release'\n", commit))
}

func writeFakeBinary(t *testing.T, path, commit string) {
	t.Helper()
	if err := os.WriteFile(path, fakeBinary(commit), 0o755); err != nil {
		t.Fatal(err)
	}
}

func upToDateValue(t *testing.T) int64 {
	t.Helper()
	v, ok := metrics.Int64GaugeValue(metrics.HLSoftwareUpToDate)
	if !ok {
		t.Fatal("hl_software_up_to_date is unset")
	}
	return v
}

func TestParseBinaryVersion(t *testing.T) {
	commit, date, err := parseBinaryVersion("commit 0abc123 | 2026-09-01 | release build\n")
	if err != nil || commit != "0abc123" || date != "2026-09-01" {
		t.Fatalf("got %q %q %v", commit, date, err)
	}
	if _, _, err := parseBinaryVersion("hl-node 1.0\n"); err == nil {
		t.Fatal("expected error for output without separators")
	}
}

func TestUpdateCheckerComparesVisorAndUsesETag(t *testing.T) {
	initTestMetrics(t)
	dir := t.TempDir()
	visor := filepath.Join(dir, "hl-visor")
	writeFakeBinary(t, visor, "aaa")

	var published atomic.Value
	published.Store("aaa")
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := `"` + published.Load().(string) + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		downloads.Add(1)
		w.Header().Set("ETag", etag)
		_, _ = w.Write(fakeBinary(published.Load().(string)))
	}))
	defer srv.Close()

	u := &updateChecker{visorPath: visor, url: srv.URL, client: srv.Client()}
	ctx := context.Background()

	if err := u.check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upToDateValue(t); got != 1 {
		t.Fatalf("up_to_date = %d, want 1", got)
	}

	// unchanged object: conditional request, no second download
	if err := u.check(ctx); err != nil {
		t.Fatal(err)
	}
	if n := downloads.Load(); n != 1 {
		t.Fatalf("downloads = %d after 304, want 1", n)
	}

	// CDN publishes a newer visor
	published.Store("bbb")
	if err := u.check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upToDateValue(t); got != 0 {
		t.Fatalf("up_to_date = %d after publish, want 0", got)
	}
	if n := downloads.Load(); n != 2 {
		t.Fatalf("downloads = %d, want 2", n)
	}

	// operator upgrades the local visor; mtime moves, so it is re-probed
	writeFakeBinary(t, visor, "bbb")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(visor, future, future); err != nil {
		t.Fatal(err)
	}
	if err := u.check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := upToDateValue(t); got != 1 {
		t.Fatalf("up_to_date = %d after local upgrade, want 1", got)
	}
}

func TestUpdateCheckerIdleWithoutLocalVisor(t *testing.T) {
	initTestMetrics(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u := &updateChecker{visorPath: filepath.Join(t.TempDir(), "hl-visor"), url: srv.URL, client: srv.Client()}
	if err := u.check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("published visor was fetched %d times with no local visor", n)
	}
}

func TestUpdateVersionInfoSkipsUnchangedBinary(t *testing.T) {
	initTestMetrics(t)
	path := filepath.Join(t.TempDir(), "hl-node")
	writeFakeBinary(t, path, "ccc")

	first, err := updateVersionInfo(context.Background(), path, time.Time{})
	if err != nil || first.IsZero() {
		t.Fatalf("first probe: %v %v", first, err)
	}
	// replace the contents without moving mtime: no re-probe, same mtime back
	writeFakeBinary(t, path, "ddd")
	if err := os.Chtimes(path, first, first); err != nil {
		t.Fatal(err)
	}
	again, err := updateVersionInfo(context.Background(), path, first)
	if err != nil || !again.Equal(first) {
		t.Fatalf("second probe: %v %v", again, err)
	}
}
