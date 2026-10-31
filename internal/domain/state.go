package domain

import (
	"time"
)

// RunMode indicates whether a prompt is executed fresh or resumed after interruption.
type RunMode string

const (
	RunFresh  RunMode = "fresh"
	RunResume RunMode = "resume" // preamble-augmented re-run after interruption (D-3)
)

// Outcome represents the classified outcome of a prompt attempt.
type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeLimit       Outcome = "limit"
	OutcomeTransient   Outcome = "transient" // network/5xx/timeout; retry with backoff
	OutcomeFatal       Outcome = "fatal"     // bad flags, auth missing, agy not found
	OutcomeInterrupted Outcome = "interrupted"
)

// Usage mirrors the token usage information from the agy response envelope.
type Usage struct {
	InputTokens  int64          `json:"input_tokens,omitempty"`
	OutputTokens int64          `json:"output_tokens,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

// Attempt records the execution details of a single prompt turn.
type Attempt struct {
	N              int        `json:"n"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Mode           RunMode    `json:"mode"`
	ConversationID string     `json:"conversation_id,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	AgyStatus      string     `json:"agy_status,omitempty"`
	Outcome        Outcome    `json:"outcome"`
	TranscriptPath string     `json:"transcript_path,omitempty"`
	Error          string     `json:"error,omitempty"`
	Usage          *Usage     `json:"usage,omitempty"`
}

// PromptRecord is the durable per-prompt state stored in RunState.
type PromptRecord struct {
	Number             PromptNumber `json:"number"`
	Title              string       `json:"title"`
	Hash               string       `json:"hash"` // hash at first start; later drift flagged (F-014)
	Status             Status       `json:"status"`
	Attempts           []Attempt    `json:"attempts"`
	Checkpoint         string       `json:"checkpoint,omitempty"` // git ref/sha created before first attempt
	LastConversationID string       `json:"last_conversation_id,omitempty"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

// PauseReason indicates why queue execution is paused.
type PauseReason string

const (
	PauseLimit    PauseReason = "limit"
	PauseOperator PauseReason = "operator"
)

// ResetSource indicates whether the pause deadline was extracted from agy or computed via fallback.
type ResetSource string

const (
	ResetParsed   ResetSource = "parsed"
	ResetFallback ResetSource = "fallback"
)

// LimitKind categorizes a usage limit.
type LimitKind string

const (
	LimitSession LimitKind = "session"
	LimitWeekly  LimitKind = "weekly"
	LimitUnknown LimitKind = "unknown"
)

// LimitSignal is emitted by the classifier when an attempt ends in OutcomeLimit.
type LimitSignal struct {
	Kind    LimitKind  `json:"kind"`
	ResetAt *time.Time `json:"reset_at,omitempty"`
	Raw     string     `json:"raw"`
}

// Pause represents an active wait deadline persisted in absolute UTC.
type Pause struct {
	Reason       PauseReason  `json:"reason"`
	Until        time.Time    `json:"until"` // UTC
	Source       ResetSource  `json:"source"`
	PromptNumber PromptNumber `json:"prompt_number"`
	Raw          string       `json:"raw,omitempty"`
}

// OperatorFlags tracks manual controls set via CLI commands.
type OperatorFlags struct {
	Paused bool `json:"paused"`
}

// RunState represents the complete durable execution state (state.json).
type RunState struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	QueueFile     string         `json:"queue_file"`
	WorkDir       string         `json:"work_dir"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	BootCount     int            `json:"boot_count"`
	Pause         *Pause         `json:"pause,omitempty"`
	Operator      OperatorFlags  `json:"operator"`
	Records       []PromptRecord `json:"records"`
}

// NewRunState initializes a fresh RunState with SchemaVersion 1.
func NewRunState(runID, queueFile, workDir string, now time.Time) *RunState {
	return &RunState{
		SchemaVersion: 1,
		RunID:         runID,
		QueueFile:     queueFile,
		WorkDir:       workDir,
		CreatedAt:     now,
		UpdatedAt:     now,
		Records:       make([]PromptRecord, 0),
	}
}

// MergeResult summarizes the mutations performed by MergeQueue (F-004, F-014).
type MergeResult struct {
	Appended []PromptNumber
	Updated  []PromptNumber
	Drifted  []PromptNumber
	Orphaned []PromptNumber
}

// MergeQueue merges parsed prompts into existing state according to docs/06 §5.
// It never deletes historical records, flags edits after start, and appends new prompts.
func MergeQueue(s *RunState, parsed []Prompt) (MergeResult, error) {
	var res MergeResult

	// Ensure parsed prompts are sorted by number
	sortedParsed := SortPrompts(parsed)

	// Map existing records
	existingIndex := make(map[PromptNumber]int)
	for i := range s.Records {
		existingIndex[s.Records[i].Number] = i
	}

	parsedSeen := make(map[PromptNumber]bool)

	for _, p := range sortedParsed {
		parsedSeen[p.Number] = true
		idx, exists := existingIndex[p.Number]
		if !exists {
			// Situation: number not in state -> append pending record (or skipped if <!-- agyq:skip -->)
			st := StatusPending
			if p.Skip {
				st = StatusSkipped
			}
			rec := PromptRecord{
				Number:    p.Number,
				Title:     p.Title,
				Hash:      p.Hash,
				Status:    st,
				Attempts:  []Attempt{},
				UpdatedAt: s.UpdatedAt,
			}
			s.Records = append(s.Records, rec)
			existingIndex[p.Number] = len(s.Records) - 1
			res.Appended = append(res.Appended, p.Number)
			continue
		}

		rec := &s.Records[idx]
		if rec.Hash == p.Hash {
			// Situation: number in state, hash equal -> nothing
			continue
		}

		if rec.Status == StatusPending {
			// Situation: number in state, status pending, hash differs -> update title/hash
			rec.Title = p.Title
			rec.Hash = p.Hash
			rec.UpdatedAt = s.UpdatedAt
			res.Updated = append(res.Updated, p.Number)
		} else {
			// Situation: number in state, status != pending, hash differs -> drift detected
			// Do not change status (F-014)
			res.Drifted = append(res.Drifted, p.Number)
		}
	}

	// Situation: number in state, missing from file
	for i := range s.Records {
		rec := &s.Records[i]
		if !parsedSeen[rec.Number] {
			res.Orphaned = append(res.Orphaned, rec.Number)
			if rec.Status == StatusPending {
				rec.Status = StatusSkipped
				rec.UpdatedAt = s.UpdatedAt
			}
		}
	}

	// Ensure records remain sorted by PromptNumber
	for i := 1; i < len(s.Records); i++ {
		for j := i; j > 0 && s.Records[j].Number < s.Records[j-1].Number; j-- {
			s.Records[j], s.Records[j-1] = s.Records[j-1], s.Records[j]
		}
	}

	return res, nil
}

// WaitInfo conveys whether the orchestrator must wait before proceeding (F-030, F-042).
type WaitInfo struct {
	ShouldWait bool
	Until      time.Time
	Reason     string
}

// NextRunnable finds the next prompt eligible for execution (F-030).
// Never returns a prompt if any prompt is already running (P-1 max concurrency == 1).
func NextRunnable(s *RunState, now time.Time) (*PromptRecord, WaitInfo) {
	if s == nil {
		return nil, WaitInfo{}
	}

	// Invariant: if any prompt is currently running, no new prompt can start
	for i := range s.Records {
		if s.Records[i].Status == StatusRunning {
			return nil, WaitInfo{}
		}
	}

	if s.Operator.Paused {
		return nil, WaitInfo{
			ShouldWait: true,
			Reason:     "operator",
		}
	}

	if s.Pause != nil && s.Pause.Until.After(now) {
		return nil, WaitInfo{
			ShouldWait: true,
			Until:      s.Pause.Until,
			Reason:     string(s.Pause.Reason),
		}
	}

	for i := range s.Records {
		rec := &s.Records[i]
		if rec.Status == StatusPending || rec.Status == StatusInterrupted || rec.Status == StatusWaitingLimit {
			return rec, WaitInfo{}
		}
	}

	return nil, WaitInfo{}
}
