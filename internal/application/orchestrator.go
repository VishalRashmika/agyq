package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agyq/internal/domain"
)

// Sentinel errors for orchestrator termination.
var (
	ErrQueueHalted        = errors.New("queue execution halted on failure")
	ErrInvariantViolation = errors.New("single-in-flight invariant violated")
)

// CheckSingleInFlightInvariant verifies that at most one non-terminal prompt has started attempts (F-035).
func CheckSingleInFlightInvariant(s *domain.RunState) error {
	if s == nil {
		return nil
	}
	nonTerminalWithAttempts := 0
	for _, r := range s.Records {
		if !r.Status.IsTerminal() && len(r.Attempts) > 0 {
			nonTerminalWithAttempts++
		}
	}
	if nonTerminalWithAttempts > 1 {
		return fmt.Errorf("%w: %d non-terminal prompts have started attempts", ErrInvariantViolation, nonTerminalWithAttempts)
	}
	return nil
}

// Orchestrator coordinates the sequential execution of queued prompts.
type Orchestrator struct {
	cfg            Config
	queueSource    QueueSource
	stateStore     StateStore
	runner         AgyRunner
	eventLog       EventLog
	errorLog       ErrorLogger
	locker         Locker
	clock          Clock
	sleeper        Sleeper
	classifier     *Classifier
	cleaner        StrayProcessCleaner
	checkpointer   Checkpointer
	rand           Rand
	controlChannel ControlChannel
}

// NewOrchestrator constructs an Orchestrator with required dependencies.
func NewOrchestrator(
	cfg Config,
	qs QueueSource,
	ss StateStore,
	runner AgyRunner,
	el EventLog,
	locker Locker,
	clk Clock,
	sl Sleeper,
) *Orchestrator {
	return &Orchestrator{
		cfg:         cfg,
		queueSource: qs,
		stateStore:  ss,
		runner:      runner,
		eventLog:    el,
		locker:      locker,
		clock:       clk,
		sleeper:     sl,
		classifier:  NewClassifier(cfg),
	}
}

// WithErrorLogger injects an error logger for writing detailed diagnostics to errors.log.
func (o *Orchestrator) WithErrorLogger(el ErrorLogger) *Orchestrator {
	o.errorLog = el
	return o
}

// WithStrayProcessCleaner injects a stray process cleaner (F-050).
func (o *Orchestrator) WithStrayProcessCleaner(c StrayProcessCleaner) *Orchestrator {
	o.cleaner = c
	return o
}

// WithCheckpointer injects a git checkpointer (F-052, F-053).
func (o *Orchestrator) WithCheckpointer(cp Checkpointer) *Orchestrator {
	o.checkpointer = cp
	return o
}

// WithRand injects a pseudorandom source for jitter (F-043).
func (o *Orchestrator) WithRand(r Rand) *Orchestrator {
	o.rand = r
	return o
}

// WithControlChannel injects an operator control channel (F-062, F-063).
func (o *Orchestrator) WithControlChannel(cc ControlChannel) *Orchestrator {
	o.controlChannel = cc
	return o
}

// Run executes the sequential prompt queue until completion, failure, or cancellation.
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.locker != nil {
		release, err := o.locker.Acquire(ctx)
		if err != nil {
			return err
		}
		defer release()
	}

	state, err := o.stateStore.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrNoState) {
			prompts, err := o.queueSource.Load(ctx, o.cfg.QueueFile)
			if err != nil {
				return fmt.Errorf("load queue: %w", err)
			}
			if err := domain.ValidatePrompts(prompts); err != nil {
				return err
			}
			now := o.clock.Now()
			runID := fmt.Sprintf("run-%d", now.Unix())
			state = domain.NewRunState(runID, o.cfg.QueueFile, o.cfg.WorkDir, now)
			_, _ = domain.MergeQueue(state, prompts)
			if err := o.stateStore.Save(ctx, state); err != nil {
				return fmt.Errorf("save initial state: %w", err)
			}
		} else {
			return fmt.Errorf("load state: %w", err)
		}
	}

	// 4. Clock-trust guard (F-054)
	if err := o.guardClockTrust(ctx, state); err != nil {
		return err
	}

	// 5. Reconcile (F-050)
	if err := o.reconcile(ctx, state); err != nil {
		return err
	}

	// Startup invariant check
	if err := CheckSingleInFlightInvariant(state); err != nil {
		o.emitEvent(ctx, "invariant.violation", 0, "Invariant check failed", map[string]any{"error": err.Error()})
		return err
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		// Process operator control commands (F-062, F-063)
		if o.controlChannel != nil {
			for {
				cmd, ok := o.controlChannel.Poll(ctx)
				if !ok {
					break
				}
				o.applyControlCommand(ctx, state, cmd)
			}
		}

		// Live queue reload (F-005): re-read queue between prompts
		if o.queueSource != nil && o.cfg.QueueFile != "" {
			if parsed, err := o.queueSource.Load(ctx, o.cfg.QueueFile); err == nil {
				if err := domain.ValidatePrompts(parsed); err == nil {
					mergeRes, _ := domain.MergeQueue(state, parsed)
					if len(mergeRes.Appended) > 0 || len(mergeRes.Updated) > 0 {
						state.UpdatedAt = o.clock.Now()
						_ = o.stateStore.Save(ctx, state)
						if len(mergeRes.Appended) > 0 {
							o.emitEvent(ctx, "queue.reloaded", 0, "Appended new prompts from live reload", map[string]any{"appended": mergeRes.Appended})
						}
					}
				} else {
					o.emitEvent(ctx, "queue.invalid_reload", 0, "Queue file validation error during reload (ignoring)", map[string]any{"error": err.Error()})
				}
			}
		}

		// Verify invariant before picking next prompt
		if err := CheckSingleInFlightInvariant(state); err != nil {
			return err
		}

		now := o.clock.Now()
		rec, wait := domain.NextRunnable(state, now)
		if wait.ShouldWait {
			if wait.Until.After(now) {
				waitDur := wait.Until.Sub(now)
				if waitDur > 60*time.Second {
					waitDur = 60 * time.Second
				}
				if err := o.sleeper.Sleep(ctx, waitDur); err != nil {
					return nil
				}
				continue
			}
			if wait.Reason == "operator" {
				if err := o.sleeper.Sleep(ctx, 1*time.Second); err != nil {
					return nil
				}
				continue
			}
		}

		if state.Pause != nil && !state.Pause.Until.After(now) {
			pNum := state.Pause.PromptNumber
			state.Pause = nil
			state.UpdatedAt = now
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "limit.resumed", pNum, fmt.Sprintf("Pause deadline reached; resuming prompt #%d", pNum), nil)
		}

		if rec == nil {
			if o.checkpointer != nil && o.cfg.WorkDir != "" {
				_, _ = o.checkpointer.Checkpoint(ctx, o.cfg.WorkDir, 0)
			}
			o.emitEvent(ctx, "queue.completed", 0, "All prompts completed in queue", nil)
			return nil
		}

		// Execute turn for chosen prompt
		var promptBody string
		if o.queueSource != nil && o.cfg.QueueFile != "" {
			if parsed, err := o.queueSource.Load(ctx, o.cfg.QueueFile); err == nil {
				for _, p := range parsed {
					if p.Number == rec.Number {
						promptBody = p.Body
						break
					}
				}
			}
		}

		if err := o.executePromptTurn(ctx, state, rec, promptBody); err != nil {
			if errors.Is(err, ErrQueueHalted) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func (o *Orchestrator) executePromptTurn(ctx context.Context, state *domain.RunState, rec *domain.PromptRecord, promptBody string) error {
	mode := domain.RunFresh
	preamble := ""
	if rec.Status == domain.StatusInterrupted {
		maxResumes := o.cfg.MaxResumes
		if maxResumes <= 0 {
			maxResumes = 5
		}

		interruptedCount := 0
		for _, att := range rec.Attempts {
			if att.Outcome == domain.OutcomeInterrupted {
				interruptedCount++
			}
		}

		if interruptedCount > maxResumes {
			_ = domain.Transition(rec.Status, domain.StatusFailed)
			rec.Status = domain.StatusFailed
			now := o.clock.Now()
			rec.UpdatedAt = now
			state.UpdatedAt = now
			if len(rec.Attempts) > 0 {
				rec.Attempts[len(rec.Attempts)-1].Error = "too_many_interruptions"
			}
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "prompt.failed", rec.Number, "Prompt failed: too_many_interruptions", map[string]any{"reason": "too_many_interruptions"})
			if o.errorLog != nil {
				o.errorLog.LogError(ctx, ErrorEntry{
					Time:    now,
					Prompt:  rec.Number,
					Title:   rec.Title,
					Attempt: len(rec.Attempts),
					Outcome: domain.OutcomeFatal,
					Reason:  "too_many_interruptions",
					Message: fmt.Sprintf("Exceeded max resume limit of %d", maxResumes),
				})
			}
			if o.cfg.OnFailure == FailureStop {
				return ErrQueueHalted
			}
			return nil
		}

		if o.cfg.Recovery == RecoveryRestartClean && o.checkpointer != nil && rec.Checkpoint != "" {
			if err := o.checkpointer.ResetTo(ctx, o.cfg.WorkDir, rec.Checkpoint, false); err != nil {
				o.emitEvent(ctx, "reset.failed", rec.Number, fmt.Sprintf("Failed to reset to checkpoint %s", rec.Checkpoint), map[string]any{"error": err.Error()})
				if o.errorLog != nil {
					o.errorLog.LogError(ctx, ErrorEntry{
						Time:    o.clock.Now(),
						Prompt:  rec.Number,
						Title:   rec.Title,
						Outcome: domain.OutcomeFatal,
						Reason:  "reset_failed",
						Message: fmt.Sprintf("Failed to reset to checkpoint %s: %v", rec.Checkpoint, err),
					})
				}
				return fmt.Errorf("reset to checkpoint %s: %w", rec.Checkpoint, err)
			}
			o.emitEvent(ctx, "reset.complete", rec.Number, fmt.Sprintf("Reset to checkpoint %s", rec.Checkpoint), map[string]any{"ref": rec.Checkpoint})
			mode = domain.RunFresh
			preamble = ""
		} else {
			mode = domain.RunResume
			preamble = domain.ResumePreamble(*rec)
		}
	} else if rec.Status == domain.StatusWaitingLimit {
		// Post-wake mode determination (docs/06 §8):
		// If the limited attempt had produced partial work (> 30s) -> resume, else fresh
		if len(rec.Attempts) > 0 {
			lastAtt := rec.Attempts[len(rec.Attempts)-1]
			if lastAtt.FinishedAt != nil && lastAtt.FinishedAt.Sub(lastAtt.StartedAt) > 30*time.Second {
				mode = domain.RunResume
				preamble = domain.ResumePreamble(*rec)
			}
		}
	}

	// Git checkpoint before attempt 1 (F-053)
	if o.checkpointer != nil && o.cfg.WorkDir != "" && len(rec.Attempts) == 0 && rec.Checkpoint == "" {
		ref, err := o.checkpointer.Checkpoint(ctx, o.cfg.WorkDir, rec.Number)
		if err != nil {
			o.emitEvent(ctx, "checkpoint.failed", rec.Number, "Failed to create checkpoint before attempt 1", map[string]any{"error": err.Error()})
			if o.errorLog != nil {
				o.errorLog.LogError(ctx, ErrorEntry{
					Time:    o.clock.Now(),
					Prompt:  rec.Number,
					Title:   rec.Title,
					Outcome: domain.OutcomeFatal,
					Reason:  "checkpoint_failed",
					Message: fmt.Sprintf("Failed to create checkpoint before attempt 1: %v", err),
				})
			}
			return fmt.Errorf("git checkpoint before prompt #%d: %w", rec.Number, err)
		}
		rec.Checkpoint = ref
		o.emitEvent(ctx, "checkpoint.created", rec.Number, fmt.Sprintf("Created git checkpoint %s before prompt #%d", ref, rec.Number), map[string]any{"ref": ref})
	}

	if err := domain.Transition(rec.Status, domain.StatusRunning); err != nil {
		return err
	}
	rec.Status = domain.StatusRunning
	attemptN := len(rec.Attempts) + 1
	startedAt := o.clock.Now()
	attempt := domain.Attempt{
		N:         attemptN,
		StartedAt: startedAt,
		Mode:      mode,
	}
	rec.Attempts = append(rec.Attempts, attempt)
	rec.UpdatedAt = startedAt
	state.UpdatedAt = startedAt

	// P-2: Write intent to disk before launching agy
	if err := o.stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("persist running intent: %w", err)
	}
	o.emitEvent(ctx, "prompt.started", rec.Number, fmt.Sprintf("Started prompt #%d (attempt %d)", rec.Number, attemptN), nil)

	req := RunRequest{
		Prompt: domain.Prompt{
			Number: rec.Number,
			Title:  rec.Title,
			Body:   promptBody,
			Hash:   rec.Hash,
		},
		Mode:           mode,
		Preamble:       preamble,
		WorkDir:        o.cfg.WorkDir,
		Timeout:        o.cfg.PromptTimeout,
		StateDir:       o.cfg.StateDir,
		AttemptNumber:  attemptN,
		ConversationID: rec.LastConversationID,
	}

	res, runErr := o.runner.Run(ctx, req)

	// Calculate previous consecutive failures for classification
	consecutiveFailures := 0
	for i := len(rec.Attempts) - 2; i >= 0; i-- {
		if rec.Attempts[i].Outcome == domain.OutcomeTransient {
			consecutiveFailures++
		} else {
			break
		}
	}

	var outcome domain.Outcome
	var reason string
	var sig *domain.LimitSignal
	if runErr != nil && res.ExitCode == 0 && !res.Killed && !res.TimedOut {
		outcome = domain.OutcomeFatal
		reason = runErr.Error()
	} else {
		outcome, reason, sig = o.classifier.ClassifyWithSignal(res, consecutiveFailures)
	}

	finTime := o.clock.Now()
	att := &rec.Attempts[len(rec.Attempts)-1]
	att.FinishedAt = &finTime
	att.ExitCode = &res.ExitCode
	if res.Envelope != nil {
		att.ConversationID = res.Envelope.ConversationID
		att.AgyStatus = res.Envelope.Status
		rec.LastConversationID = res.Envelope.ConversationID
		if res.Envelope.Usage != nil {
			att.Usage = &domain.Usage{Raw: res.Envelope.Usage}
		}
	}
	att.Outcome = outcome
	att.TranscriptPath = res.TranscriptPath
	att.Error = reason

	if o.errorLog != nil && outcome != domain.OutcomeSuccess && outcome != domain.OutcomeInterrupted {
		errMsg := ""
		if res.Envelope != nil && res.Envelope.Error != "" {
			errMsg = res.Envelope.Error
		} else if runErr != nil {
			errMsg = runErr.Error()
		}
		o.errorLog.LogError(ctx, ErrorEntry{
			Time:       finTime,
			Prompt:     rec.Number,
			Title:      rec.Title,
			Attempt:    attemptN,
			ExitCode:   res.ExitCode,
			Outcome:    outcome,
			Reason:     reason,
			Message:    errMsg,
			Transcript: res.TranscriptPath,
			StderrTail: string(res.Stderr),
		})
	}

	switch outcome {
	case domain.OutcomeSuccess:
		_ = domain.Transition(rec.Status, domain.StatusSucceeded)
		rec.Status = domain.StatusSucceeded
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "prompt.succeeded", rec.Number, fmt.Sprintf("Prompt #%d succeeded", rec.Number), nil)
		return nil

	case domain.OutcomeInterrupted:
		_ = domain.Transition(rec.Status, domain.StatusInterrupted)
		rec.Status = domain.StatusInterrupted
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "prompt.interrupted", rec.Number, fmt.Sprintf("Prompt #%d interrupted", rec.Number), nil)
		return nil

	case domain.OutcomeTransient:
		transientTotal := consecutiveFailures + 1
		if transientTotal < o.cfg.Retry.MaxAttempts {
			_ = domain.Transition(rec.Status, domain.StatusInterrupted)
			rec.Status = domain.StatusInterrupted
			rec.UpdatedAt = finTime
			state.UpdatedAt = finTime
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "prompt.retry", rec.Number, fmt.Sprintf("Prompt #%d transient error (%s); retrying (%d/%d)", rec.Number, reason, transientTotal, o.cfg.Retry.MaxAttempts), nil)
			delay := o.backoffDelay(transientTotal)
			_ = o.sleeper.Sleep(ctx, delay)
			return nil
		}
		// Max attempts reached
		_ = domain.Transition(rec.Status, domain.StatusFailed)
		rec.Status = domain.StatusFailed
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "prompt.failed", rec.Number, fmt.Sprintf("Prompt #%d failed: retries exhausted (%s)", rec.Number, reason), nil)
		if o.cfg.OnFailure == FailureStop {
			return ErrQueueHalted
		}
		return nil

	case domain.OutcomeFatal:
		_ = domain.Transition(rec.Status, domain.StatusFailed)
		rec.Status = domain.StatusFailed
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "prompt.failed", rec.Number, fmt.Sprintf("Prompt #%d failed (fatal): %s", rec.Number, reason), nil)
		if o.cfg.OnFailure == FailureStop {
			return ErrQueueHalted
		}
		return nil

	case domain.OutcomeLimit:
		kind := domain.LimitSession
		if sig != nil && sig.Kind != "" {
			kind = sig.Kind
		}
		if kind == domain.LimitSession && domain.IsWeeklySuspected(rec.Attempts, finTime) {
			kind = domain.LimitWeekly
		}

		var resetAt *time.Time
		if sig != nil && sig.ResetAt != nil {
			resetAt = sig.ResetAt
		} else {
			rawCandidate := ""
			if sig != nil && sig.Raw != "" {
				rawCandidate = sig.Raw
			} else if res.Envelope != nil && res.Envelope.Response != "" {
				rawCandidate = res.Envelope.Response
			} else {
				rawCandidate = string(res.Stderr)
			}
			resetAt = domain.ParseResetTime(rawCandidate, finTime, o.cfg.Limits)
		}

		var until time.Time
		var source domain.ResetSource
		if resetAt != nil {
			until = resetAt.Add(60 * time.Second)
			source = domain.ResetParsed
		} else {
			jitter := 0.5
			if o.rand != nil {
				jitter = o.rand.Float64()
			}
			until = domain.FallbackPause(finTime, kind, o.cfg.Limits.Fallback, jitter)
			source = domain.ResetFallback
		}

		// Post-wake sanity (F-045): if the very first call after waking limits again, ensure min 5 min between probes
		if len(rec.Attempts) >= 2 && rec.Attempts[len(rec.Attempts)-2].Outcome == domain.OutcomeLimit {
			minUntil := finTime.Add(5 * time.Minute)
			if until.Before(minUntil) {
				until = minUntil
			}
		}

		// 8-day total wait cap (F-043)
		firstLimit := finTime
		for i := len(rec.Attempts) - 1; i >= 0; i-- {
			if rec.Attempts[i].Outcome == domain.OutcomeLimit {
				firstLimit = rec.Attempts[i].StartedAt
			} else {
				break
			}
		}
		maxTotal := o.cfg.Limits.Fallback.MaxTotal
		if maxTotal <= 0 {
			maxTotal = 192 * time.Hour
		}
		if until.Sub(firstLimit) > maxTotal {
			_ = domain.Transition(rec.Status, domain.StatusFailed)
			rec.Status = domain.StatusFailed
			att.Error = "limit_unresolved"
			rec.UpdatedAt = finTime
			state.UpdatedAt = finTime
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "prompt.failed", rec.Number, "Prompt failed: limit_unresolved", map[string]any{"reason": "limit_unresolved"})
			if o.errorLog != nil {
				o.errorLog.LogError(ctx, ErrorEntry{
					Time:    finTime,
					Prompt:  rec.Number,
					Title:   rec.Title,
					Attempt: attemptN,
					Outcome: domain.OutcomeFatal,
					Reason:  "limit_unresolved",
					Message: fmt.Sprintf("Usage limit wait exceeded maximum duration of %s", maxTotal),
				})
			}
			if o.cfg.OnFailure == FailureStop {
				return ErrQueueHalted
			}
			return nil
		}

		_ = domain.Transition(rec.Status, domain.StatusWaitingLimit)
		rec.Status = domain.StatusWaitingLimit
		rawMsg := ""
		if sig != nil {
			rawMsg = sig.Raw
		}
		state.Pause = &domain.Pause{
			Reason:       domain.PauseLimit,
			Until:        until,
			Source:       source,
			PromptNumber: rec.Number,
			Raw:          rawMsg,
		}
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "limit.hit", rec.Number, fmt.Sprintf("Prompt #%d usage limit hit: %s; pausing until %s (%s)", rec.Number, reason, until.Format(time.RFC3339), source), map[string]any{
			"until":  until,
			"source": string(source),
			"kind":   string(kind),
		})
		return nil

	default:
		_ = domain.Transition(rec.Status, domain.StatusFailed)
		rec.Status = domain.StatusFailed
		rec.UpdatedAt = finTime
		state.UpdatedAt = finTime
		_ = o.stateStore.Save(ctx, state)
		if o.cfg.OnFailure == FailureStop {
			return ErrQueueHalted
		}
		return nil
	}
}

func (o *Orchestrator) backoffDelay(attempt int) time.Duration {
	backoffs := o.cfg.Retry.Backoff
	if len(backoffs) == 0 {
		return 1 * time.Minute
	}
	idx := attempt - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffs) {
		idx = len(backoffs) - 1
	}
	return backoffs[idx]
}

func (o *Orchestrator) emitEvent(ctx context.Context, kind string, prompt domain.PromptNumber, msg string, fields map[string]any) {
	if o.eventLog != nil {
		o.eventLog.Emit(ctx, Event{
			Time:   o.clock.Now(),
			Level:  "info",
			Kind:   kind,
			Prompt: prompt,
			Msg:    msg,
			Fields: fields,
		})
	}
}

// guardClockTrust waits in <= 15s slices if the system clock is earlier than state.UpdatedAt by > 2m (F-054).
func (o *Orchestrator) guardClockTrust(ctx context.Context, state *domain.RunState) error {
	if state == nil || state.UpdatedAt.IsZero() {
		return nil
	}

	threshold := 2 * time.Minute
	maxWait := 10 * time.Minute
	sliceDuration := 15 * time.Second

	now := o.clock.Now()
	if now.Before(state.UpdatedAt.Add(-threshold)) {
		o.emitEvent(ctx, "clock.suspect", 0, "System clock earlier than state.UpdatedAt by > 2m; waiting for NTP sync", map[string]any{
			"now":              now,
			"state_updated_at": state.UpdatedAt,
		})

		waited := time.Duration(0)
		for waited < maxWait {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slice := sliceDuration
			if waited+slice > maxWait {
				slice = maxWait - waited
			}
			if err := o.sleeper.Sleep(ctx, slice); err != nil {
				return err
			}
			waited += slice

			if !o.clock.Now().Before(state.UpdatedAt.Add(-threshold)) {
				o.emitEvent(ctx, "clock.recovered", 0, "System clock synchronized; proceeding", map[string]any{
					"now": o.clock.Now(),
				})
				return nil
			}
		}

		o.emitEvent(ctx, "clock.timeout", 0, "System clock did not catch up within 10m; proceeding with caution", map[string]any{
			"now": o.clock.Now(),
		})
	}
	return nil
}

// reconcile implements startup recovery for F-050:
// - any running -> interrupted
// - BootCount++
// - emits boot.recovered event listing affected prompts
// - terminates stray agy processes
// - saves reconciled state
func (o *Orchestrator) reconcile(ctx context.Context, state *domain.RunState) error {
	if state == nil {
		return nil
	}

	state.BootCount++
	now := o.clock.Now()
	state.UpdatedAt = now

	var recovered []domain.PromptNumber
	for i := range state.Records {
		rec := &state.Records[i]
		if rec.Status == domain.StatusRunning {
			_ = domain.Transition(rec.Status, domain.StatusInterrupted)
			rec.Status = domain.StatusInterrupted
			rec.UpdatedAt = now
			if len(rec.Attempts) > 0 {
				att := &rec.Attempts[len(rec.Attempts)-1]
				if att.FinishedAt == nil {
					att.FinishedAt = &now
					att.Outcome = domain.OutcomeInterrupted
					att.Error = "reconciled on daemon boot"
				}
			}
			recovered = append(recovered, rec.Number)
		}
	}

	if len(recovered) > 0 {
		o.emitEvent(ctx, "boot.recovered", 0, fmt.Sprintf("Recovered %d interrupted prompt(s)", len(recovered)), map[string]any{
			"recovered":  recovered,
			"boot_count": state.BootCount,
		})
	}

	if o.cleaner != nil && o.cfg.WorkDir != "" {
		if err := o.cleaner.CleanStrayProcesses(ctx, o.cfg.WorkDir); err != nil {
			o.emitEvent(ctx, "boot.cleaner_error", 0, "Failed to clean stray processes", map[string]any{"error": err.Error()})
		}
	}

	if err := o.stateStore.Save(ctx, state); err != nil {
		return fmt.Errorf("save reconciled state: %w", err)
	}
	return nil
}

func (o *Orchestrator) applyControlCommand(ctx context.Context, state *domain.RunState, cmd ControlCommand) {
	now := o.clock.Now()
	switch cmd.Kind {
	case CommandPause:
		state.Operator.Paused = true
		state.UpdatedAt = now
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "operator.paused", 0, "Operator paused queue", nil)

	case CommandResume:
		state.Operator.Paused = false
		state.UpdatedAt = now
		_ = o.stateStore.Save(ctx, state)
		o.emitEvent(ctx, "operator.resumed", 0, "Operator resumed queue", nil)

	case CommandSkip:
		var target *domain.PromptRecord
		for i := range state.Records {
			if state.Records[i].Number == cmd.Prompt {
				target = &state.Records[i]
				break
			}
		}
		if target == nil {
			o.emitEvent(ctx, "operator.skip_error", cmd.Prompt, fmt.Sprintf("Prompt #%d not found in state", cmd.Prompt), nil)
			return
		}
		if target.Status == domain.StatusRunning {
			o.emitEvent(ctx, "operator.skip_refused", cmd.Prompt, fmt.Sprintf("Cannot skip running prompt #%d", cmd.Prompt), nil)
			return
		}
		if err := domain.Transition(target.Status, domain.StatusSkipped); err == nil {
			target.Status = domain.StatusSkipped
			target.UpdatedAt = now
			if state.Pause != nil && state.Pause.PromptNumber == target.Number {
				state.Pause = nil
			}
			state.UpdatedAt = now
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "prompt.skipped", target.Number, fmt.Sprintf("Operator skipped prompt #%d", target.Number), nil)
		} else {
			o.emitEvent(ctx, "operator.skip_error", cmd.Prompt, fmt.Sprintf("Cannot skip prompt #%d from status %s", cmd.Prompt, target.Status), nil)
		}

	case CommandRetry:
		var target *domain.PromptRecord
		for i := range state.Records {
			if state.Records[i].Number == cmd.Prompt {
				target = &state.Records[i]
				break
			}
		}
		if target == nil {
			o.emitEvent(ctx, "operator.retry_error", cmd.Prompt, fmt.Sprintf("Prompt #%d not found in state", cmd.Prompt), nil)
			return
		}
		if target.Status == domain.StatusRunning {
			o.emitEvent(ctx, "operator.retry_refused", cmd.Prompt, fmt.Sprintf("Cannot retry running prompt #%d", cmd.Prompt), nil)
			return
		}
		if err := domain.Transition(target.Status, domain.StatusPending); err == nil {
			target.Status = domain.StatusPending
			target.Attempts = nil
			target.LastConversationID = ""
			target.UpdatedAt = now
			if state.Pause != nil && state.Pause.PromptNumber == target.Number {
				state.Pause = nil
			}
			state.UpdatedAt = now
			_ = o.stateStore.Save(ctx, state)
			o.emitEvent(ctx, "prompt.retried", target.Number, fmt.Sprintf("Operator retried prompt #%d (reset to pending, attempt cap cleared)", target.Number), nil)
		} else {
			o.emitEvent(ctx, "operator.retry_error", cmd.Prompt, fmt.Sprintf("Cannot retry prompt #%d from status %s", cmd.Prompt, target.Status), nil)
		}
	}
}
