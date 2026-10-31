package application

import (
	"testing"

	"agyq/internal/domain"
)

func TestClassifier_F031_CompletionDetectionTable(t *testing.T) {
	cfg := DefaultConfig()
	classifier := NewClassifier(cfg)

	tests := []struct {
		name                string
		res                 RawRunResult
		consecutiveFailures int
		wantOutcome         domain.Outcome
	}{
		{
			name: "exit 0, SUCCESS envelope -> success",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status:   "SUCCESS",
					Response: "All tasks done",
				},
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeSuccess,
		},
		{
			name: "exit 0, other status ERROR -> transient first time, then fatal",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status:   "ERROR",
					Response: "failed to compile",
				},
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeTransient,
		},
		{
			name: "exit 0, other status ERROR on retry -> fatal",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status:   "ERROR",
					Response: "failed to compile",
				},
			},
			consecutiveFailures: 1,
			wantOutcome:         domain.OutcomeFatal,
		},
		{
			name: "exit 0, nil envelope -> transient first time",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: nil,
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeTransient,
		},
		{
			name: "exit 0, nil envelope on retry -> fatal",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: nil,
			},
			consecutiveFailures: 1,
			wantOutcome:         domain.OutcomeFatal,
		},
		{
			name: "exit non-zero -> not success (transient first time)",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("unexpected failure"),
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeTransient,
		},
		{
			name: "exit non-zero on repeat -> fatal",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("unexpected failure"),
			},
			consecutiveFailures: 1,
			wantOutcome:         domain.OutcomeFatal,
		},
		{
			name: "killed -> interrupted",
			res: RawRunResult{
				ExitCode: 137,
				Killed:   true,
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeInterrupted,
		},
		{
			name: "timed out -> transient",
			res: RawRunResult{
				ExitCode: 137,
				TimedOut: true,
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeTransient,
		},
		{
			name: "limit pattern in envelope status -> limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{Status: "RESOURCE_EXHAUSTED"},
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeLimit,
		},
		{
			name: "auth failure in stderr -> fatal",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("Error: not signed in to Google AI"),
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeFatal,
		},
		{
			name: "transient network pattern in stderr -> transient",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("connection 503 unavailable, try again"),
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeTransient,
		},
		{
			name: "exit 0, status SUCCESS, but denied_actions in envelope -> fatal (PF-01)",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status:        "SUCCESS",
					DeniedActions: []any{map[string]any{"action": "command"}},
				},
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeFatal,
		},
		{
			name: "exit 0, status SUCCESS, but headless auto-denied in stderr -> fatal (PF-01)",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status: "SUCCESS",
				},
				Stderr: []byte(`jetski: no output produced — a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied.`),
			},
			consecutiveFailures: 0,
			wantOutcome:         domain.OutcomeFatal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outcome, reason := classifier.Classify(tc.res, tc.consecutiveFailures)
			if outcome != tc.wantOutcome {
				t.Errorf("outcome got %q, want %q (reason: %s)", outcome, tc.wantOutcome, reason)
			}
		})
	}
}

func TestClassifier_PF01_PermissionDenialReason(t *testing.T) {
	cfg := DefaultConfig()
	classifier := NewClassifier(cfg)

	res := RawRunResult{
		ExitCode: 0,
		Envelope: &Envelope{
			Status:        "SUCCESS",
			DeniedActions: []any{map[string]any{"action": "command"}},
		},
	}
	outcome, reason := classifier.Classify(res, 0)
	if outcome != domain.OutcomeFatal {
		t.Fatalf("expected fatal outcome, got %v", outcome)
	}
	if reason == "" || !domain.HasPermissionDenial(res) {
		t.Fatalf("expected HasPermissionDenial to be true, got %v", domain.HasPermissionDenial(res))
	}
}
