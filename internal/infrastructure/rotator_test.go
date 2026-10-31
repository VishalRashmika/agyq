package infrastructure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

func TestRotatingFileLog_RotationAndBackupsCap(t *testing.T) {
	// F-064 AC: Log files size-rotated (10 MiB x 5); test rotation.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agyq.log")

	// Set small maxSize to test rotation easily (100 bytes), maxBackups 5
	maxSize := int64(100)
	maxBackups := 5
	rotator, err := NewRotatingFileLog(logPath, maxSize, maxBackups, "info")
	if err != nil {
		t.Fatalf("NewRotatingFileLog failed: %v", err)
	}

	// Write 15 chunks of 60 bytes each -> will rotate multiple times
	for i := 1; i <= 15; i++ {
		msg := fmt.Sprintf("Log message line %02d padding to make size predictable\n", i)
		if _, err := rotator.Write([]byte(msg)); err != nil {
			t.Fatalf("Write %d failed: %v", i, err)
		}
	}

	if err := rotator.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify agyq.log exists
	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("base log file missing: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("expected perm 0600, got %#o", perm)
	}

	// Verify backups .1 through .5 exist
	for i := 1; i <= maxBackups; i++ {
		p := fmt.Sprintf("%s.%d", logPath, i)
		bst, err := os.Stat(p)
		if err != nil {
			t.Errorf("backup file %s missing: %v", p, err)
			continue
		}
		if perm := bst.Mode().Perm(); perm != 0600 {
			t.Errorf("expected backup %s perm 0600, got %#o", p, perm)
		}
	}

	// Verify backup .6 does NOT exist (pruned)
	p6 := fmt.Sprintf("%s.%d", logPath, maxBackups+1)
	if _, err := os.Stat(p6); !os.IsNotExist(err) {
		t.Errorf("expected backup %s to not exist, but stat returned %v", p6, err)
	}
}

func TestCompositeEventLog_LevelFiltering(t *testing.T) {
	// F-064: events.jsonl + human agyq.log; level filter in config
	dir := t.TempDir()
	logger, err := NewCustomCompositeEventLog(dir, "info", 10*1024*1024, 5)
	if err != nil {
		t.Fatalf("NewCustomCompositeEventLog failed: %v", err)
	}
	defer logger.Close()

	ctx := context.Background()

	// 1. Emit debug event -> should go to events.jsonl, but NOT agyq.log
	logger.Emit(ctx, application.Event{
		Time:   time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		Level:  "debug",
		Kind:   "worker.debug",
		Msg:    "Internal debugging detail",
		Fields: map[string]any{"debug_key": "val"},
	})

	// 2. Emit info event -> should go to both
	logger.Emit(ctx, application.Event{
		Time:   time.Date(2026, 10, 10, 12, 1, 0, 0, time.UTC),
		Level:  "info",
		Kind:   "prompt.started",
		Prompt: domain.PromptNumber(1),
		Msg:    "Starting prompt",
	})

	// 3. Emit warn event -> should go to both
	logger.Emit(ctx, application.Event{
		Time:   time.Date(2026, 10, 10, 12, 2, 0, 0, time.UTC),
		Level:  "warn",
		Kind:   "retry.delay",
		Prompt: domain.PromptNumber(1),
		Msg:    "Retrying after transient failure",
	})

	// Read events.jsonl
	events, err := ReadEventsFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadEventsFile failed: %v", err)
	}
	// All 3 events should be in events.jsonl
	if len(events) != 3 {
		t.Errorf("expected 3 events in events.jsonl, got %d", len(events))
	}

	// Read agyq.log
	humanLogBytes, err := os.ReadFile(filepath.Join(dir, "agyq.log"))
	if err != nil {
		t.Fatalf("reading agyq.log failed: %v", err)
	}
	humanLog := string(humanLogBytes)

	// debug event should NOT be in human log
	if strings.Contains(humanLog, "Internal debugging detail") {
		t.Errorf("debug message leaked into agyq.log when minLevel is info:\n%s", humanLog)
	}

	// info and warn events should be in human log
	if !strings.Contains(humanLog, "[INFO] [prompt.started] #1: Starting prompt") {
		t.Errorf("expected info message in agyq.log, got:\n%s", humanLog)
	}
	if !strings.Contains(humanLog, "[WARN] [retry.delay] #1: Retrying after transient failure") {
		t.Errorf("expected warn message in agyq.log, got:\n%s", humanLog)
	}
}
