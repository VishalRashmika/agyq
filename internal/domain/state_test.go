package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestMergeQueue_AppendNewPrompts(t *testing.T) {
	state := &RunState{
		SchemaVersion: 1,
		RunID:         "test-run",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	p1 := Prompt{Number: 1, Title: "Task 1", Body: "Body 1", Hash: ComputePromptHash("Task 1", "Body 1")}
	p2 := Prompt{Number: 2, Title: "Task 2", Body: "Body 2", Hash: ComputePromptHash("Task 2", "Body 2"), Skip: true}

	res, err := MergeQueue(state, []Prompt{p1, p2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(res.Appended, []PromptNumber{1, 2}) {
		t.Errorf("Appended = %v, want [1, 2]", res.Appended)
	}
	if len(state.Records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(state.Records))
	}
	if state.Records[0].Status != StatusPending {
		t.Errorf("record 0 status = %v, want pending", state.Records[0].Status)
	}
	if state.Records[1].Status != StatusSkipped {
		t.Errorf("record 1 status = %v, want skipped (agyq:skip)", state.Records[1].Status)
	}
}

func TestMergeQueue_Idempotent(t *testing.T) {
	state := &RunState{
		SchemaVersion: 1,
		RunID:         "test-run",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	p1 := Prompt{Number: 1, Title: "Task 1", Body: "Body 1", Hash: ComputePromptHash("Task 1", "Body 1")}
	p2 := Prompt{Number: 2, Title: "Task 2", Body: "Body 2", Hash: ComputePromptHash("Task 2", "Body 2")}

	_, err := MergeQueue(state, []Prompt{p1, p2})
	if err != nil {
		t.Fatalf("first merge failed: %v", err)
	}

	recordsBefore := make([]PromptRecord, len(state.Records))
	copy(recordsBefore, state.Records)

	res2, err := MergeQueue(state, []Prompt{p1, p2})
	if err != nil {
		t.Fatalf("second merge failed: %v", err)
	}

	if len(res2.Appended) != 0 || len(res2.Updated) != 0 || len(res2.Drifted) != 0 || len(res2.Orphaned) != 0 {
		t.Errorf("second merge should be no-op, got %+v", res2)
	}
	if !reflect.DeepEqual(state.Records, recordsBefore) {
		t.Error("records changed on second identical merge")
	}
}

func TestMergeQueue_UpdatePendingHash(t *testing.T) {
	state := &RunState{
		SchemaVersion: 1,
		RunID:         "test-run",
		Records: []PromptRecord{
			{
				Number: PromptNumber(1),
				Title:  "Old Title",
				Hash:   "oldhash",
				Status: StatusPending,
			},
		},
	}

	newP := Prompt{
		Number: 1,
		Title:  "New Title",
		Body:   "New Body",
		Hash:   ComputePromptHash("New Title", "New Body"),
	}

	res, err := MergeQueue(state, []Prompt{newP})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if !reflect.DeepEqual(res.Updated, []PromptNumber{1}) {
		t.Errorf("Updated = %v, want [1]", res.Updated)
	}
	if state.Records[0].Title != "New Title" || state.Records[0].Hash != newP.Hash {
		t.Errorf("record not updated: %+v", state.Records[0])
	}
	if state.Records[0].Status != StatusPending {
		t.Errorf("status changed from pending to %v", state.Records[0].Status)
	}
}

func TestMergeQueue_DriftDetectionOnNonPending(t *testing.T) {
	nonPendingStatuses := []Status{
		StatusRunning,
		StatusSucceeded,
		StatusFailed,
		StatusInterrupted,
		StatusWaitingLimit,
		StatusSkipped,
	}

	for _, st := range nonPendingStatuses {
		t.Run(string(st), func(t *testing.T) {
			state := &RunState{
				SchemaVersion: 1,
				Records: []PromptRecord{
					{
						Number: PromptNumber(1),
						Title:  "Original Title",
						Hash:   "originalhash",
						Status: st,
					},
				},
			}

			editedP := Prompt{
				Number: 1,
				Title:  "Edited After Start",
				Body:   "Edited body",
				Hash:   ComputePromptHash("Edited After Start", "Edited body"),
			}

			res, err := MergeQueue(state, []Prompt{editedP})
			if err != nil {
				t.Fatalf("merge failed: %v", err)
			}

			if !reflect.DeepEqual(res.Drifted, []PromptNumber{1}) {
				t.Errorf("status %s: Drifted = %v, want [1]", st, res.Drifted)
			}
			// Status and hash must NOT be updated
			if state.Records[0].Status != st {
				t.Errorf("status changed to %v, want %v", state.Records[0].Status, st)
			}
			if state.Records[0].Hash != "originalhash" {
				t.Errorf("hash was updated unexpectedly: %v", state.Records[0].Hash)
			}
		})
	}
}

func TestMergeQueue_OrphanedPrompts(t *testing.T) {
	state := &RunState{
		SchemaVersion: 1,
		Records: []PromptRecord{
			{Number: 1, Status: StatusSucceeded, Hash: "h1"},
			{Number: 2, Status: StatusPending, Hash: "h2"},
		},
	}

	// Only prompt 1 in file, prompt 2 is deleted from queue file
	res, err := MergeQueue(state, []Prompt{{Number: 1, Hash: "h1"}})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	if !reflect.DeepEqual(res.Orphaned, []PromptNumber{2}) {
		t.Errorf("Orphaned = %v, want [2]", res.Orphaned)
	}
	if len(state.Records) != 2 {
		t.Fatalf("prompt 2 should not be deleted, len = %d", len(state.Records))
	}
	// Prompt 2 was pending, so it should be marked skipped
	if state.Records[1].Status != StatusSkipped {
		t.Errorf("orphaned pending prompt 2 status = %v, want skipped", state.Records[1].Status)
	}
	// Prompt 1 status untouched
	if state.Records[0].Status != StatusSucceeded {
		t.Errorf("prompt 1 status = %v, want succeeded", state.Records[0].Status)
	}
}

func TestNextRunnable(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	t.Run("first pending prompt selected", func(t *testing.T) {
		s := &RunState{
			Records: []PromptRecord{
				{Number: 1, Status: StatusSucceeded},
				{Number: 2, Status: StatusPending},
				{Number: 3, Status: StatusPending},
			},
		}
		rec, wait := NextRunnable(s, now)
		if wait.ShouldWait {
			t.Fatal("unexpected wait")
		}
		if rec == nil || rec.Number != 2 {
			t.Fatalf("expected prompt 2, got %v", rec)
		}
	})

	t.Run("interrupted prompt selected before later pending", func(t *testing.T) {
		s := &RunState{
			Records: []PromptRecord{
				{Number: 1, Status: StatusInterrupted},
				{Number: 2, Status: StatusPending},
			},
		}
		rec, wait := NextRunnable(s, now)
		if wait.ShouldWait || rec == nil || rec.Number != 1 {
			t.Fatalf("expected prompt 1, got %v", rec)
		}
	})

	t.Run("never returns prompt when any prompt is running (max concurrency 1)", func(t *testing.T) {
		s := &RunState{
			Records: []PromptRecord{
				{Number: 1, Status: StatusRunning},
				{Number: 2, Status: StatusPending},
			},
		}
		rec, wait := NextRunnable(s, now)
		if rec != nil {
			t.Fatalf("expected nil when a prompt is running, got %v", rec)
		}
		if wait.ShouldWait {
			t.Fatal("unexpected wait when prompt running")
		}
	})

	t.Run("pause deadline in future returns ShouldWait and Until", func(t *testing.T) {
		future := now.Add(30 * time.Minute)
		s := &RunState{
			Pause: &Pause{
				Until:  future,
				Reason: PauseLimit,
			},
			Records: []PromptRecord{
				{Number: 1, Status: StatusWaitingLimit},
			},
		}
		rec, wait := NextRunnable(s, now)
		if rec != nil {
			t.Fatalf("expected nil prompt while paused, got %v", rec)
		}
		if !wait.ShouldWait || !wait.Until.Equal(future) || wait.Reason != "limit" {
			t.Fatalf("unexpected wait info: %+v", wait)
		}
	})

	t.Run("pause deadline in past allows waiting_limit prompt to run", func(t *testing.T) {
		past := now.Add(-5 * time.Minute)
		s := &RunState{
			Pause: &Pause{
				Until:  past,
				Reason: PauseLimit,
			},
			Records: []PromptRecord{
				{Number: 1, Status: StatusWaitingLimit},
			},
		}
		rec, wait := NextRunnable(s, now)
		if wait.ShouldWait {
			t.Fatal("expected no wait when deadline in past")
		}
		if rec == nil || rec.Number != 1 {
			t.Fatalf("expected prompt 1, got %v", rec)
		}
	})

	t.Run("operator pause halts runnable selection", func(t *testing.T) {
		s := &RunState{
			Operator: OperatorFlags{Paused: true},
			Records: []PromptRecord{
				{Number: 1, Status: StatusPending},
			},
		}
		rec, wait := NextRunnable(s, now)
		if rec != nil || !wait.ShouldWait || wait.Reason != "operator" {
			t.Fatalf("unexpected result: rec=%v, wait=%+v", rec, wait)
		}
	})

	t.Run("all terminal returns nil without wait", func(t *testing.T) {
		s := &RunState{
			Records: []PromptRecord{
				{Number: 1, Status: StatusSucceeded},
				{Number: 2, Status: StatusFailed},
				{Number: 3, Status: StatusSkipped},
			},
		}
		rec, wait := NextRunnable(s, now)
		if rec != nil || wait.ShouldWait {
			t.Fatalf("expected nil prompt and false wait, got rec=%v, wait=%+v", rec, wait)
		}
	})
}
