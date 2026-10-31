package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agyq/internal/application"
	"agyq/internal/infrastructure"
	"agyq/internal/presentation"
)

func TestCmd_Plan_GoldenExample(t *testing.T) {
	stateDir := t.TempDir()
	queuePath := "../../examples/queue.example.md"

	loader := func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = queuePath
		cfg.StateDir = stateDir
		return &cfg, nil
	}

	queueSource := infrastructure.NewFileQueueSource()
	stateStoreFactory := func(dir string) (application.StateStore, error) {
		return infrastructure.NewFileStateStore(dir, nil), nil
	}

	app := presentation.NewApp(nil, loader).
		WithQueueSource(queueSource).
		WithStateStoreFactory(stateStoreFactory)

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan", "--state-dir", stateDir, queuePath}, &stdout, &stderr)
	if code != presentation.ExitOK {
		t.Fatalf("expected ExitOK (0), got %d; stderr=%s", code, stderr.String())
	}

	outStr := stdout.String()

	expectedLines := []string{
		"#   STATUS   TITLE",
		"#1  pending  Scaffold the project",
		"#2  pending  Add a greeting package",
		"#3  pending  (no title)",
		"#4  pending  Colon-style title",
		"#5  pending  Dash-style title",
		"",
		"Plan: 5 prompts (5 pending)",
	}
	expectedOutput := strings.Join(expectedLines, "\n") + "\n"

	if outStr != expectedOutput {
		t.Errorf("plan output differs from golden.\nGOT:\n%s\nWANT:\n%s", outStr, expectedOutput)
	}

	// Verify state.json exists and contains 5 prompts
	store := infrastructure.NewFileStateStore(stateDir, nil)
	s1, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("failed to load state.json: %v", err)
	}
	if len(s1.Records) != 5 {
		t.Fatalf("expected 5 prompt records in state.json, got %d", len(s1.Records))
	}

	// Idempotency: re-running produces byte-identical output and idempotent state
	var stdout2, stderr2 bytes.Buffer
	code2 := app.Run([]string{"plan", "--state-dir", stateDir, queuePath}, &stdout2, &stderr2)
	if code2 != presentation.ExitOK {
		t.Fatalf("re-run failed: %d", code2)
	}
	if stdout2.String() != expectedOutput {
		t.Errorf("re-run output differs from golden")
	}

	s2, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("failed to reload state: %v", err)
	}
	for i := range s1.Records {
		if s1.Records[i].Number != s2.Records[i].Number ||
			s1.Records[i].Title != s2.Records[i].Title ||
			s1.Records[i].Hash != s2.Records[i].Hash ||
			s1.Records[i].Status != s2.Records[i].Status {
			t.Errorf("record %d changed across plan runs", i)
		}
	}
}

func TestCmd_Plan_DryRun(t *testing.T) {
	stateDir := t.TempDir()
	queuePath := "../../examples/queue.example.md"

	loader := func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = queuePath
		cfg.StateDir = stateDir
		return &cfg, nil
	}

	queueSource := infrastructure.NewFileQueueSource()
	stateStoreFactory := func(dir string) (application.StateStore, error) {
		return infrastructure.NewFileStateStore(dir, nil), nil
	}

	app := presentation.NewApp(nil, loader).
		WithQueueSource(queueSource).
		WithStateStoreFactory(stateStoreFactory)

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan", "--dry-run", "--state-dir", stateDir, queuePath}, &stdout, &stderr)
	if code != presentation.ExitOK {
		t.Fatalf("expected ExitOK, got %d", code)
	}

	if !strings.Contains(stdout.String(), "Plan: 5 prompts (5 pending) [dry-run]") {
		t.Errorf("expected dry-run note in output")
	}

	// Verify state.json was not created
	statePath := filepath.Join(stateDir, "state.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("expected state.json to not exist after dry-run")
	}
}

func TestCmd_ZeroConfig_PlanAndStatus(t *testing.T) {
	origWd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	queueContent := "# #1 First prompt\nBuild a feature\n"
	if err := os.WriteFile(filepath.Join(tmp, "queue.md"), []byte(queueContent), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	queueSource := infrastructure.NewFileQueueSource()
	stateStoreFactory := func(dir string) (application.StateStore, error) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		return infrastructure.NewFileStateStore(dir, nil), nil
	}

	eventsReader := func(dir string, limit int) ([]application.Event, error) {
		return nil, nil
	}

	app := presentation.NewApp(nil, infrastructure.LoadConfigOrDefault).
		WithQueueSource(queueSource).
		WithStateStoreFactory(stateStoreFactory).
		WithEventsReader(eventsReader)

	// 1. Run plan with zero args
	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan"}, &stdout, &stderr)
	if code != presentation.ExitOK {
		t.Fatalf("expected ExitOK for zero-config plan, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#1  pending  First prompt") {
		t.Errorf("plan output missing prompt: %s", stdout.String())
	}

	// 2. Run status with zero args
	stdout.Reset()
	stderr.Reset()
	code = app.Run([]string{"status"}, &stdout, &stderr)
	if code != presentation.ExitOK {
		t.Fatalf("expected ExitOK for zero-config status, got %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "#1") || !strings.Contains(stdout.String(), "pending") || !strings.Contains(stdout.String(), "First prompt") {
		t.Errorf("status output missing prompt: %s", stdout.String())
	}

	// 3. Verify .gitignore was auto-updated with .agyq/
	giData, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}
	if !strings.Contains(string(giData), ".agyq/") {
		t.Errorf("expected .gitignore to contain .agyq/, got: %s", string(giData))
	}
}
