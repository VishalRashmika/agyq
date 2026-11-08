package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyq/internal/domain"
)

// In-memory test doubles

type memStateStore struct {
	mu    sync.Mutex
	state *domain.RunState
}

func (m *memStateStore) Load(ctx context.Context) (*domain.RunState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil, ErrNoState
	}
	return m.state, nil
}

func (m *memStateStore) Save(ctx context.Context, s *domain.RunState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	return nil
}

type memQueueSource struct {
	mu      sync.Mutex
	prompts []domain.Prompt
}

func (m *memQueueSource) Load(ctx context.Context, path string) ([]domain.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Prompt, len(m.prompts))
	copy(out, m.prompts)
	return out, nil
}

func (m *memQueueSource) AppendPrompt(p domain.Prompt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prompts = append(m.prompts, p)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Add(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

type fakeSleeper struct {
	mu     sync.Mutex
	sleeps []time.Duration
	clock  *fakeClock
}

func (f *fakeSleeper) Sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleeps = append(f.sleeps, d)
	if f.clock != nil {
		f.clock.Add(d)
	}
	return nil
}

type fakeLocker struct{}

func (f *fakeLocker) Acquire(ctx context.Context) (func(), error) {
	return func() {}, nil
}

type fakeEventLog struct {
	events []Event
}

func (f *fakeEventLog) Emit(ctx context.Context, e Event) {
	f.events = append(f.events, e)
}

type fakeErrorLog struct {
	entries []ErrorEntry
}

func (f *fakeErrorLog) LogError(ctx context.Context, entry ErrorEntry) {
	f.entries = append(f.entries, entry)
}

type concurrencyTrackerRunner struct {
	activeCount int32
	maxActive   int32
	turnHandler func(req RunRequest) (RawRunResult, error)
}

func (r *concurrencyTrackerRunner) Run(ctx context.Context, req RunRequest) (RawRunResult, error) {
	cur := atomic.AddInt32(&r.activeCount, 1)
	defer atomic.AddInt32(&r.activeCount, -1)

	// Update max concurrency observed
	for {
		m := atomic.LoadInt32(&r.maxActive)
		if cur <= m {
			break
		}
		if atomic.CompareAndSwapInt32(&r.maxActive, m, cur) {
			break
		}
	}

	if r.turnHandler != nil {
		return r.turnHandler(req)
	}

	return RawRunResult{
		ExitCode: 0,
		Envelope: &Envelope{
			Status:   "SUCCESS",
			Response: "done",
		},
	}, nil
}

func (r *concurrencyTrackerRunner) Preflight(ctx context.Context) (PreflightReport, error) {
	return PreflightReport{}, nil
}

func TestOrchestrator_F030_SequentialLoopAndMaxConcurrencyOne(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	prompts := make([]domain.Prompt, 5)
	for i := 0; i < 5; i++ {
		prompts[i] = domain.Prompt{
			Number: domain.PromptNumber(i + 1),
			Title:  "Task",
			Body:   "Body",
			Hash:   domain.ComputePromptHash("Task", "Body"),
		}
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	runner := &concurrencyTrackerRunner{}
	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	sl := &fakeSleeper{}
	el := &fakeEventLog{}

	orch := NewOrchestrator(cfg, qs, ss, runner, el, &fakeLocker{}, clk, sl)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify all 5 prompts succeeded
	if ss.state == nil || len(ss.state.Records) != 5 {
		t.Fatalf("expected 5 records in state, got: %v", ss.state)
	}
	for _, rec := range ss.state.Records {
		if rec.Status != domain.StatusSucceeded {
			t.Errorf("prompt #%d status got %s, want succeeded", rec.Number, rec.Status)
		}
	}

	// Verify max concurrency == 1 (P-1)
	if runner.maxActive != 1 {
		t.Errorf("max concurrency observed was %d, expected strictly 1", runner.maxActive)
	}
}

func TestOrchestrator_F032_RetryPolicyTransient(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"
	cfg.Retry.MaxAttempts = 3
	cfg.Retry.Backoff = []time.Duration{10 * time.Second, 30 * time.Second}

	prompts := []domain.Prompt{
		{Number: 1, Title: "Transient task", Body: "work", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	sl := &fakeSleeper{}

	// Fail twice with transient, then succeed on 3rd attempt
	callCount := 0
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCount++
			if callCount < 3 {
				return RawRunResult{
					ExitCode: 1,
					Stderr:   []byte("503 service unavailable, try again"),
				}, nil
			}
			return RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{Status: "SUCCESS"},
			}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, sl)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	rec := ss.state.Records[0]
	if rec.Status != domain.StatusSucceeded {
		t.Errorf("expected succeeded after 3 attempts, got: %s", rec.Status)
	}
	if len(rec.Attempts) != 3 {
		t.Fatalf("expected 3 attempt records, got: %d", len(rec.Attempts))
	}
	if rec.Attempts[0].Outcome != domain.OutcomeTransient || rec.Attempts[1].Outcome != domain.OutcomeTransient {
		t.Errorf("attempts 1 and 2 should be transient, got: %v, %v", rec.Attempts[0].Outcome, rec.Attempts[1].Outcome)
	}
	if rec.Attempts[2].Outcome != domain.OutcomeSuccess {
		t.Errorf("attempt 3 should be success, got: %v", rec.Attempts[2].Outcome)
	}
	// Verify backoff sleeps were called
	if len(sl.sleeps) != 2 {
		t.Errorf("expected 2 backoff sleeps, got: %d", len(sl.sleeps))
	}
}

func TestOrchestrator_F033_FailurePolicyStopVsContinue(t *testing.T) {
	prompts := []domain.Prompt{
		{Number: 1, Title: "Fatal task", Body: "bad", Hash: "h1"},
		{Number: 2, Title: "Next task", Body: "good", Hash: "h2"},
	}

	t.Run("on_failure stop halts queue and returns ErrQueueHalted", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.QueueFile = "queue.md"
		cfg.OnFailure = FailureStop

		qs := &memQueueSource{prompts: prompts}
		ss := &memStateStore{}
		runner := &concurrencyTrackerRunner{
			turnHandler: func(req RunRequest) (RawRunResult, error) {
				if req.Prompt.Number == 1 {
					return RawRunResult{
						ExitCode: 127, // fatal
						Stderr:   []byte("binary not found"),
					}, nil
				}
				return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
			},
		}

		orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
		err := orch.Run(context.Background())
		if !errors.Is(err, ErrQueueHalted) {
			t.Fatalf("expected ErrQueueHalted, got: %v", err)
		}

		// Prompt 1 is failed, Prompt 2 never started (pending)
		if ss.state.Records[0].Status != domain.StatusFailed {
			t.Errorf("prompt 1 status got %s, want failed", ss.state.Records[0].Status)
		}
		if ss.state.Records[1].Status != domain.StatusPending {
			t.Errorf("prompt 2 status got %s, want pending", ss.state.Records[1].Status)
		}
	})

	t.Run("on_failure continue marks failed and proceeds to next", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.QueueFile = "queue.md"
		cfg.OnFailure = FailureContinue

		qs := &memQueueSource{prompts: prompts}
		ss := &memStateStore{}
		runner := &concurrencyTrackerRunner{
			turnHandler: func(req RunRequest) (RawRunResult, error) {
				if req.Prompt.Number == 1 {
					return RawRunResult{ExitCode: 127, Stderr: []byte("binary not found")}, nil
				}
				return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
			},
		}

		orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
		err := orch.Run(context.Background())
		if err != nil {
			t.Fatalf("expected nil error on continue, got: %v", err)
		}

		if ss.state.Records[0].Status != domain.StatusFailed {
			t.Errorf("prompt 1 status got %s, want failed", ss.state.Records[0].Status)
		}
		if ss.state.Records[1].Status != domain.StatusSucceeded {
			t.Errorf("prompt 2 status got %s, want succeeded", ss.state.Records[1].Status)
		}
	})

	t.Run("on_failure retry-at-end (default) skips failed prompt, completes remaining, retries at end and succeeds", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.QueueFile = "queue.md"
		// Default is FailureRetryAtEnd

		qs := &memQueueSource{prompts: prompts}
		ss := &memStateStore{}
		var promptExecutionOrder []int
		var attemptModes []domain.RunMode
		var preambles []string

		runner := &concurrencyTrackerRunner{
			turnHandler: func(req RunRequest) (RawRunResult, error) {
				promptExecutionOrder = append(promptExecutionOrder, int(req.Prompt.Number))
				attemptModes = append(attemptModes, req.Mode)
				preambles = append(preambles, req.Preamble)

				if req.Prompt.Number == 1 {
					if req.AttemptNumber == 1 {
						return RawRunResult{ExitCode: 127, Stderr: []byte("initial build error")}, nil
					}
					// Attempt 2 (retry at end): succeeds
					return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
				}
				// Prompt 2 succeeds
				return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
			},
		}

		orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
		err := orch.Run(context.Background())
		if err != nil {
			t.Fatalf("expected nil error on retry-at-end success, got: %v", err)
		}

		// Prompt execution order must be: Prompt 1 (fail), Prompt 2 (success), Prompt 1 (retry, success)
		expectedOrder := []int{1, 2, 1}
		if !reflect.DeepEqual(promptExecutionOrder, expectedOrder) {
			t.Errorf("execution order got %v, want %v", promptExecutionOrder, expectedOrder)
		}

		// Second attempt at prompt 1 should run in RunResume mode with FailedRetryPreamble
		if attemptModes[2] != domain.RunResume {
			t.Errorf("retry attempt mode got %v, want RunResume", attemptModes[2])
		}
		if !strings.Contains(preambles[2], "NOTE FROM THE AUTOMATION RUNNER") || !strings.Contains(preambles[2], "initial build error") {
			t.Errorf("retry preamble missing expected error context: %s", preambles[2])
		}

		// Both prompts ultimately succeeded!
		if ss.state.Records[0].Status != domain.StatusSucceeded {
			t.Errorf("prompt 1 status got %s, want succeeded", ss.state.Records[0].Status)
		}
		if ss.state.Records[1].Status != domain.StatusSucceeded {
			t.Errorf("prompt 2 status got %s, want succeeded", ss.state.Records[1].Status)
		}
		if !ss.state.Records[0].RetriedAtEnd {
			t.Errorf("expected RetriedAtEnd=true on prompt 1")
		}
	})

	t.Run("on_failure retry-at-end exhausts retry, logs error, and finishes queue", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.QueueFile = "queue.md"

		qs := &memQueueSource{prompts: prompts}
		ss := &memStateStore{}
		errLogger := &fakeErrorLog{}
		var promptExecutionOrder []int

		runner := &concurrencyTrackerRunner{
			turnHandler: func(req RunRequest) (RawRunResult, error) {
				promptExecutionOrder = append(promptExecutionOrder, int(req.Prompt.Number))
				if req.Prompt.Number == 1 {
					return RawRunResult{ExitCode: 127, Stderr: []byte("persistent compilation failure")}, nil
				}
				return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
			},
		}

		orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{}).
			WithErrorLogger(errLogger)

		err := orch.Run(context.Background())
		if err != nil {
			t.Fatalf("expected clean finish (nil err) despite prompt failure, got: %v", err)
		}

		// Execution order: 1 (fail), 2 (succeed), 1 (retry fail)
		expectedOrder := []int{1, 2, 1}
		if !reflect.DeepEqual(promptExecutionOrder, expectedOrder) {
			t.Errorf("execution order got %v, want %v", promptExecutionOrder, expectedOrder)
		}

		// Prompt 1 failed after retry; Prompt 2 succeeded; queue finished cleanly
		if ss.state.Records[0].Status != domain.StatusFailed {
			t.Errorf("prompt 1 status got %s, want failed", ss.state.Records[0].Status)
		}
		if !ss.state.Records[0].RetriedAtEnd {
			t.Errorf("expected RetriedAtEnd=true on prompt 1")
		}
		if ss.state.Records[1].Status != domain.StatusSucceeded {
			t.Errorf("prompt 2 status got %s, want succeeded", ss.state.Records[1].Status)
		}

		// Verify errors logged
		if len(errLogger.entries) < 2 {
			t.Fatalf("expected at least 2 error log entries for prompt 1 attempts, got %d", len(errLogger.entries))
		}
	})
}

func TestOrchestrator_F034_GracefulShutdownPersistsInterrupted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	prompts := []domain.Prompt{
		{Number: 1, Title: "Long task", Body: "long", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}

	ctx, cancel := context.WithCancel(context.Background())

	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			cancel() // simulate SIGINT/SIGTERM mid-turn
			return RawRunResult{
				ExitCode: 137,
				Killed:   true,
			}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
	err := orch.Run(ctx)
	if err != nil {
		t.Fatalf("expected clean return (nil error) on graceful shutdown, got: %v", err)
	}

	rec := ss.state.Records[0]
	if rec.Status != domain.StatusInterrupted {
		t.Errorf("expected prompt marked interrupted, got: %s", rec.Status)
	}
	if len(rec.Attempts) != 1 || rec.Attempts[0].Outcome != domain.OutcomeInterrupted {
		t.Errorf("expected attempt recorded with OutcomeInterrupted: %+v", rec.Attempts)
	}
}

func TestOrchestrator_F035_SingleInFlightInvariantCheck(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	// Construct corrupt state with TWO non-terminal prompts with started attempts
	now := time.Now()
	corruptState := &domain.RunState{
		SchemaVersion: 1,
		Records: []domain.PromptRecord{
			{
				Number: 1,
				Status: domain.StatusRunning,
				Attempts: []domain.Attempt{
					{N: 1, StartedAt: now},
				},
			},
			{
				Number: 2,
				Status: domain.StatusRunning,
				Attempts: []domain.Attempt{
					{N: 1, StartedAt: now},
				},
			},
		},
	}

	ss := &memStateStore{state: corruptState}
	qs := &memQueueSource{}
	runnerCalled := false
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			runnerCalled = true
			return RawRunResult{}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
	err := orch.Run(context.Background())

	if !errors.Is(err, ErrInvariantViolation) {
		t.Fatalf("expected ErrInvariantViolation, got: %v", err)
	}
	if runnerCalled {
		t.Fatal("runner was called despite invariant violation!")
	}
}

func TestOrchestrator_F005_LiveQueueReload(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	initialPrompts := []domain.Prompt{
		{Number: 1, Title: "Prompt 1", Body: "Task 1", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: initialPrompts}
	ss := &memStateStore{}

	// After Prompt 1 runs, simulate queue file growth by appending Prompt 2
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			if req.Prompt.Number == 1 {
				// While prompt 1 is running / finishes, user appends Prompt 2 to queue file
				qs.AppendPrompt(domain.Prompt{
					Number: 2,
					Title:  "Prompt 2",
					Body:   "Task 2",
					Hash:   "h2",
				})
			}
			return RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{Status: "SUCCESS"},
			}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify that both prompt 1 AND prompt 2 were processed and succeeded!
	if len(ss.state.Records) != 2 {
		t.Fatalf("expected 2 prompts in state after live reload, got: %d", len(ss.state.Records))
	}
	if ss.state.Records[0].Status != domain.StatusSucceeded || ss.state.Records[1].Status != domain.StatusSucceeded {
		t.Errorf("expected both prompts succeeded: p1=%s, p2=%s", ss.state.Records[0].Status, ss.state.Records[1].Status)
	}
}

func TestOrchestrator_F055_ResumeAttemptCap(t *testing.T) {
	// Scenario 1: OnFailure == FailureStop
	cfg := DefaultConfig()
	cfg.MaxResumes = 5
	cfg.OnFailure = FailureStop

	// Construct state with a prompt that was interrupted 6 times (exceeding 5)
	attempts := make([]domain.Attempt, 6)
	for i := 0; i < 6; i++ {
		attempts[i] = domain.Attempt{
			N:       i + 1,
			Outcome: domain.OutcomeInterrupted,
		}
	}

	state := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-run-cap",
		Records: []domain.PromptRecord{
			{
				Number:   1,
				Title:    "Poison Prompt",
				Status:   domain.StatusInterrupted,
				Attempts: attempts,
			},
		},
	}

	ss := &memStateStore{state: state}
	qs := &memQueueSource{}
	agyCalls := 0
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			agyCalls++
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}
	events := &fakeEventLog{}
	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})

	err := orch.Run(context.Background())
	if !errors.Is(err, ErrQueueHalted) {
		t.Fatalf("expected ErrQueueHalted on cap exceeded, got: %v", err)
	}
	if agyCalls != 0 {
		t.Errorf("expected 0 agy calls for poison prompt exceeding cap, got: %d", agyCalls)
	}

	if ss.state.Records[0].Status != domain.StatusFailed {
		t.Errorf("expected prompt status failed, got: %s", ss.state.Records[0].Status)
	}
	if ss.state.Records[0].Attempts[5].Error != "too_many_interruptions" {
		t.Errorf("expected error 'too_many_interruptions', got: %q", ss.state.Records[0].Attempts[5].Error)
	}

	// Verify event was emitted
	foundEvent := false
	for _, e := range events.events {
		if e.Kind == "prompt.failed" && e.Fields["reason"] == "too_many_interruptions" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Errorf("expected prompt.failed event with reason=too_many_interruptions")
	}

	// Scenario 2: OnFailure == FailureContinue moves on to next prompt
	cfgContinue := DefaultConfig()
	cfgContinue.MaxResumes = 2
	cfgContinue.OnFailure = FailureContinue

	state2 := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-run-cap-continue",
		Records: []domain.PromptRecord{
			{
				Number: 1,
				Title:  "Poison Prompt",
				Status: domain.StatusInterrupted,
				Attempts: []domain.Attempt{
					{N: 1, Outcome: domain.OutcomeInterrupted},
					{N: 2, Outcome: domain.OutcomeInterrupted},
					{N: 3, Outcome: domain.OutcomeInterrupted}, // 3 > 2 cap
				},
			},
			{
				Number: 2,
				Title:  "Healthy Prompt",
				Status: domain.StatusPending,
			},
		},
	}

	ss2 := &memStateStore{state: state2}
	qs2 := &memQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Poison Prompt", Body: "Task 1"},
			{Number: 2, Title: "Healthy Prompt", Body: "Task 2"},
		},
	}
	runner2 := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			if req.Prompt.Number == 2 {
				return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
			}
			t.Errorf("unexpected agy call for prompt #%d", req.Prompt.Number)
			return RawRunResult{}, nil
		},
	}
	orch2 := NewOrchestrator(cfgContinue, qs2, ss2, runner2, &fakeEventLog{}, &fakeLocker{}, &fakeClock{}, &fakeSleeper{})
	if err := orch2.Run(context.Background()); err != nil {
		t.Fatalf("orch2.Run failed: %v", err)
	}

	if ss2.state.Records[0].Status != domain.StatusFailed {
		t.Errorf("expected prompt 1 failed, got: %s", ss2.state.Records[0].Status)
	}
	if ss2.state.Records[1].Status != domain.StatusSucceeded {
		t.Errorf("expected prompt 2 succeeded, got: %s", ss2.state.Records[1].Status)
	}
}

type advanceClockSleeper struct {
	clock   *fakeClock
	advance time.Duration
	sleeps  []time.Duration
}

func (s *advanceClockSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.sleeps = append(s.sleeps, d)
	if s.clock != nil {
		s.clock.now = s.clock.now.Add(s.advance)
	}
	return nil
}

func TestOrchestrator_F054_ClockTrustGuard(t *testing.T) {
	stateUpdatedAt := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	// Scenario 1: Clock is suspect (10 minutes behind state.UpdatedAt), catches up after 2 slices
	cfg := DefaultConfig()
	state := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-run-clock",
		UpdatedAt:     stateUpdatedAt,
		Records: []domain.PromptRecord{
			{Number: 1, Title: "P1", Status: domain.StatusPending},
		},
	}
	ss := &memStateStore{state: state}
	qs := &memQueueSource{
		prompts: []domain.Prompt{{Number: 1, Title: "P1", Body: "Task 1"}},
	}
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}
	events := &fakeEventLog{}

	// Clock initially 10 minutes earlier than state.UpdatedAt (exceeds 2m threshold)
	clk := &fakeClock{now: stateUpdatedAt.Add(-10 * time.Minute)}
	// Each 15s sleep advances clock by 6 minutes, so after 2 sleeps clock is at +2m after stateUpdatedAt
	sleeper := &advanceClockSleeper{
		clock:   clk,
		advance: 6 * time.Minute,
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, clk, sleeper)
	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify slices were 15 seconds
	if len(sleeper.sleeps) != 2 {
		t.Fatalf("expected 2 sleeps before clock recovered, got: %d", len(sleeper.sleeps))
	}
	for _, sl := range sleeper.sleeps {
		if sl != 15*time.Second {
			t.Errorf("expected 15s sleep slice, got %v", sl)
		}
	}

	// Verify events: clock.suspect then clock.recovered
	var eventKinds []string
	for _, e := range events.events {
		eventKinds = append(eventKinds, e.Kind)
	}
	hasSuspect := false
	hasRecovered := false
	for _, k := range eventKinds {
		if k == "clock.suspect" {
			hasSuspect = true
		}
		if k == "clock.recovered" {
			hasRecovered = true
		}
	}
	if !hasSuspect || !hasRecovered {
		t.Errorf("expected clock.suspect and clock.recovered events, got: %v", eventKinds)
	}

	// Scenario 2: Clock is normal (now is after state.UpdatedAt)
	clkNormal := &fakeClock{now: stateUpdatedAt.Add(1 * time.Minute)}
	sleeperNormal := &fakeSleeper{}
	eventsNormal := &fakeEventLog{}
	stateNormal := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-run-clock-normal",
		UpdatedAt:     stateUpdatedAt,
		Records: []domain.PromptRecord{
			{Number: 1, Title: "P1", Status: domain.StatusPending},
		},
	}
	ssNormal := &memStateStore{state: stateNormal}
	orchNormal := NewOrchestrator(cfg, qs, ssNormal, runner, eventsNormal, &fakeLocker{}, clkNormal, sleeperNormal)
	if err := orchNormal.Run(context.Background()); err != nil {
		t.Fatalf("orchNormal.Run failed: %v", err)
	}
	for _, e := range eventsNormal.events {
		if e.Kind == "clock.suspect" {
			t.Errorf("did not expect clock.suspect event for normal clock")
		}
	}
	if len(sleeperNormal.sleeps) != 0 {
		t.Errorf("expected 0 sleeps for normal clock, got: %d", len(sleeperNormal.sleeps))
	}
}

type fakeStrayCleaner struct {
	cleanedWorkDir string
}

func (f *fakeStrayCleaner) CleanStrayProcesses(ctx context.Context, workDir string) error {
	f.cleanedWorkDir = workDir
	return nil
}

func TestOrchestrator_F050_StartupReconcile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkDir = "/tmp/fake-work-dir"

	// Construct state with a prompt mid-run (status == running)
	startedAt := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	initialState := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-reconcile-boot",
		BootCount:     0,
		UpdatedAt:     startedAt,
		Records: []domain.PromptRecord{
			{
				Number: 1,
				Title:  "Interrupted Mid Run",
				Status: domain.StatusRunning,
				Attempts: []domain.Attempt{
					{
						N:         1,
						StartedAt: startedAt,
						Mode:      domain.RunFresh,
					},
				},
			},
		},
	}

	ss := &memStateStore{state: initialState}
	qs := &memQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Interrupted Mid Run", Body: "Finish the work"},
		},
	}
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			// Verify it resumes with Mode == RunResume
			if req.Mode != domain.RunResume {
				t.Errorf("expected resumed prompt to run in RunResume mode, got: %s", req.Mode)
			}
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}
	events := &fakeEventLog{}
	cleaner := &fakeStrayCleaner{}
	clk := &fakeClock{now: startedAt.Add(5 * time.Minute)}

	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, clk, &fakeSleeper{}).
		WithStrayProcessCleaner(cleaner)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// 1. Verify cleaner called with workDir
	if cleaner.cleanedWorkDir != "/tmp/fake-work-dir" {
		t.Errorf("expected cleaner called with /tmp/fake-work-dir, got: %q", cleaner.cleanedWorkDir)
	}

	// 2. Verify BootCount incremented
	if ss.state.BootCount != 1 {
		t.Errorf("expected BootCount == 1, got: %d", ss.state.BootCount)
	}

	// 3. Verify boot.recovered event was emitted listing prompt 1
	var bootRecoveredEvent *Event
	for _, e := range events.events {
		if e.Kind == "boot.recovered" {
			bootRecoveredEvent = &e
			break
		}
	}
	if bootRecoveredEvent == nil {
		t.Fatal("expected boot.recovered event to be emitted")
	}
	recoveredList, ok := bootRecoveredEvent.Fields["recovered"].([]domain.PromptNumber)
	if !ok || len(recoveredList) != 1 || recoveredList[0] != 1 {
		t.Errorf("expected recovered list [1], got: %v", bootRecoveredEvent.Fields["recovered"])
	}

	// 4. Verify prompt ultimately succeeded on attempt 2 (resume)
	rec := ss.state.Records[0]
	if rec.Status != domain.StatusSucceeded {
		t.Errorf("expected prompt succeeded after resume, got: %s", rec.Status)
	}
	if len(rec.Attempts) != 2 {
		t.Fatalf("expected 2 attempts (initial interrupted + resumed success), got: %d", len(rec.Attempts))
	}
	if rec.Attempts[0].Outcome != domain.OutcomeInterrupted {
		t.Errorf("expected attempt 1 outcome interrupted, got: %s", rec.Attempts[0].Outcome)
	}
	if rec.Attempts[1].Outcome != domain.OutcomeSuccess {
		t.Errorf("expected attempt 2 outcome success, got: %s", rec.Attempts[1].Outcome)
	}

	// 5. Idempotence: running orchestrator again on terminal state
	events2 := &fakeEventLog{}
	cleaner2 := &fakeStrayCleaner{}
	orch2 := NewOrchestrator(cfg, qs, ss, runner, events2, &fakeLocker{}, clk, &fakeSleeper{}).
		WithStrayProcessCleaner(cleaner2)
	if err := orch2.Run(context.Background()); err != nil {
		t.Fatalf("second orch.Run failed: %v", err)
	}
	if ss.state.BootCount != 2 {
		t.Errorf("expected BootCount == 2 after second run, got: %d", ss.state.BootCount)
	}
	for _, e := range events2.events {
		if e.Kind == "boot.recovered" {
			t.Errorf("did not expect boot.recovered event on second run with no running prompts")
		}
	}
}

type fakeCheckpointer struct {
	mu            sync.Mutex
	checkpoints   map[domain.PromptNumber]int
	resetRefs     []string
	checkpointErr error
	resetErr      error
}

func (f *fakeCheckpointer) Checkpoint(ctx context.Context, workDir string, n domain.PromptNumber) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkpointErr != nil {
		return "", f.checkpointErr
	}
	if f.checkpoints == nil {
		f.checkpoints = make(map[domain.PromptNumber]int)
	}
	f.checkpoints[n]++
	return fmt.Sprintf("agyq/cp-%d", n), nil
}

func (f *fakeCheckpointer) ResetTo(ctx context.Context, workDir, ref string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resetErr != nil {
		return f.resetErr
	}
	f.resetRefs = append(f.resetRefs, ref)
	return nil
}

func TestOrchestrator_F053_CheckpointBeforeAttempt1(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkDir = "/tmp/test-repo"

	initialState := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-checkpoint",
		Records: []domain.PromptRecord{
			{Number: 1, Title: "P1", Status: domain.StatusPending},
		},
	}

	ss := &memStateStore{state: initialState}
	qs := &memQueueSource{
		prompts: []domain.Prompt{{Number: 1, Title: "P1", Body: "Task 1"}},
	}
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}
	events := &fakeEventLog{}
	cp := &fakeCheckpointer{}

	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, &fakeClock{}, &fakeSleeper{}).
		WithCheckpointer(cp)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify checkpoint created before attempt 1
	if cp.checkpoints[1] != 1 {
		t.Errorf("expected 1 checkpoint call for prompt 1, got: %d", cp.checkpoints[1])
	}
	if ss.state.Records[0].Checkpoint != "agyq/cp-1" {
		t.Errorf("expected checkpoint 'agyq/cp-1' in state, got: %s", ss.state.Records[0].Checkpoint)
	}

	// Verify checkpoint event emitted
	hasCheckpointEvent := false
	for _, e := range events.events {
		if e.Kind == "checkpoint.created" {
			hasCheckpointEvent = true
			break
		}
	}
	if !hasCheckpointEvent {
		t.Errorf("expected checkpoint.created event to be emitted")
	}
}

func TestOrchestrator_F052_RecoveryRestartClean(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkDir = "/tmp/test-repo"
	cfg.Recovery = RecoveryRestartClean

	// Construct state with an interrupted prompt with an existing checkpoint
	state := &domain.RunState{
		SchemaVersion: 1,
		RunID:         "test-restart-clean",
		Records: []domain.PromptRecord{
			{
				Number:     1,
				Title:      "Interrupted Task",
				Status:     domain.StatusInterrupted,
				Checkpoint: "agyq/cp-1",
				Attempts: []domain.Attempt{
					{N: 1, Outcome: domain.OutcomeInterrupted},
				},
			},
		},
	}

	ss := &memStateStore{state: state}
	qs := &memQueueSource{
		prompts: []domain.Prompt{{Number: 1, Title: "Interrupted Task", Body: "Task 1"}},
	}
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			// In restart-clean, mode is RunFresh and preamble is empty
			if req.Mode != domain.RunFresh {
				t.Errorf("expected RunFresh mode for restart-clean, got: %s", req.Mode)
			}
			if req.Preamble != "" {
				t.Errorf("expected empty preamble for restart-clean, got: %q", req.Preamble)
			}
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}
	events := &fakeEventLog{}
	cp := &fakeCheckpointer{}

	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, &fakeClock{}, &fakeSleeper{}).
		WithCheckpointer(cp)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify ResetTo was called with "agyq/cp-1"
	if len(cp.resetRefs) != 1 || cp.resetRefs[0] != "agyq/cp-1" {
		t.Errorf("expected ResetTo called with agyq/cp-1, got: %v", cp.resetRefs)
	}

	// Verify checkpoint was not repeated on resume attempt
	if cp.checkpoints[1] != 0 {
		t.Errorf("expected 0 checkpoint calls on resume attempt, got: %d", cp.checkpoints[1])
	}
}

func TestOrchestrator_F044_LimitHitDoesNotConsumeRetry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"
	cfg.Retry.MaxAttempts = 2 // Strict limit of 2 retries for transient errors

	prompts := []domain.Prompt{
		{Number: 1, Title: "Limited task", Body: "work", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	sl := &fakeSleeper{clock: clk}

	callCount := 0
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCount++
			if callCount <= 2 {
				// Hit limit on first two attempts
				return RawRunResult{
					ExitCode: 1,
					Envelope: &Envelope{
						Status:   "RESOURCE_EXHAUSTED",
						Response: "rate limit exceeded: resets in 2m",
					},
				}, nil
			}
			// Succeed on 3rd attempt
			return RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{Status: "SUCCESS"},
			}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, clk, sl)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	rec := ss.state.Records[0]
	if rec.Status != domain.StatusSucceeded {
		t.Fatalf("expected succeeded despite 2 limit hits on MaxAttempts=2, got: %s", rec.Status)
	}
	if len(rec.Attempts) != 3 {
		t.Fatalf("expected 3 total attempts, got: %d", len(rec.Attempts))
	}
	if rec.Attempts[0].Outcome != domain.OutcomeLimit || rec.Attempts[1].Outcome != domain.OutcomeLimit {
		t.Fatalf("expected first two outcomes to be limit, got: %s, %s", rec.Attempts[0].Outcome, rec.Attempts[1].Outcome)
	}
}

func TestOrchestrator_F045_PostWakeReLimitGuard(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	prompts := []domain.Prompt{
		{Number: 1, Title: "Re-limiting task", Body: "work", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	sl := &fakeSleeper{clock: clk}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callCount := 0
	var secondCallFinTime time.Time

	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCount++
			if callCount == 1 {
				// 1st limit: resets in 1m
				return RawRunResult{
					ExitCode: 1,
					Envelope: &Envelope{
						Status:   "RESOURCE_EXHAUSTED",
						Response: "usage limit: resets in 1m",
					},
				}, nil
			}
			if callCount == 2 {
				secondCallFinTime = clk.Now()
				cancel()
				// 2nd call (post-wake) limits again with short reset string "try again in 10s"
				return RawRunResult{
					ExitCode: 1,
					Envelope: &Envelope{
						Status:   "RESOURCE_EXHAUSTED",
						Response: "usage limit: try again in 10s",
					},
				}, nil
			}
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, clk, sl)

	_ = orch.Run(ctx)

	if ss.state == nil || ss.state.Pause == nil {
		t.Fatalf("expected active Pause state, got nil")
	}

	// Post-wake sanity: Pause.Until must be at least 5 minutes after secondCallFinTime
	minExpected := secondCallFinTime.Add(5 * time.Minute)
	if ss.state.Pause.Until.Before(minExpected) {
		t.Fatalf("post-wake re-limit guard failed: Until %v is earlier than min %v", ss.state.Pause.Until, minExpected)
	}
}

func TestOrchestrator_F042_PauseUntilResetAndMidPauseRestart(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFile = "queue.md"

	prompts := []domain.Prompt{
		{Number: 1, Title: "Pause-wake task", Body: "work", Hash: "h1"},
	}

	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	initialTime := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: initialTime}

	// 1. Process 1: run until limit is hit and Pause is persisted
	ctx1, cancel1 := context.WithCancel(context.Background())
	sl1 := &fakeSleeper{} // Does NOT auto-advance clock so process sleeps at pause

	callCountProc1 := 0
	runner1 := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCountProc1++
			// Immediately cancel context on background goroutine to simulate shutdown mid-pause
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancel1()
			}()
			// Limit with explicit absolute reset at 11:00 UTC (1h ahead)
			return RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status:   "RESOURCE_EXHAUSTED",
					Response: "quota exceeded: resets at 2026-10-11T11:00:00Z",
				},
			}, nil
		},
	}

	orch1 := NewOrchestrator(cfg, qs, ss, runner1, &fakeEventLog{}, &fakeLocker{}, clk, sl1)
	_ = orch1.Run(ctx1)

	// Verify Pause was persisted before sleeping
	if ss.state == nil || ss.state.Pause == nil {
		t.Fatalf("expected state.Pause persisted before sleep, got nil")
	}
	expectedDeadline := time.Date(2026, 10, 11, 11, 1, 0, 0, time.UTC) // 11:00 + 60s margin
	if !ss.state.Pause.Until.Equal(expectedDeadline) {
		t.Fatalf("expected deadline %v, got %v", expectedDeadline, ss.state.Pause.Until)
	}
	if ss.state.Records[0].Status != domain.StatusWaitingLimit {
		t.Fatalf("expected status waiting_limit, got %s", ss.state.Records[0].Status)
	}

	// 2. Process 2 restart mid-pause: advance clock by 30 min (10:30, still before 11:01 deadline)
	clk.Set(time.Date(2026, 10, 11, 10, 30, 0, 0, time.UTC))
	ctx2, cancel2 := context.WithCancel(context.Background())
	sl2 := &fakeSleeper{} // Does not auto-advance

	callCountProc2 := 0
	runner2 := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCountProc2++
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	orch2 := NewOrchestrator(cfg, qs, ss, runner2, &fakeEventLog{}, &fakeLocker{}, clk, sl2)

	// Cancel after short sleep in Run
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel2()
	}()
	_ = orch2.Run(ctx2)

	// Assert: zero agy calls made before deadline!
	if callCountProc2 != 0 {
		t.Fatalf("expected 0 agy calls before deadline on mid-pause restart, got %d", callCountProc2)
	}
	// Assert: deadline was preserved
	if ss.state.Pause == nil || !ss.state.Pause.Until.Equal(expectedDeadline) {
		t.Fatalf("expected deadline %v preserved, got %v", expectedDeadline, ss.state.Pause)
	}

	// 3. Process 3: clock now reaches/exceeds deadline (11:05 UTC)
	clk.Set(time.Date(2026, 10, 11, 11, 5, 0, 0, time.UTC))
	callCountProc3 := 0
	runner3 := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			callCountProc3++
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	orch3 := NewOrchestrator(cfg, qs, ss, runner3, &fakeEventLog{}, &fakeLocker{}, clk, &fakeSleeper{})
	if err := orch3.Run(context.Background()); err != nil {
		t.Fatalf("orch3.Run failed: %v", err)
	}

	// Assert: called runner and succeeded!
	if callCountProc3 != 1 {
		t.Fatalf("expected 1 agy call after wake, got %d", callCountProc3)
	}
	if ss.state.Records[0].Status != domain.StatusSucceeded {
		t.Fatalf("expected status succeeded after waking, got %s", ss.state.Records[0].Status)
	}
	if ss.state.Pause != nil {
		t.Fatalf("expected Pause cleared after wake, got %+v", ss.state.Pause)
	}
}

type fakeControlChannel struct {
	mu   sync.Mutex
	cmds []ControlCommand
}

func (f *fakeControlChannel) Poll(ctx context.Context) (ControlCommand, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) == 0 {
		return ControlCommand{}, false
	}
	cmd := f.cmds[0]
	f.cmds = f.cmds[1:]
	return cmd, true
}

func (f *fakeControlChannel) Push(cmd ControlCommand) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
}

func TestOrchestrator_F062_PauseAndResume(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = "/tmp/agyq-test"
	cfg.QueueFile = "/tmp/agyq-test/queue.md"

	qs := &memQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Prompt 1", Body: "echo 1"},
			{Number: 2, Title: "Prompt 2", Body: "echo 2"},
		},
	}
	ss := &memStateStore{
		state: &domain.RunState{
			SchemaVersion: 1,
			Records: []domain.PromptRecord{
				{Number: 1, Title: "Prompt 1", Status: domain.StatusPending},
				{Number: 2, Title: "Prompt 2", Status: domain.StatusPending},
			},
		},
	}

	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	ctrl := &fakeControlChannel{}

	executed := make([]domain.PromptNumber, 0)
	var runnerMu sync.Mutex

	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			runnerMu.Lock()
			executed = append(executed, req.Prompt.Number)
			runnerMu.Unlock()

			// While prompt 1 is executing, operator submits pause
			if req.Prompt.Number == 1 {
				ctrl.Push(ControlCommand{
					Kind:      CommandPause,
					CreatedAt: clk.Now(),
				})
			}
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	sl := &fakeSleeper{clock: clk}
	orch := NewOrchestrator(cfg, qs, ss, runner, &fakeEventLog{}, &fakeLocker{}, clk, sl).
		WithControlChannel(ctrl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// In a separate goroutine, observe pause and then resume
	go func() {
		for {
			time.Sleep(5 * time.Millisecond)
			ss.mu.Lock()
			paused := ss.state.Operator.Paused
			prompt1Done := len(ss.state.Records) > 0 && ss.state.Records[0].Status == domain.StatusSucceeded
			ss.mu.Unlock()

			if paused && prompt1Done {
				// Assert: prompt 1 finished, prompt 2 not started yet!
				runnerMu.Lock()
				execCount := len(executed)
				runnerMu.Unlock()
				if execCount != 1 {
					t.Errorf("expected only prompt 1 executed when paused, got %d", execCount)
				}

				// Resume
				ctrl.Push(ControlCommand{
					Kind:      CommandResume,
					CreatedAt: clk.Now(),
				})
				break
			}
		}
	}()

	if err := orch.Run(ctx); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Verify both completed in order
	runnerMu.Lock()
	defer runnerMu.Unlock()
	if len(executed) != 2 || executed[0] != 1 || executed[1] != 2 {
		t.Fatalf("expected executed [1, 2], got %v", executed)
	}
	if ss.state.Records[0].Status != domain.StatusSucceeded || ss.state.Records[1].Status != domain.StatusSucceeded {
		t.Fatalf("expected both succeeded, got %s, %s", ss.state.Records[0].Status, ss.state.Records[1].Status)
	}
	if ss.state.Operator.Paused {
		t.Fatalf("expected operator paused=false at end")
	}
}

func TestOrchestrator_F063_Skip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = "/tmp/agyq-test"
	cfg.QueueFile = "/tmp/agyq-test/queue.md"

	qs := &memQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Prompt 1", Body: "echo 1"},
			{Number: 2, Title: "Prompt 2", Body: "echo 2"},
		},
	}
	ss := &memStateStore{
		state: &domain.RunState{
			SchemaVersion: 1,
			Records: []domain.PromptRecord{
				{Number: 1, Title: "Prompt 1", Status: domain.StatusPending},
				{Number: 2, Title: "Prompt 2", Status: domain.StatusPending},
			},
		},
	}

	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	ctrl := &fakeControlChannel{}

	// Skip prompt 2 ahead of time
	ctrl.Push(ControlCommand{
		Kind:      CommandSkip,
		Prompt:    2,
		CreatedAt: clk.Now(),
	})

	executed := make([]domain.PromptNumber, 0)
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			executed = append(executed, req.Prompt.Number)
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	events := &fakeEventLog{}
	sl := &fakeSleeper{clock: clk}
	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, clk, sl).
		WithControlChannel(ctrl)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Assert prompt 1 succeeded, prompt 2 skipped without runner invocation
	if len(executed) != 1 || executed[0] != 1 {
		t.Fatalf("expected only prompt 1 executed, got: %v", executed)
	}
	if ss.state.Records[1].Status != domain.StatusSkipped {
		t.Fatalf("expected prompt 2 status skipped, got %s", ss.state.Records[1].Status)
	}

	// Assert refusal on running prompt
	runningState := &domain.RunState{
		Records: []domain.PromptRecord{
			{Number: 1, Status: domain.StatusRunning},
		},
	}
	orch.applyControlCommand(context.Background(), runningState, ControlCommand{
		Kind:   CommandSkip,
		Prompt: 1,
	})
	if runningState.Records[0].Status != domain.StatusRunning {
		t.Fatalf("expected prompt 1 to remain running on skip refusal, got %s", runningState.Records[0].Status)
	}
}

func TestOrchestrator_F063_Retry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = "/tmp/agyq-test"
	cfg.QueueFile = "/tmp/agyq-test/queue.md"
	cfg.Retry.MaxAttempts = 2

	qs := &memQueueSource{
		prompts: []domain.Prompt{
			{Number: 1, Title: "Prompt 1", Body: "echo 1"},
		},
	}
	// Initial state: prompt 1 failed with 2 exhausted attempts
	ss := &memStateStore{
		state: &domain.RunState{
			SchemaVersion: 1,
			Records: []domain.PromptRecord{
				{
					Number: 1,
					Title:  "Prompt 1",
					Status: domain.StatusFailed,
					Attempts: []domain.Attempt{
						{N: 1, Outcome: domain.OutcomeTransient, Error: "timeout"},
						{N: 2, Outcome: domain.OutcomeTransient, Error: "timeout"},
					},
				},
			},
		},
	}

	clk := &fakeClock{now: time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)}
	ctrl := &fakeControlChannel{}

	// Operator issues retry for prompt 1
	ctrl.Push(ControlCommand{
		Kind:      CommandRetry,
		Prompt:    1,
		CreatedAt: clk.Now(),
	})

	executed := make([]domain.PromptNumber, 0)
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			executed = append(executed, req.Prompt.Number)
			return RawRunResult{ExitCode: 0, Envelope: &Envelope{Status: "SUCCESS"}}, nil
		},
	}

	events := &fakeEventLog{}
	sl := &fakeSleeper{clock: clk}
	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, clk, sl).
		WithControlChannel(ctrl)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	// Assert prompt 1 ran and succeeded
	if len(executed) != 1 || executed[0] != 1 {
		t.Fatalf("expected prompt 1 executed after retry, got: %v", executed)
	}
	if ss.state.Records[0].Status != domain.StatusSucceeded {
		t.Fatalf("expected prompt 1 succeeded, got %s", ss.state.Records[0].Status)
	}

	// Assert refusal on running prompt
	runningState := &domain.RunState{
		Records: []domain.PromptRecord{
			{Number: 1, Status: domain.StatusRunning},
		},
	}
	orch.applyControlCommand(context.Background(), runningState, ControlCommand{
		Kind:   CommandRetry,
		Prompt: 1,
	})
	if runningState.Records[0].Status != domain.StatusRunning {
		t.Fatalf("expected prompt 1 to remain running on retry refusal, got %s", runningState.Records[0].Status)
	}
}

func TestOrchestrator_ErrorLoggerRecordsDiagnostics(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	cfg := DefaultConfig()
	cfg.OnFailure = FailureContinue

	prompts := []domain.Prompt{
		{Number: 1, Title: "Broken prompt", Body: "do something", Hash: "h1"},
	}
	qs := &memQueueSource{prompts: prompts}
	ss := &memStateStore{}
	runner := &concurrencyTrackerRunner{
		turnHandler: func(req RunRequest) (RawRunResult, error) {
			return RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status: "ERROR",
					Error:  "unrecognized model slug",
				},
				Stderr:         []byte("error: invalid model"),
				TranscriptPath: ".agyq/transcripts/0001-a1.log",
			}, nil
		},
	}
	events := &fakeEventLog{}
	errorLog := &fakeErrorLog{}
	sl := &fakeSleeper{clock: clk}

	orch := NewOrchestrator(cfg, qs, ss, runner, events, &fakeLocker{}, clk, sl).
		WithErrorLogger(errorLog)

	if err := orch.Run(context.Background()); err != nil {
		t.Fatalf("orch.Run failed: %v", err)
	}

	if len(errorLog.entries) == 0 {
		t.Fatal("expected error log entries, got none")
	}
	entry := errorLog.entries[0]
	if entry.Prompt != 1 {
		t.Errorf("expected entry prompt 1, got %d", entry.Prompt)
	}
	if entry.Message != "unrecognized model slug" {
		t.Errorf("expected entry message 'unrecognized model slug', got %q", entry.Message)
	}
	if entry.Transcript != ".agyq/transcripts/0001-a1.log" {
		t.Errorf("expected transcript path, got %q", entry.Transcript)
	}
}
