//go:build linux

package isolation

import (
	"io"
	"os"
	"sync"
)

const maxDetachedLogBytes int64 = 10 * 1024 * 1024

const detachedLogTruncatedMarker = "\n[MiniDock: log output truncated at 10 MiB]\n"

// cappedLogWriter serializes stdout and stderr writes to a single log file.
// Once the file reaches its limit, later output is discarded and a marker is
// written once so users can tell that the log is incomplete.
type cappedLogWriter struct {
	mu        sync.Mutex
	file      *os.File
	written   int64
	truncated bool
}

func (w *cappedLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.truncated {
		return len(data), nil
	}
	contentLimit := maxDetachedLogBytes - int64(len(detachedLogTruncatedMarker))
	remaining := contentLimit - w.written
	if int64(len(data)) <= remaining {
		n, err := w.file.Write(data)
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		if n != len(data) {
			return n, io.ErrShortWrite
		}
		return n, nil
	}

	if remaining > 0 {
		prefix := data[:int(remaining)]
		n, err := w.file.Write(prefix)
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		if n != len(prefix) {
			return n, io.ErrShortWrite
		}
	}
	if _, err := w.file.WriteString(detachedLogTruncatedMarker); err != nil {
		return 0, err
	}
	w.truncated = true
	// Report the entire input as consumed so a full log does not break the
	// workload's stdout or stderr pipe.
	return len(data), nil
}
