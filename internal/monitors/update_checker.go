package monitors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

// visorUpdateStream is the envelope name for the published hl-visor check.
const visorUpdateStream = "visor_update"

const updateCheckInterval = 30 * time.Minute

// updateChecker compares the local hl-visor build against the hl-visor
// published on the Hyperliquid CDN and drives hl_software_up_to_date.
//
// hl-visor is the binary operators manage; it swaps its hl-node child on its
// own, so comparing the published visor against the local hl-node commit
// (what an earlier version did) reports "outdated" whenever the child was
// auto-updated mid-release. The remote side is fetched conditionally: while
// the CDN object's ETag is unchanged the check is one small round-trip.
type updateChecker struct {
	visorPath string
	url       string
	client    *http.Client

	localHash  string
	localMtime time.Time
	latestHash string
	etag       string

	missingLogged bool
}

func newUpdateChecker(cfg config.Config) *updateChecker {
	url := "https://binaries.hyperliquid-testnet.xyz/Testnet/hl-visor"
	if cfg.Chain == "mainnet" {
		url = "https://binaries.hyperliquid.xyz/Mainnet/hl-visor"
	}
	return &updateChecker{
		visorPath: filepath.Join(cfg.BinaryHome, "hl-visor"),
		url:       url,
		client:    &http.Client{Timeout: 10 * time.Minute},
	}
}

func StartUpdateChecker(ctx context.Context, cfg config.Config, _ chan<- error) {
	if !cfg.EnableBinaryMetrics {
		logger.InfoComponent("system", "Update checker disabled (--binary-metrics=false)")
		return
	}
	u := newUpdateChecker(cfg)
	safego.Go("system", func() {
		u.tick(ctx)

		ticker := time.NewTicker(updateCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				u.tick(ctx)
			}
		}
	})
}

func (u *updateChecker) tick(ctx context.Context) {
	if err := u.check(ctx); err != nil {
		logger.ErrorComponent("system", "Update checker error: %v", err)
	}
}

// check refreshes both hashes as needed and publishes the comparison. A
// missing local hl-visor leaves hl_software_up_to_date unset.
func (u *updateChecker) check(ctx context.Context) error {
	info, err := os.Stat(u.visorPath)
	if err != nil {
		metrics.SetSourceUp(visorUpdateStream, false)
		if !os.IsNotExist(err) {
			metrics.IncrementSourceErrors(visorUpdateStream, "stat")
			return fmt.Errorf("stat local hl-visor: %w", err)
		}
		if !u.missingLogged {
			logger.InfoComponent("system", "No hl-visor at %s; update check idle (hl_software_up_to_date stays unset). Set BINARY_HOME if the visor lives elsewhere", u.visorPath)
			u.missingLogged = true
		}
		return nil
	}

	if u.localHash == "" || info.ModTime().After(u.localMtime) {
		commit, _, err := binaryVersionViaCopy(ctx, u.visorPath)
		if err != nil {
			metrics.IncrementSourceErrors(visorUpdateStream, "decode")
			metrics.SetSourceUp(visorUpdateStream, false)
			return fmt.Errorf("local hl-visor version: %w", err)
		}
		u.localHash = commit
		u.localMtime = info.ModTime()
	}

	if err := u.refreshLatest(ctx); err != nil {
		metrics.IncrementSourceErrors(visorUpdateStream, "request")
		metrics.SetSourceUp(visorUpdateStream, false)
		return err
	}
	metrics.SetSourceUp(visorUpdateStream, true)
	metrics.MarkSourceSample(visorUpdateStream, time.Now())

	upToDate := u.localHash == u.latestHash
	metrics.SetSoftwareUpToDate(upToDate)
	if !upToDate {
		logger.InfoComponent("system", "hl-visor is NOT up to date. Local: %s, Latest: %s", u.localHash, u.latestHash)
	}
	return nil
}

// refreshLatest resolves the commit of the newest published hl-visor. With a
// stored ETag the request is a 304 round-trip; the download and exec only
// happen when the object changed.
func (u *updateChecker) refreshLatest(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.url, nil)
	if err != nil {
		return fmt.Errorf("build update request: %w", err)
	}
	if u.etag != "" && u.latestHash != "" {
		req.Header.Set("If-None-Match", u.etag)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch published hl-visor: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil
	case http.StatusOK:
	default:
		return fmt.Errorf("unexpected status %s fetching %s", resp.Status, u.url)
	}

	tmpFile, err := os.CreateTemp("", "hl-visor-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("download published hl-visor: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	commit, _, err := binaryVersionDirect(ctx, tmpPath)
	if err != nil {
		return fmt.Errorf("published hl-visor version: %w", err)
	}
	u.latestHash = commit
	u.etag = resp.Header.Get("ETag")
	return nil
}
