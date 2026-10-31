package domain

import (
	"strings"
	"testing"
)

func defaultTestLimitPatterns() LimitPatterns {
	return LimitPatterns{
		Patterns: []string{
			"quota",
			"rate limit",
			"usage limit",
			`resource[_ ]exhausted`,
			"429",
			"limit reached",
			"exceeded your",
			`try again (in|after|at)`,
		},
		ExitCodes: []int{429},
		StatusValues: []string{
			"RESOURCE_EXHAUSTED",
			"QUOTA_EXCEEDED",
			"RATE_LIMITED",
		},
		WeeklyHint: []string{
			"weekly",
			"per week",
			"7 day",
		},
		ResetRegexes: []string{
			`(?i)reset[s]? (?:at|on) (?P<abs>[^.\n]+)`,
			`(?i)(?:in|after) (?P<rel>(?:\d+\s*(?:d|day|days|h|hr|hrs|hour|hours|m|min|mins|minute|minutes)\s*(?:and\s*)?)+)`,
		},
		Timezone: "UTC",
	}
}

func TestClassifyOutcome_F040_Table(t *testing.T) {
	p := defaultTestLimitPatterns()

	tests := []struct {
		name        string
		res         RawRunResult
		wantOutcome Outcome
		wantSignal  bool
		wantKind    LimitKind
	}{
		{
			name: "unknown text in stderr is NOT a limit",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("panic: runtime error: index out of range"),
			},
			wantOutcome: OutcomeTransient,
			wantSignal:  false,
		},
		{
			name: "unknown random error message is NOT a limit",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("failed to connect to local database socket /tmp/db.sock"),
			},
			wantOutcome: OutcomeTransient,
			wantSignal:  false,
		},
		{
			name: "clean exit 0 + SUCCESS with '429' in response body is NOT a limit (safety valve)",
			res: RawRunResult{
				ExitCode: 0,
				Envelope: &Envelope{
					Status:   "SUCCESS",
					Response: "To handle HTTP 429 Too Many Requests in Go, implement exponential backoff.",
				},
			},
			wantOutcome: OutcomeSuccess,
			wantSignal:  false,
		},
		{
			name: "envelope status RESOURCE_EXHAUSTED -> limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status:   "RESOURCE_EXHAUSTED",
					Response: "Usage limit reached. Try again in 3h 12m.",
				},
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "envelope status QUOTA_EXCEEDED -> limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status: "QUOTA_EXCEEDED",
				},
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "envelope status RATE_LIMITED -> limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status: "RATE_LIMITED",
				},
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "exit code matches ExitCodes (429) -> limit",
			res: RawRunResult{
				ExitCode: 429,
				Stderr:   []byte("API rejected request"),
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "stderr contains 'rate limit' -> limit (session)",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("Error: rate limit exceeded, try again later"),
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "stderr contains 'usage limit' and 'try again in 3h' -> limit (session)",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("usage limit reached: try again in 3h 12m"),
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "response contains weekly hint 'per week' -> limit (weekly)",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status:   "ERROR",
					Response: "You have exceeded your 7 day quota. Limit is 50 requests per week.",
				},
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitWeekly,
		},
		{
			name: "stderr contains weekly hint 'weekly usage limit' -> limit (weekly)",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("weekly usage limit reached: try again in 5 days"),
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitWeekly,
		},
		{
			name: "response over 4 KiB: limit pattern in first 4 KiB -> limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status:   "ERROR",
					Response: "quota exceeded at start of response: " + strings.Repeat("x", 5000),
				},
			},
			wantOutcome: OutcomeLimit,
			wantSignal:  true,
			wantKind:    LimitSession,
		},
		{
			name: "response over 4 KiB: limit pattern after 4 KiB -> ignored, unknown != limit",
			res: RawRunResult{
				ExitCode: 1,
				Envelope: &Envelope{
					Status:   "ERROR",
					Response: strings.Repeat("x", 4100) + "quota exceeded at end",
				},
			},
			wantOutcome: OutcomeTransient,
			wantSignal:  false,
		},
		{
			name: "killed -> interrupted",
			res: RawRunResult{
				ExitCode: 137,
				Killed:   true,
			},
			wantOutcome: OutcomeInterrupted,
			wantSignal:  false,
		},
		{
			name: "timed out -> transient",
			res: RawRunResult{
				ExitCode: 137,
				TimedOut: true,
			},
			wantOutcome: OutcomeTransient,
			wantSignal:  false,
		},
		{
			name: "auth error in stderr -> fatal",
			res: RawRunResult{
				ExitCode: 1,
				Stderr:   []byte("Error: not signed in to Google AI"),
			},
			wantOutcome: OutcomeFatal,
			wantSignal:  false,
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
			wantOutcome: OutcomeFatal,
			wantSignal:  false,
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
			wantOutcome: OutcomeFatal,
			wantSignal:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outcome, sig := ClassifyOutcome(tc.res, p)
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome got %q, want %q", outcome, tc.wantOutcome)
			}
			if tc.wantSignal {
				if sig == nil {
					t.Fatalf("expected LimitSignal, got nil")
				}
				if sig.Kind != tc.wantKind {
					t.Fatalf("signal.Kind got %q, want %q", sig.Kind, tc.wantKind)
				}
				if sig.Raw == "" {
					t.Fatalf("signal.Raw was empty")
				}
			} else {
				if sig != nil {
					t.Fatalf("expected nil LimitSignal, got %+v", sig)
				}
			}
		})
	}
}
