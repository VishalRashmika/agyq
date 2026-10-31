package infrastructure

import (
	"context"
	"testing"
	"time"

	"agyq/internal/application"
)

func TestFileControlChannel_WriteAndPollFIFO(t *testing.T) {
	tempDir := t.TempDir()

	ch := NewFileControlChannel(tempDir)
	ctx := context.Background()

	// Initial poll on empty directory
	cmd, ok := ch.Poll(ctx)
	if ok {
		t.Fatalf("expected ok=false on empty control dir, got cmd: %+v", cmd)
	}

	// Write two commands in sequence: pause then resume
	cmd1 := application.ControlCommand{
		ID:        "cmd-1",
		Kind:      application.CommandPause,
		CreatedAt: time.Now().UTC(),
	}
	if err := WriteControlCommand(tempDir, cmd1); err != nil {
		t.Fatalf("WriteControlCommand(cmd1) failed: %v", err)
	}

	// Small sleep to ensure nanosecond timestamp ordering
	time.Sleep(5 * time.Millisecond)

	cmd2 := application.ControlCommand{
		ID:        "cmd-2",
		Kind:      application.CommandResume,
		CreatedAt: time.Now().UTC(),
	}
	if err := WriteControlCommand(tempDir, cmd2); err != nil {
		t.Fatalf("WriteControlCommand(cmd2) failed: %v", err)
	}

	// Poll first command -> should be cmd1 (FIFO)
	polled1, ok1 := ch.Poll(ctx)
	if !ok1 || polled1.Kind != application.CommandPause || polled1.ID != "cmd-1" {
		t.Fatalf("expected polled1=cmd-1/pause, got: ok=%v, cmd=%+v", ok1, polled1)
	}

	// Poll second command -> should be cmd2 (FIFO)
	polled2, ok2 := ch.Poll(ctx)
	if !ok2 || polled2.Kind != application.CommandResume || polled2.ID != "cmd-2" {
		t.Fatalf("expected polled2=cmd-2/resume, got: ok=%v, cmd=%+v", ok2, polled2)
	}

	// Poll again -> should be empty (all consumed and deleted)
	_, ok3 := ch.Poll(ctx)
	if ok3 {
		t.Fatalf("expected ok=false after all commands consumed")
	}
}
