package domain

import "testing"

func TestStatusIsTerminal(t *testing.T) {
	cases := map[Status]bool{
		StatusPending:      false,
		StatusRunning:      false,
		StatusInterrupted:  false,
		StatusWaitingLimit: false,
		StatusSucceeded:    true,
		StatusFailed:       true,
		StatusSkipped:      true,
	}
	for s, want := range cases {
		if got := s.IsTerminal(); got != want {
			t.Errorf("%s.IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestStatusTransition(t *testing.T) {
	valid := [][2]Status{
		{StatusPending, StatusRunning},
		{StatusPending, StatusSkipped},
		{StatusRunning, StatusSucceeded},
		{StatusRunning, StatusFailed},
		{StatusRunning, StatusInterrupted},
		{StatusRunning, StatusWaitingLimit},
		{StatusInterrupted, StatusRunning},
		{StatusInterrupted, StatusFailed},
		{StatusInterrupted, StatusSkipped},
		{StatusWaitingLimit, StatusRunning},
		{StatusWaitingLimit, StatusSkipped},
		{StatusFailed, StatusPending},
		{StatusFailed, StatusRunning},
		// Self transitions
		{StatusPending, StatusPending},
		{StatusRunning, StatusRunning},
	}

	for _, pair := range valid {
		if err := Transition(pair[0], pair[1]); err != nil {
			t.Errorf("expected transition %s -> %s to be valid, got: %v", pair[0], pair[1], err)
		}
	}

	invalid := [][2]Status{
		{StatusPending, StatusSucceeded},
		{StatusPending, StatusFailed},
		{StatusSucceeded, StatusRunning},
		{StatusSucceeded, StatusPending},
		{StatusSkipped, StatusRunning},
		{StatusSkipped, StatusPending},
		{StatusWaitingLimit, StatusSucceeded},
		{StatusInterrupted, StatusSucceeded},
	}

	for _, pair := range invalid {
		if err := Transition(pair[0], pair[1]); err == nil {
			t.Errorf("expected transition %s -> %s to fail, but it succeeded", pair[0], pair[1])
		}
	}
}
