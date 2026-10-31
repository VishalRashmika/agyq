package infrastructure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agyq/internal/application"
)

// DefaultMaxLogSize is 10 MiB per log file (F-064).
const DefaultMaxLogSize = 10 * 1024 * 1024

// DefaultMaxBackups is 5 rotated log files (F-064).
const DefaultMaxBackups = 5

// RotatingFileLog writes human-readable logs to a size-rotated file (F-064).
type RotatingFileLog struct {
	mu          sync.Mutex
	basePath    string
	maxSize     int64
	maxBackups  int
	minLevel    string
	currentSize int64
	file        *os.File
}

// NewRotatingFileLog creates a RotatingFileLog at basePath.
func NewRotatingFileLog(basePath string, maxSize int64, maxBackups int, minLevel string) (*RotatingFileLog, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxLogSize
	}
	if maxBackups <= 0 {
		maxBackups = DefaultMaxBackups
	}
	if minLevel == "" {
		minLevel = "info"
	}

	f, err := os.OpenFile(basePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", basePath, err)
	}

	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat log file %s: %w", basePath, err)
	}

	r := &RotatingFileLog{
		basePath:    basePath,
		maxSize:     maxSize,
		maxBackups:  maxBackups,
		minLevel:    minLevel,
		currentSize: st.Size(),
		file:        f,
	}

	return r, nil
}

func (r *RotatingFileLog) backupPath(i int) string {
	if i == 0 {
		return r.basePath
	}
	return fmt.Sprintf("%s.%d", r.basePath, i)
}

func (r *RotatingFileLog) rotate() error {
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}

	for i := r.maxBackups; i >= 1; i-- {
		src := r.backupPath(i - 1)
		dst := r.backupPath(i)
		if i == r.maxBackups {
			_ = os.Remove(dst)
		}
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}

	f, err := os.OpenFile(r.basePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create new log file %s: %w", r.basePath, err)
	}
	r.file = f
	r.currentSize = 0
	return nil
}

// Write writes data to the log file, rotating if writing would exceed maxSize.
func (r *RotatingFileLog) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return 0, fmt.Errorf("log file %s is closed", r.basePath)
	}

	dataLen := int64(len(data))
	if r.currentSize+dataLen > r.maxSize && r.currentSize > 0 {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(data)
	r.currentSize += int64(n)
	return n, err
}

// Close closes the underlying log file.
func (r *RotatingFileLog) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// ParseLogLevel maps log level strings to comparable integer severities.
func ParseLogLevel(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return 1
	case "info":
		return 2
	case "warn", "warning":
		return 3
	case "error":
		return 4
	case "fatal":
		return 5
	default:
		return 2 // info
	}
}

// LevelEnabled returns true if eventLevel meets or exceeds configuredLevel.
func LevelEnabled(eventLevel, configuredLevel string) bool {
	return ParseLogLevel(eventLevel) >= ParseLogLevel(configuredLevel)
}

// FormatHumanLog formats an Event into a human-readable log line.
func FormatHumanLog(e application.Event) string {
	t := e.Time
	if t.IsZero() {
		t = time.Now().UTC()
	}
	level := strings.ToUpper(e.Level)
	if level == "" {
		level = "INFO"
	}

	var sb strings.Builder
	sb.WriteString(t.Format("2006-01-02T15:04:05Z"))
	sb.WriteString(" [")
	sb.WriteString(level)
	sb.WriteString("]")
	if e.Kind != "" {
		sb.WriteString(" [")
		sb.WriteString(e.Kind)
		sb.WriteString("]")
	}
	if e.Prompt != 0 {
		fmt.Fprintf(&sb, " #%d", e.Prompt)
	}
	if e.Msg != "" {
		sb.WriteString(": ")
		sb.WriteString(e.Msg)
	}
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, " %s=%v", k, e.Fields[k])
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// CompositeEventLog writes structured events to events.jsonl and human-readable lines to agyq.log (F-064).
type CompositeEventLog struct {
	eventsLog *FileEventLog
	humanLog  *RotatingFileLog
}

// NewCompositeEventLog constructs a CompositeEventLog in stateDir.
func NewCompositeEventLog(stateDir string, minLevel string) (*CompositeEventLog, error) {
	return NewCustomCompositeEventLog(stateDir, minLevel, DefaultMaxLogSize, DefaultMaxBackups)
}

// NewCustomCompositeEventLog constructs a CompositeEventLog with custom rotation parameters.
func NewCustomCompositeEventLog(stateDir, minLevel string, maxSize int64, maxBackups int) (*CompositeEventLog, error) {
	eventsPath := filepath.Join(stateDir, "events.jsonl")
	evLog, err := NewFileEventLog(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open events.jsonl: %w", err)
	}

	humanPath := filepath.Join(stateDir, "agyq.log")
	humanLog, err := NewRotatingFileLog(humanPath, maxSize, maxBackups, minLevel)
	if err != nil {
		_ = evLog.Close()
		return nil, fmt.Errorf("failed to open agyq.log: %w", err)
	}

	return &CompositeEventLog{
		eventsLog: evLog,
		humanLog:  humanLog,
	}, nil
}

// Emit writes the event to events.jsonl and, if the level matches minLevel, to agyq.log.
func (c *CompositeEventLog) Emit(ctx context.Context, e application.Event) {
	if c.eventsLog != nil {
		c.eventsLog.Emit(ctx, e)
	}
	if c.humanLog != nil && LevelEnabled(e.Level, c.humanLog.minLevel) {
		line := FormatHumanLog(e)
		_, _ = c.humanLog.Write([]byte(line))
	}
}

// Close closes both log streams.
func (c *CompositeEventLog) Close() error {
	var firstErr error
	if c.eventsLog != nil {
		if err := c.eventsLog.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.humanLog != nil {
		if err := c.humanLog.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
