package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"agyq/internal/application"
)

// FileEventLog writes structured events to an append-only events.jsonl file (F-012).
type FileEventLog struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	syncCount int // test inspection
}

// NewFileEventLog creates or opens the events.jsonl file at the specified path.
func NewFileEventLog(path string) (*FileEventLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open events file %s: %w", path, err)
	}
	return &FileEventLog{
		path: path,
		file: f,
	}, nil
}

// IsTerminalEvent returns true if the event represents a terminal lifecycle event requiring fsync.
func IsTerminalEvent(e application.Event) bool {
	if e.Level == "fatal" {
		return true
	}
	switch e.Kind {
	case "prompt.succeeded", "prompt.failed", "prompt.interrupted", "prompt.skipped",
		"run.finished", "run.stopped", "run.fatal", "boot.recovered":
		return true
	}
	return false
}

// Emit appends one event as a single-line JSON object, flushes it immediately,
// and fsyncs the file if the event is terminal (F-012).
func (l *FileEventLog) Emit(ctx context.Context, e application.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}

	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')

	if _, err := l.file.Write(data); err != nil {
		return
	}

	if IsTerminalEvent(e) {
		if err := l.file.Sync(); err == nil {
			l.syncCount++
		}
	}
}

// SyncCount returns the number of fsyncs performed (for testing).
func (l *FileEventLog) SyncCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.syncCount
}

// Close closes the underlying file.
func (l *FileEventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// ReadEvents parses all events from an io.Reader. If the final line is torn/incomplete,
// it is tolerated (ignored) rather than returning an error (F-012 AC).
func ReadEvents(r io.Reader) ([]application.Event, error) {
	var events []application.Event

	scanner := bufio.NewScanner(r)
	// Buffer up to 1MB per line if needed
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var lastLine []byte
	var hadLine bool

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if hadLine {
			// Process previously saved line (guaranteed not the last line)
			var ev application.Event
			if err := json.Unmarshal(lastLine, &ev); err != nil {
				return nil, fmt.Errorf("invalid json in events stream: %w", err)
			}
			events = append(events, ev)
		}
		lastLine = make([]byte, len(line))
		copy(lastLine, line)
		hadLine = true
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("error scanning events: %w", err)
	}

	// Now handle the very last line: if valid JSON, append it.
	// If it fails to parse, tolerate it as a torn final line per F-012 AC.
	if hadLine && len(lastLine) > 0 {
		var ev application.Event
		if err := json.Unmarshal(lastLine, &ev); err == nil {
			events = append(events, ev)
		}
		// If unmarshal fails on the final line, tolerate and discard
	}

	return events, nil
}

// ReadEventsFile reads all events from a file path.
func ReadEventsFile(path string) ([]application.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadEvents(f)
}
