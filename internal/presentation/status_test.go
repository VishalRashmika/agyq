package presentation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

func TestStatus_GoldenTableOutput(t *testing.T) {
	now := time.Date(2026, 10, 11, 10, 30, 0, 0, time.UTC)
	updatedTime := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	pauseUntil := time.Date(2026, 10, 11, 11, 0, 0, 0, time.UTC)

	sampleState := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "run-golden-1",
		QueueFile:     "/path/to/queue.md",
		WorkDir:       "/path/to/workdir",
		BootCount:     3,
		Pause: &domain.Pause{
			Reason: domain.PauseLimit,
			Until:  pauseUntil,
		},
		Records: []domain.PromptRecord{
			{
				Number:    1,
				Title:     "Build parser",
				Status:    domain.StatusSucceeded,
				Attempts:  []domain.Attempt{{N: 1}},
				UpdatedAt: updatedTime,
			},
			{
				Number:    2,
				Title:     "Add runner",
				Status:    domain.StatusWaitingLimit,
				Attempts:  []domain.Attempt{{N: 1}},
				UpdatedAt: updatedTime,
			},
			{
				Number:    3,
				Title:     "Write docs",
				Status:    domain.StatusPending,
				Attempts:  []domain.Attempt{},
				UpdatedAt: updatedTime,
			},
		},
	}

	sampleEvents := []application.Event{
		{
			Time:   time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC),
			Level:  "info",
			Kind:   "prompt.started",
			Prompt: 1,
			Msg:    "Started prompt #1",
		},
		{
			Time:   time.Date(2026, 10, 11, 10, 10, 0, 0, time.UTC),
			Level:  "info",
			Kind:   "prompt.succeeded",
			Prompt: 1,
			Msg:    "Prompt #1 succeeded",
		},
	}

	app := NewApp(nil, nil).
		WithStateStoreFactory(func(stateDir string) (application.StateStore, error) {
			return &mockStateStore{state: sampleState}, nil
		}).
		WithEventsReader(func(stateDir string, limit int) ([]application.Event, error) {
			return sampleEvents, nil
		}).
		WithClock(&fixedClock{now: now})

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"status"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit code got %d, want 0 (stderr: %s)", code, stderr.String())
	}

	out := stdout.String()

	expectedLines := []string{
		"#   STATUS         ATTEMPTS  LAST CHANGE              TITLE",
		"#1  succeeded      1         2026-10-11 10:00:00 UTC  Build parser",
		"#2  waiting_limit  1         2026-10-11 10:00:00 UTC  Add runner",
		"#3  pending        0         2026-10-11 10:00:00 UTC  Write docs",
		"",
		"Pause: waiting until 11:00:00 UTC (reason: limit, 30m0s remaining)",
		"",
		"Boot count: 3",
		"",
		"Recent events:",
		"  10:00:00 [info] prompt.started #1: Started prompt #1",
		"  10:10:00 [info] prompt.succeeded #1: Prompt #1 succeeded",
	}
	expectedOutput := strings.Join(expectedLines, "\n") + "\n"

	if out != expectedOutput {
		t.Errorf("status output does not match golden.\nGOT:\n%s\nWANT:\n%s", out, expectedOutput)
	}
}

func TestStatus_GoldenJSONOutput(t *testing.T) {
	now := time.Date(2026, 10, 11, 10, 30, 0, 0, time.UTC)
	sampleState := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "run-json-1",
		BootCount:     1,
		Records: []domain.PromptRecord{
			{Number: 1, Title: "Task JSON", Status: domain.StatusSucceeded},
		},
	}
	sampleEvents := []application.Event{
		{Time: now, Level: "info", Kind: "prompt.succeeded", Prompt: 1, Msg: "done"},
	}

	app := NewApp(nil, nil).
		WithStateStoreFactory(func(stateDir string) (application.StateStore, error) {
			return &mockStateStore{state: sampleState}, nil
		}).
		WithEventsReader(func(stateDir string, limit int) ([]application.Event, error) {
			return sampleEvents, nil
		}).
		WithClock(&fixedClock{now: now})

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"status", "--json"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit code got %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var parsed struct {
		State        domain.RunState     `json:"state"`
		RecentEvents []application.Event `json:"recent_events"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid json output: %v, raw:\n%s", err, stdout.String())
	}

	if parsed.State.RunID != "run-json-1" {
		t.Errorf("run_id got %q, want run-json-1", parsed.State.RunID)
	}
	if len(parsed.RecentEvents) != 1 || parsed.RecentEvents[0].Kind != "prompt.succeeded" {
		t.Errorf("recent events got: %+v", parsed.RecentEvents)
	}
}
