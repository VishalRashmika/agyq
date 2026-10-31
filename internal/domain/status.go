package domain

import (
	"errors"
	"fmt"
)

// Status is the lifecycle of a prompt in the run state (docs/02 section 1).
type Status string

const (
	StatusPending      Status = "pending"       // never started
	StatusRunning      Status = "running"       // intent persisted, agy launched
	StatusInterrupted  Status = "interrupted"   // was running; process/host died or was cancelled -> resumable
	StatusWaitingLimit Status = "waiting_limit" // hit a usage limit; retry after Pause.Until
	StatusSucceeded    Status = "succeeded"     // terminal
	StatusFailed       Status = "failed"        // terminal
	StatusSkipped      Status = "skipped"       // terminal
)

// IsTerminal reports whether no further automatic action will happen.
func (s Status) IsTerminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusSkipped
}

// ErrIllegalTransition is returned when an invalid status transition is requested (F-030).
var ErrIllegalTransition = errors.New("illegal status transition")

// Transition validates whether a transition from one status to another is permitted.
func Transition(from, to Status) error {
	if from == to {
		return nil
	}
	switch from {
	case StatusPending:
		if to == StatusRunning || to == StatusSkipped {
			return nil
		}
	case StatusRunning:
		if to == StatusSucceeded || to == StatusFailed || to == StatusInterrupted || to == StatusWaitingLimit {
			return nil
		}
	case StatusInterrupted:
		if to == StatusRunning || to == StatusFailed || to == StatusSkipped {
			return nil
		}
	case StatusWaitingLimit:
		if to == StatusRunning || to == StatusSkipped {
			return nil
		}
	case StatusFailed:
		if to == StatusPending {
			return nil
		}
	case StatusSucceeded, StatusSkipped:
		// Terminal states cannot transition
	}
	return fmt.Errorf("%w: cannot transition from %q to %q", ErrIllegalTransition, from, to)
}
