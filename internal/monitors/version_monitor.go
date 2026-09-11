package monitors

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/safego"
)

// nodeBinaryStream is the envelope name for the local hl-node binary probe.
const nodeBinaryStream = "node_binary"

// StartVersionMonitor publishes hl_software_version from `hl-node --version`.
// The binary only changes when hl-visor swaps it, so the copy and exec run
// once and then only when the file's mtime moves.
func StartVersionMonitor(ctx context.Context, cfg config.Config, _ chan<- error) {
	safego.Go("system", func() {
		var lastMtime time.Time
		probe := func() {
			mtime, err := updateVersionInfo(ctx, cfg.NodeBinary, lastMtime)
			if err != nil {
				logger.ErrorComponent("system", "Version monitor error: %v", err)
				return
			}
			lastMtime = mtime
		}
		probe()

		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probe()
			}
		}
	})
}

// updateVersionInfo probes path when its mtime is newer than lastMtime and
// returns the mtime that is now published.
func updateVersionInfo(ctx context.Context, path string, lastMtime time.Time) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		metrics.IncrementSourceErrors(nodeBinaryStream, "stat")
		metrics.SetSourceUp(nodeBinaryStream, false)
		return lastMtime, fmt.Errorf("stat node binary: %w", err)
	}
	if !lastMtime.IsZero() && !info.ModTime().After(lastMtime) {
		metrics.SetSourceUp(nodeBinaryStream, true)
		return lastMtime, nil
	}

	commit, date, err := binaryVersionViaCopy(ctx, path)
	if err != nil {
		metrics.IncrementSourceErrors(nodeBinaryStream, "decode")
		metrics.SetSourceUp(nodeBinaryStream, false)
		return lastMtime, err
	}
	metrics.SetSourceUp(nodeBinaryStream, true)
	metrics.MarkSourceSample(nodeBinaryStream, time.Now())
	metrics.SetSoftwareVersion(commit, date)
	logger.InfoComponent("system", "Detected hl-node version: commit=%s, date=%s", commit, date)
	return info.ModTime(), nil
}

// binaryVersionViaCopy copies the binary to a temp file before running
// --version on it, so hl-visor atomically swapping the real binary mid-exec
// cannot bite. Use for live binaries; a fresh download nothing else touches
// can go through binaryVersionDirect.
func binaryVersionViaCopy(ctx context.Context, path string) (commit, date string, err error) {
	tmpFile, err := os.CreateTemp("", "hl_version_*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	source, err := os.Open(path)
	if err != nil {
		_ = tmpFile.Close()
		return "", "", fmt.Errorf("open binary: %w", err)
	}
	defer func() { _ = source.Close() }()

	if _, err := io.Copy(tmpFile, source); err != nil {
		_ = tmpFile.Close()
		return "", "", fmt.Errorf("copy binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", "", fmt.Errorf("close temp file: %w", err)
	}
	return binaryVersionDirect(ctx, tmpPath)
}

// binaryVersionDirect runs `<path> --version` and parses the
// "commit <hash> | <date> | ..." output shared by hl-node and hl-visor.
func binaryVersionDirect(ctx context.Context, path string) (commit, date string, err error) {
	if err := os.Chmod(path, 0o755); err != nil {
		return "", "", fmt.Errorf("chmod binary: %w", err)
	}

	cmd := exec.CommandContext(ctx, path, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("run --version: %w", err)
	}
	return parseBinaryVersion(out.String())
}

func parseBinaryVersion(output string) (commit, date string, err error) {
	parts := strings.Split(output, "|")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("unexpected version output format: %q", output)
	}
	commitParts := strings.Fields(parts[0])
	if len(commitParts) < 2 {
		return "", "", fmt.Errorf("unexpected version output format: %q", output)
	}
	return commitParts[1], strings.TrimSpace(parts[1]), nil
}
