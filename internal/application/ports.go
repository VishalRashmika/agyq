package application

import (
	"context"
	"errors"
	"time"

	"agyq/internal/domain"
)

// ErrNoState is returned by StateStore.Load when state.json does not exist.
var ErrNoState = errors.New("no state file found")

// StateStore persists and loads RunState atomically (F-010).
type StateStore interface {
	Load(ctx context.Context) (*domain.RunState, error)
	Save(ctx context.Context, s *domain.RunState) error
}

// QueueSource loads and parses the markdown queue (F-001).
type QueueSource interface {
	Load(ctx context.Context, path string) ([]domain.Prompt, error)
}

// Clock returns the current time in UTC.
type Clock interface{ Now() time.Time }

// Sleeper blocks for d or until ctx is cancelled.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// Rand supplies jitter; injected so backoff is deterministic in tests.
type Rand interface{ Float64() float64 }

// ErrLocked is returned by Locker.Acquire when another instance holds the lock (F-013).
var ErrLocked = errors.New("lock already held by another process")

// Locker manages exclusive single-instance execution (F-013).
type Locker interface {
	Acquire(ctx context.Context) (release func(), err error)
}

// CommandKind identifies operator control actions (F-062, F-063).
type CommandKind string

const (
	CommandPause  CommandKind = "pause"
	CommandResume CommandKind = "resume"
	CommandSkip   CommandKind = "skip"
	CommandRetry  CommandKind = "retry"
)

// ControlCommand represents an operator action submitted via control file.
type ControlCommand struct {
	ID        string              `json:"id"`
	Kind      CommandKind         `json:"kind"`
	Prompt    domain.PromptNumber `json:"prompt,omitempty"`
	CreatedAt time.Time           `json:"created_at"`
}

// ControlChannel polls operator commands from the control queue (F-062, F-063).
type ControlChannel interface {
	Poll(ctx context.Context) (ControlCommand, bool)
}

// Event is one structured log record (F-012, F-064).
type Event struct {
	Time   time.Time           `json:"time"`
	Level  string              `json:"level"`
	Kind   string              `json:"kind"` // e.g. "prompt.started", "limit.hit", "boot.recovered"
	Prompt domain.PromptNumber `json:"prompt,omitempty"`
	Msg    string              `json:"msg"`
	Fields map[string]any      `json:"fields,omitempty"`
}

// EventLog receives structured events.
type EventLog interface {
	Emit(ctx context.Context, e Event)
}

// ErrorEntry represents a detailed failure and diagnostic record.
type ErrorEntry struct {
	Time       time.Time           `json:"time"`
	Prompt     domain.PromptNumber `json:"prompt,omitempty"`
	Title      string              `json:"title,omitempty"`
	Attempt    int                 `json:"attempt,omitempty"`
	ExitCode   int                 `json:"exit_code,omitempty"`
	Outcome    domain.Outcome      `json:"outcome,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	Message    string              `json:"message,omitempty"`
	Transcript string              `json:"transcript,omitempty"`
	StderrTail string              `json:"stderr_tail,omitempty"`
}

// ErrorLogger records diagnostic information to an errors log file.
type ErrorLogger interface {
	LogError(ctx context.Context, entry ErrorEntry)
}

// RunRequest defines the parameters for a prompt execution turn.
type RunRequest struct {
	Prompt         domain.Prompt
	Mode           domain.RunMode
	Preamble       string // non-empty for RunResume
	WorkDir        string
	Timeout        time.Duration
	ConversationID string // optional: continue a known conversation
	StateDir       string // where transcripts and temporary prompt files are kept
	AttemptNumber  int    // 1-based attempt index for transcripts naming
}

// RawRunResult holds the unclassified observations from an agy execution.
type RawRunResult = domain.RawRunResult

// Envelope is the JSON structure output by agy in print mode.
type Envelope = domain.Envelope

// AgyRunner abstracts the execution of the agy CLI binary (F-020, F-023).
type AgyRunner interface {
	Run(ctx context.Context, req RunRequest) (RawRunResult, error)
	Preflight(ctx context.Context) (PreflightReport, error)
}

// StrayProcessCleaner locates and terminates leftover agy processes from earlier runs (F-050).
type StrayProcessCleaner interface {
	CleanStrayProcesses(ctx context.Context, workDir string) error
}

// Checkpointer creates git checkpoints and resets worktree state (F-052, F-053).
type Checkpointer interface {
	Checkpoint(ctx context.Context, workDir string, n domain.PromptNumber) (ref string, err error)
	ResetTo(ctx context.Context, workDir, ref string, force bool) error
}
