package infrastructure

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"agyq/internal/application"
)

// FileErrorLogger appends human-readable diagnostic error entries to an errors.log file.
type FileErrorLogger struct {
	mu   sync.Mutex
	path string
	file *os.File
}

// NewFileErrorLogger creates or opens the errors.log file at path.
func NewFileErrorLogger(path string) (*FileErrorLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open error log file %s: %w", path, err)
	}
	return &FileErrorLogger{
		path: path,
		file: f,
	}, nil
}

// LogError writes a structured, human-readable error record to the log file and fsyncs it.
func (l *FileErrorLogger) LogError(ctx context.Context, entry application.ErrorEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return
	}

	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}

	var sb strings.Builder
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString(fmt.Sprintf("[%s] ERROR", entry.Time.UTC().Format("2006-01-02 15:04:05 UTC")))
	if entry.Prompt > 0 {
		sb.WriteString(fmt.Sprintf(" - Prompt #%d", entry.Prompt))
		if entry.Title != "" {
			sb.WriteString(fmt.Sprintf(": %s", entry.Title))
		}
	}
	sb.WriteString("\n")

	if entry.Attempt > 0 || entry.Outcome != "" || entry.ExitCode != 0 {
		sb.WriteString(fmt.Sprintf("Attempt: %d | Outcome: %s | Exit Code: %d\n", entry.Attempt, entry.Outcome, entry.ExitCode))
	}

	if entry.Reason != "" {
		sb.WriteString(fmt.Sprintf("Reason: %s\n", entry.Reason))
	}
	if entry.Message != "" {
		sb.WriteString(fmt.Sprintf("Message: %s\n", entry.Message))
	}
	if entry.Transcript != "" {
		sb.WriteString(fmt.Sprintf("Transcript: %s\n", entry.Transcript))
	}
	if entry.StderrTail != "" {
		sb.WriteString("Stderr:\n")
		lines := strings.Split(strings.TrimSpace(entry.StderrTail), "\n")
		maxLines := 15
		if len(lines) > maxLines {
			lines = lines[len(lines)-maxLines:]
			sb.WriteString("  [...truncated...]\n")
		}
		for _, line := range lines {
			sb.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}
	sb.WriteString("--------------------------------------------------------------------------------\n")

	data := []byte(sb.String())
	if _, err := l.file.Write(data); err == nil {
		_ = l.file.Sync()
	}
}

// Close closes the underlying log file.
func (l *FileErrorLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// ReadErrorsFile reads the trailing lines from an errors.log file.
func ReadErrorsFile(path string, maxLines int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	// Buffer up to 1MB per line
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading error log: %w", err)
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines, nil
}
