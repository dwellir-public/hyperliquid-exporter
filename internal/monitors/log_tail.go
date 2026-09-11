package monitors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

// readCommittedLines tails a log file from offset and only commits newline-terminated lines.
// If the file shrinks, offset is reset to zero so truncation/rotation-in-place is handled.
func readCommittedLines(filePath string, offset int64, fn func([]byte)) (int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return offset, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return offset, fmt.Errorf("failed to stat file: %w", err)
	}

	if offset > info.Size() {
		offset = 0
	}

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return offset, fmt.Errorf("failed to seek: %w", err)
		}
	}

	reader := bufio.NewReader(file)
	committed := offset

	for {
		rawLine, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Don't commit a trailing fragment; retry it on the next poll.
				return committed, nil
			}
			return committed, fmt.Errorf("failed to read line: %w", err)
		}

		committed += int64(len(rawLine))
		line := bytes.TrimSuffix(rawLine, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		fn(line)
	}
}

// latestFileRescan bounds how often EOF-loop tailers re-resolve the newest
// file; a rollover is picked up within this delay.
const latestFileRescan = 2 * time.Second

// tailState tracks the hourly log file a polling monitor is tailing and the
// offset of the last committed line.
type tailState struct {
	path   string
	offset int64
}

// poll reads new lines from latest via read, which returns the new committed
// offset. When latest differs from the current path, the current file is
// drained once more first so lines written just before the hour rolled over
// are not lost. A drain failure (for example the old file is gone) does not
// block the switch.
func (t *tailState) poll(latest string, read func(path string, offset int64) (int64, error)) error {
	if latest != t.path {
		if t.path != "" {
			if off, err := read(t.path, t.offset); err == nil {
				t.offset = off
			}
		}
		t.path, t.offset = latest, 0
	}
	off, err := read(t.path, t.offset)
	// readCommittedLines reports the committed offset even when a later read
	// fails; keeping it avoids re-emitting those lines on the next poll
	if off > t.offset {
		t.offset = off
	}
	return err
}

// streamTailer follows the newest file under dir in a tight loop, the way
// hl-node's hourly log streams are consumed. Only newline-terminated lines
// reach the callback; a trailing fragment is held until the rest is written.
// On first open it starts at EOF so a restart does not replay the current
// hour. When a newer file appears the old one is drained once more before
// switching. Source health is reported through the exporter envelope under
// stream; an empty stream disables that reporting.
type streamTailer struct {
	stream    string
	component string
	dir       string
	// idle runs at every EOF pause, for housekeeping that must fire while
	// the log is quiet
	idle func()
	// bufSize overrides the reader buffer, for streams with very long lines
	bufSize int
	// pause between EOF checks; 10 ms when zero
	pause time.Duration
	// rescan bounds how often the newest file is re-resolved; latestFileRescan when zero
	rescan time.Duration
	// opened, if set, is called after each file is opened; tests use it to
	// know the tailer is positioned before writing more
	opened func(path string)
}

const tailPause = 10 * time.Millisecond

func (t *streamTailer) run(ctx context.Context, fn func(line []byte) error) {
	rescan := t.rescan
	if rescan == 0 {
		rescan = latestFileRescan
	}
	files := utils.NewLatestFileCache(t.dir, rescan)
	var cur *fileTail
	first := true
	defer func() {
		if cur != nil {
			cur.close()
		}
	}()

	pause := t.pause
	if pause == 0 {
		pause = tailPause
	}

	for ctx.Err() == nil {
		latest, err := files.Get()
		switch {
		case errors.Is(err, os.ErrNotExist), err == nil && latest == "":
			t.setUp(false)
			sleepCtx(ctx, 10*time.Second)
			continue
		case err != nil:
			logger.ErrorComponent(t.component, "Error finding latest %s file: %v", t.stream, err)
			t.sourceError("walk")
			sleepCtx(ctx, 5*time.Second)
			continue
		}

		if cur == nil || latest != cur.path {
			if cur != nil {
				// drain lines written just before the rollover
				if err := cur.drain(t, fn); err != nil {
					logger.WarningComponent(t.component, "Error draining %s before switching: %v", cur.path, err)
				}
				cur.close()
				cur = nil
			}
			nf, err := openTail(latest, first, t.bufSize)
			if err != nil {
				logger.ErrorComponent(t.component, "Error opening %s: %v", latest, err)
				t.sourceError("read")
				sleepCtx(ctx, time.Second)
				continue
			}
			if first {
				logger.InfoComponent(t.component, "First run: streaming from the end of %s", latest)
			} else {
				logger.InfoComponent(t.component, "Switching to %s", latest)
			}
			first = false
			cur = nf
			if t.opened != nil {
				t.opened(latest)
			}
		}

		if err := cur.drain(t, fn); err != nil {
			logger.ErrorComponent(t.component, "Error reading %s: %v", cur.path, err)
			t.sourceError("read")
			sleepCtx(ctx, time.Second)
			continue
		}
		t.setUp(true)
		if t.idle != nil {
			t.idle()
		}
		sleepCtx(ctx, pause)
	}
}

func (t *streamTailer) setUp(up bool) {
	if t.stream != "" {
		metrics.SetSourceUp(t.stream, up)
	}
}

func (t *streamTailer) sourceError(stage string) {
	if t.stream != "" {
		metrics.IncrementSourceErrors(t.stream, stage)
		metrics.SetSourceUp(t.stream, false)
	}
}

// fileTail is one open file being followed by a streamTailer.
type fileTail struct {
	path    string
	f       *os.File
	r       *bufio.Reader
	pending []byte // trailing fragment without its newline yet
}

func openTail(path string, seekEnd bool, bufSize int) (*fileTail, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if seekEnd {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	r := bufio.NewReader(f)
	if bufSize > 0 {
		r = bufio.NewReaderSize(f, bufSize)
	}
	return &fileTail{path: path, f: f, r: r}, nil
}

func (ft *fileTail) close() {
	_ = ft.f.Close()
}

// drain reads to EOF, handing every complete line to fn and counting parse
// results into the envelope. A partial last line is kept for the next drain.
func (ft *fileTail) drain(t *streamTailer, fn func(line []byte) error) error {
	sampled := false
	for {
		raw, err := ft.r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			ft.pending = append(ft.pending, raw...)
			break
		}
		if err != nil {
			return err
		}
		line := raw
		if len(ft.pending) > 0 {
			line = append(ft.pending, raw...)
			ft.pending = nil
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if perr := fn(line); perr != nil {
			logger.DebugComponent(t.component, "Rejected %s line: %v", t.stream, perr)
			if t.stream != "" {
				metrics.IncrementParseErrors(t.stream, parseStage(perr))
			}
			continue
		}
		sampled = true
	}
	if sampled && t.stream != "" {
		metrics.MarkSourceSample(t.stream, time.Now())
	}
	return nil
}

// sleepCtx sleeps for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
