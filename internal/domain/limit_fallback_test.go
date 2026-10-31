package domain

import (
	"testing"
	"time"
)

func TestFallbackPause_F043_DeterministicJitter(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cfg := BackoffConfig{
		SessionProbe: 30 * time.Minute,
		WeeklyProbe:  3 * time.Hour,
		Jitter:       0.10, // 10%
		MaxTotal:     192 * time.Hour,
	}

	tests := []struct {
		name     string
		kind     LimitKind
		jitter   float64
		wantDiff time.Duration
	}{
		{
			name:     "session probe zero jitter (middle)",
			kind:     LimitSession,
			jitter:   0.5,
			wantDiff: 30 * time.Minute,
		},
		{
			name:     "session probe min jitter (-10%)",
			kind:     LimitSession,
			jitter:   0.0,
			wantDiff: 27 * time.Minute, // 30m * 0.9
		},
		{
			name:     "session probe max jitter (+10%)",
			kind:     LimitSession,
			jitter:   1.0,
			wantDiff: 33 * time.Minute, // 30m * 1.1
		},
		{
			name:     "weekly probe zero jitter (middle)",
			kind:     LimitWeekly,
			jitter:   0.5,
			wantDiff: 3 * time.Hour,
		},
		{
			name:     "weekly probe min jitter (-10%)",
			kind:     LimitWeekly,
			jitter:   0.0,
			wantDiff: 162 * time.Minute, // 180m * 0.9 = 162m
		},
		{
			name:     "weekly probe max jitter (+10%)",
			kind:     LimitWeekly,
			jitter:   1.0,
			wantDiff: 198 * time.Minute, // 180m * 1.1 = 198m
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FallbackPause(now, tc.kind, cfg, tc.jitter)
			diff := got.Sub(now)
			if diff != tc.wantDiff {
				t.Fatalf("got diff %v, want %v", diff, tc.wantDiff)
			}
		})
	}
}

func TestIsWeeklySuspected_F043(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		attempts []Attempt
		want     bool
	}{
		{
			name:     "less than 3 attempts -> false",
			attempts: []Attempt{{Outcome: OutcomeLimit, StartedAt: now.Add(-1 * time.Hour)}},
			want:     false,
		},
		{
			name: "3 consecutive limits within 6 hours -> true",
			attempts: []Attempt{
				{Outcome: OutcomeLimit, StartedAt: now.Add(-5 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-3 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-1 * time.Hour)},
			},
			want: true,
		},
		{
			name: "3 consecutive limits but oldest is 7 hours ago -> false",
			attempts: []Attempt{
				{Outcome: OutcomeLimit, StartedAt: now.Add(-7 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-3 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-1 * time.Hour)},
			},
			want: false,
		},
		{
			name: "3 limits but not consecutive (broken by transient) -> false",
			attempts: []Attempt{
				{Outcome: OutcomeLimit, StartedAt: now.Add(-5 * time.Hour)},
				{Outcome: OutcomeTransient, StartedAt: now.Add(-3 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-2 * time.Hour)},
				{Outcome: OutcomeLimit, StartedAt: now.Add(-1 * time.Hour)},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsWeeklySuspected(tc.attempts, now)
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
