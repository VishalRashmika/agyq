package infrastructure

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

func TestFileErrorLogger_WriteAndRead(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "errors.log")

	logger, err := NewFileErrorLogger(logPath)
	if err != nil {
		t.Fatalf("failed to create error logger: %v", err)
	}
	defer logger.Close()

	ctx := context.Background()
	entry := application.ErrorEntry{
		Time:       time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
		Prompt:     2,
		Title:      "Fix auth bug",
		Attempt:    1,
		ExitCode:   1,
		Outcome:    domain.OutcomeFatal,
		Reason:     "invalid model selection",
		Message:    "model not found",
		Transcript: ".agyq/transcripts/0002-a1.log",
		StderrTail: "error: unrecognized model slug\ncheck settings",
	}

	logger.LogError(ctx, entry)

	lines, err := ReadErrorsFile(logPath, 50)
	if err != nil {
		t.Fatalf("failed to read error log: %v", err)
	}

	content := strings.Join(lines, "\n")
	if !strings.Contains(content, "Prompt #2: Fix auth bug") {
		t.Errorf("expected prompt #2 in log, got:\n%s", content)
	}
	if !strings.Contains(content, "Outcome: fatal") {
		t.Errorf("expected outcome fatal in log, got:\n%s", content)
	}
	if !strings.Contains(content, "Transcript: .agyq/transcripts/0002-a1.log") {
		t.Errorf("expected transcript in log, got:\n%s", content)
	}
	if !strings.Contains(content, "unrecognized model slug") {
		t.Errorf("expected stderr in log, got:\n%s", content)
	}
}

func TestReadErrorsFile_NonExistent(t *testing.T) {
	lines, err := ReadErrorsFile("/nonexistent/errors.log", 10)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if lines != nil {
		t.Errorf("expected nil lines, got: %v", lines)
	}
}
