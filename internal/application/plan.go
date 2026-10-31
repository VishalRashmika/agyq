package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"agyq/internal/domain"
)

// PlanRequest contains parameters for running agyq plan.
type PlanRequest struct {
	QueueFile string
	WorkDir   string
	StateDir  string
	DryRun    bool
}

// PlanResult contains the result of planning.
type PlanResult struct {
	State    *domain.RunState
	Changes  domain.MergeResult
	Prompts  []domain.Prompt
	IsDryRun bool
}

// PlanService coordinates parsing, validating, merging, and persisting the queue (F-067).
type PlanService struct {
	queueSource QueueSource
	stateStore  StateStore
	clock       Clock
}

// NewPlanService constructs a PlanService with injected dependencies.
func NewPlanService(qs QueueSource, ss StateStore, clk Clock) *PlanService {
	return &PlanService{
		queueSource: qs,
		stateStore:  ss,
		clock:       clk,
	}
}

func generateRunID(t time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("run-%d-%s", t.Unix(), hex.EncodeToString(b))
}

// Execute loads the queue, validates it, merges into state, and optionally saves the state.
func (s *PlanService) Execute(ctx context.Context, req PlanRequest) (*PlanResult, error) {
	if req.QueueFile == "" {
		return nil, errors.New("queue file must be specified")
	}

	prompts, err := s.queueSource.Load(ctx, req.QueueFile)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if s.clock != nil {
		now = s.clock.Now()
	}

	var state *domain.RunState
	loaded, err := s.stateStore.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrNoState) {
			state = domain.NewRunState(generateRunID(now), req.QueueFile, req.WorkDir, now)
		} else {
			return nil, fmt.Errorf("failed to load state: %w", err)
		}
	} else {
		state = loaded
	}

	changes, err := domain.MergeQueue(state, prompts)
	if err != nil {
		return nil, fmt.Errorf("failed to merge queue into state: %w", err)
	}

	state.UpdatedAt = now

	if !req.DryRun {
		if err := s.stateStore.Save(ctx, state); err != nil {
			return nil, fmt.Errorf("failed to save state: %w", err)
		}
	}

	return &PlanResult{
		State:    state,
		Changes:  changes,
		Prompts:  prompts,
		IsDryRun: req.DryRun,
	}, nil
}
