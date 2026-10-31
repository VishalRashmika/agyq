package infrastructure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

func TestFileEventLog_1000EventsAndTornFinalLine(t *testing.T) {
	// F-012 AC: Test: 1000 events, each line valid JSON; torn final line is tolerated by the reader.
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	logger, err := NewFileEventLog(logPath)
	if err != nil {
		t.Fatalf("NewFileEventLog failed: %v", err)
	}

	ctx := context.Background()

	// Emit 1000 events
	for i := 1; i <= 1000; i++ {
		kind := "prompt.progress"
		if i == 1000 {
			kind = "prompt.succeeded" // terminal event
		}
		logger.Emit(ctx, application.Event{
			Time:   time.Date(2026, 10, 10, 12, 0, 0, i, time.UTC),
			Level:  "info",
			Kind:   kind,
			Prompt: domain.PromptNumber(i % 10),
			Msg:    fmt.Sprintf("Event message number %d", i),
			Fields: map[string]any{"iteration": i},
		})
	}

	// Verify terminal event caused at least 1 sync
	if count := logger.SyncCount(); count < 1 {
		t.Errorf("expected at least 1 sync for terminal event, got %d", count)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("logger.Close failed: %v", err)
	}

	// Check file mode 0600
	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("expected perm 0600, got %#o", perm)
	}

	// Read events back using ReadEventsFile
	events, err := ReadEventsFile(logPath)
	if err != nil {
		t.Fatalf("ReadEventsFile failed: %v", err)
	}
	if len(events) != 1000 {
		t.Fatalf("expected 1000 events, got %d", len(events))
	}
	if events[999].Kind != "prompt.succeeded" {
		t.Errorf("expected last event kind prompt.succeeded, got %s", events[999].Kind)
	}

	// Now append a torn final line (partial write / crash during emit)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	_, err = f.WriteString("{\"time\":\"2026-10-10T12:00:00Z\",\"kind\":\"prompt.start") // torn line
	_ = f.Close()

	// Reader must tolerate the torn final line and still return the 1000 valid events
	eventsAfterTorn, err := ReadEventsFile(logPath)
	if err != nil {
		t.Fatalf("expected reader to tolerate torn final line, got error: %v", err)
	}
	if len(eventsAfterTorn) != 1000 {
		t.Fatalf("expected 1000 events after torn final line, got %d", len(eventsAfterTorn))
	}
}

func TestFileEventLog_TerminalEventFsync(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")

	logger, err := NewFileEventLog(logPath)
	if err != nil {
		t.Fatalf("NewFileEventLog failed: %v", err)
	}
	defer logger.Close()

	ctx := context.Background()

	// Non-terminal event
	logger.Emit(ctx, application.Event{
		Kind:  "prompt.started",
		Level: "info",
		Msg:   "Started",
	})
	if logger.SyncCount() != 0 {
		t.Errorf("expected 0 syncs for non-terminal event, got %d", logger.SyncCount())
	}

	// Terminal events
	terminalKinds := []string{
		"prompt.succeeded",
		"prompt.failed",
		"prompt.interrupted",
		"prompt.skipped",
		"run.finished",
		"run.stopped",
		"run.fatal",
		"boot.recovered",
	}

	for idx, kind := range terminalKinds {
		logger.Emit(ctx, application.Event{
			Kind:  kind,
			Level: "info",
			Msg:   "Lifecycle event",
		})
		if logger.SyncCount() != idx+1 {
			t.Errorf("expected %d syncs after kind %s, got %d", idx+1, kind, logger.SyncCount())
		}
	}

	// Fatal level event
	currSyncs := logger.SyncCount()
	logger.Emit(ctx, application.Event{
		Kind:  "unexpected.panic",
		Level: "fatal",
		Msg:   "Fatal event",
	})
	if logger.SyncCount() != currSyncs+1 {
		t.Errorf("expected sync on Level=fatal event")
	}
}
