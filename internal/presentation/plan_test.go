package presentation

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

type mockQueueSource struct {
	prompts []domain.Prompt
	err     error
}

func (m *mockQueueSource) Load(ctx context.Context, path string) ([]domain.Prompt, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.prompts, nil
}

type mockStateStore struct {
	state *domain.RunState
	saved *domain.RunState
}

func (m *mockStateStore) Load(ctx context.Context) (*domain.RunState, error) {
	if m.state == nil {
		return nil, application.ErrNoState
	}
	return m.state, nil
}

func (m *mockStateStore) Save(ctx context.Context, s *domain.RunState) error {
	m.saved = s
	m.state = s
	return nil
}

func TestPlan_GoldenOutputFormat(t *testing.T) {
	// Golden test of CLI table formatting with 5 prompts
	prompts := []domain.Prompt{
		{Number: 1, Title: "Scaffold the project", Body: "Body 1", Hash: "h1"},
		{Number: 2, Title: "Add a greeting package", Body: "Body 2", Hash: "h2"},
		{Number: 3, Title: "", Body: "Body 3", Hash: "h3"}, // empty title
		{Number: 4, Title: "Colon-style title", Body: "Body 4", Hash: "h4"},
		{Number: 5, Title: "Dash-style title", Body: "Body 5", Hash: "h5"},
	}

	qs := &mockQueueSource{prompts: prompts}
	store := &mockStateStore{}

	loader := func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = "queue.md"
		cfg.StateDir = ".agyq"
		return &cfg, nil
	}

	app := NewApp(nil, loader).
		WithQueueSource(qs).
		WithStateStoreFactory(func(dir string) (application.StateStore, error) {
			return store, nil
		})

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan", "queue.md"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d; stderr=%s", code, stderr.String())
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
		t.Errorf("plan output does not match golden.\nGOT:\n%s\nWANT:\n%s", outStr, expectedOutput)
	}

	// Verify state was saved
	if store.saved == nil || len(store.saved.Records) != 5 {
		t.Fatalf("expected 5 records saved to state")
	}

	// Re-run is idempotent
	var stdout2, stderr2 bytes.Buffer
	code2 := app.Run([]string{"plan", "queue.md"}, &stdout2, &stderr2)
	if code2 != ExitOK {
		t.Fatalf("re-run failed: %d", code2)
	}
	if stdout2.String() != expectedOutput {
		t.Errorf("re-run output differed from golden")
	}
}

func TestPlan_DryRun(t *testing.T) {
	prompts := []domain.Prompt{
		{Number: 1, Title: "Test task", Body: "Body", Hash: "h1"},
	}
	qs := &mockQueueSource{prompts: prompts}
	store := &mockStateStore{}

	app := NewApp(nil, func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = "queue.md"
		return &cfg, nil
	}).
		WithQueueSource(qs).
		WithStateStoreFactory(func(dir string) (application.StateStore, error) {
			return store, nil
		})

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan", "--dry-run", "queue.md"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d", code)
	}

	if !strings.Contains(stdout.String(), "Plan: 1 prompts (1 pending) [dry-run]") {
		t.Errorf("expected dry-run note in output, got:\n%s", stdout.String())
	}

	// Verify state was NOT saved
	if store.saved != nil {
		t.Errorf("expected state not to be saved in --dry-run mode")
	}
}

func TestPlan_ValidationErrors(t *testing.T) {
	valErr := &domain.QueueValidationError{
		Errors: []string{
			"line 1: prompt #1 has an empty body",
			"line 2: prompt #0 is out of order",
		},
	}
	qs := &mockQueueSource{err: valErr}
	store := &mockStateStore{}

	app := NewApp(nil, func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = "queue.md"
		return &cfg, nil
	}).
		WithQueueSource(qs).
		WithStateStoreFactory(func(dir string) (application.StateStore, error) {
			return store, nil
		})

	var stdout, stderr bytes.Buffer
	code := app.Run([]string{"plan", "queue.md"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (%d), got %d", ExitUsage, code)
	}

	errStr := stderr.String()
	if !strings.Contains(errStr, "queue validation failed") {
		t.Errorf("missing validation failure header: %s", errStr)
	}
	if !strings.Contains(errStr, "line 1: prompt #1 has an empty body") {
		t.Errorf("missing error detail: %s", errStr)
	}
}

type fixedClock struct {
	now time.Time
}

func (f *fixedClock) Now() time.Time {
	return f.now
}

func TestPlanService_ExecuteUnit(t *testing.T) {
	qs := &mockQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Alpha", Body: "Do alpha", Hash: "hash1"},
			{Number: 2, Title: "Beta", Body: "Do beta", Hash: "hash2"},
		},
	}
	ss := &mockStateStore{}
	fixed := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{now: fixed}

	svc := application.NewPlanService(qs, ss, clk)
	ctx := context.Background()

	res, err := svc.Execute(ctx, application.PlanRequest{
		QueueFile: "queue.md",
		WorkDir:   "/work",
		StateDir:  "/state",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(res.State.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(res.State.Records))
	}
	if ss.saved == nil {
		t.Fatalf("expected state to be saved")
	}
	if !ss.saved.UpdatedAt.Equal(fixed) {
		t.Errorf("expected UpdatedAt %v, got %v", fixed, ss.saved.UpdatedAt)
	}
}

func TestPlanService_MissingQueueFile(t *testing.T) {
	svc := application.NewPlanService(&mockQueueSource{}, &mockStateStore{}, nil)
	_, err := svc.Execute(context.Background(), application.PlanRequest{})
	if err == nil {
		t.Fatalf("expected error for empty queue file")
	}
}
