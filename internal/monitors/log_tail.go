package monitors

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
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
