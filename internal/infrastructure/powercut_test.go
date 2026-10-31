package infrastructure

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

type faultInjectingRunner struct {
	failProbability float64
	rnd             *rand.Rand
	calls           int
	successes       map[domain.PromptNumber]int
}

func (r *faultInjectingRunner) Run(ctx context.Context, req application.RunRequest) (application.RawRunResult, error) {
	r.calls++
	if r.rnd.Float64() < r.failProbability {
		// Simulate hard ungraceful termination mid-turn
		return application.RawRunResult{
			ExitCode: 137, // SIGKILL exit code
			Killed:   true,
		}, nil
	}

	if r.successes == nil {
		r.successes = make(map[domain.PromptNumber]int)
	}
	r.successes[req.Prompt.Number]++

	return application.RawRunResult{
		ExitCode: 0,
		Envelope: &application.Envelope{
			Status:   "SUCCESS",
			Response: "done",
		},
	}, nil
}

func (r *faultInjectingRunner) Preflight(ctx context.Context) (application.PreflightReport, error) {
	return application.PreflightReport{
		Checks: []application.CheckResult{
			{Name: "test", Status: application.CheckPass},
		},
	}, nil
}

func TestPowerCutSimulation_50Iterations(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewFileStateStore(tmpDir, nil)
	ctx := context.Background()

	// Initial queue with 3 prompts
	prompts := []domain.Prompt{
		{Number: 1, Title: "Step 1", Body: "Execute step 1"},
		{Number: 2, Title: "Step 2", Body: "Execute step 2"},
		{Number: 3, Title: "Step 3", Body: "Execute step 3"},
	}

	cfg := application.DefaultConfig()
	cfg.StateDir = tmpDir
	cfg.MaxResumes = 50 // generous for simulation
	cfg.Retry.MaxAttempts = 10

	rnd := rand.New(rand.NewSource(42))
	runner := &faultInjectingRunner{
		failProbability: 0.6, // 60% chance of power cut mid-run
		rnd:             rnd,
	}

	// Run up to 50 iterations (simulating reboots after power-cuts)
	for i := 0; i < 50; i++ {
		state, err := store.Load(ctx)
		if err == application.ErrNoState {
			state = domain.NewRunState("sim-run", "queue.md", tmpDir, time.Now())
			_, _ = domain.MergeQueue(state, prompts)
			_ = store.Save(ctx, state)
		} else if err != nil {
			t.Fatalf("load state at iteration %d failed: %v", i, err)
		}

		// Reconcile startup simulates power-cut recovery
		state.BootCount++
		now := time.Now()
		state.UpdatedAt = now
		for j := range state.Records {
			rec := &state.Records[j]
			if rec.Status == domain.StatusRunning {
				rec.Status = domain.StatusInterrupted
				rec.UpdatedAt = now
			}
		}
		_ = store.Save(ctx, state)

		// Check if all succeeded
		allDone := true
		for _, rec := range state.Records {
			if rec.Status != domain.StatusSucceeded {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}

		// Orchestrator run
		orch := application.NewOrchestrator(
			cfg,
			nil,
			store,
			runner,
			nil,
			&fakeLocker{},
			SystemClock{},
			SystemSleeper{},
		)

		_ = orch.Run(ctx)
	}

	// Final verification
	finalState, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("failed to load final state: %v", err)
	}

	if len(finalState.Records) != 3 {
		t.Fatalf("expected 3 prompt records, got: %d", len(finalState.Records))
	}

	for _, rec := range finalState.Records {
		if rec.Status != domain.StatusSucceeded {
			t.Errorf("prompt #%d did not reach succeeded, status is: %s", rec.Number, rec.Status)
		}
		// Assert each prompt succeeded exactly once (no duplicate successes)
		if runner.successes[rec.Number] != 1 {
			t.Errorf("prompt #%d succeeded %d times, expected exactly 1", rec.Number, runner.successes[rec.Number])
		}
	}
}

type fakeLocker struct{}

func (f *fakeLocker) Acquire(ctx context.Context) (func(), error) {
	return func() {}, nil
}
