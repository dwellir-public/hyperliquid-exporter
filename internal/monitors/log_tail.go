package monitors

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
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
