package domain

import (
	"testing"
	"time"
)

func TestParseResetTime_F041_Table(t *testing.T) {
	pUTC := LimitPatterns{
		ResetRegexes: []string{
			`(?i)reset[s]? (?:at|on) (?P<abs>[^.\n]+)`,
			`(?i)(?:in|after) (?P<rel>(?:\d+\s*(?:d|day|days|h|hr|hrs|hour|hours|m|min|mins|minute|minutes)\s*(?:and\s*)?)+)`,
		},
		Timezone: "UTC",
	}

	pNY := LimitPatterns{
		ResetRegexes: []string{
			`(?i)reset[s]? (?:at|on) (?P<abs>[^.\n]+)`,
			`(?i)(?:in|after) (?P<rel>(?:\d+\s*(?:d|day|days|h|hr|hrs|hour|hours|m|min|mins|minute|minutes)\s*(?:and\s*)?)+)`,
		},
		Timezone: "America/New_York",
	}

	now := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		raw      string
		now      time.Time
		p        LimitPatterns
		wantTime *time.Time
		wantNil  bool
	}{
		{
			name:     "relative 3h 12m",
			raw:      "usage limit reached: try again in 3h 12m.",
			now:      now,
			p:        pUTC,
			wantTime: ptrTime(now.Add(3*time.Hour + 12*time.Minute)),
		},
		{
			name:     "relative 5 days",
			raw:      "weekly usage limit: try again in 5 days",
			now:      now,
			p:        pUTC,
			wantTime: ptrTime(now.Add(5 * 24 * time.Hour)),
		},
		{
			name:     "relative after 2 hours and 30 minutes",
			raw:      "rate limit: please retry after 2 hours and 30 minutes",
			now:      now,
			p:        pUTC,
			wantTime: ptrTime(now.Add(2*time.Hour + 30*time.Minute)),
		},
		{
			name:     "absolute RFC3339 in UTC",
			raw:      "resets at 2026-10-11T14:35:00Z",
			now:      now,
			p:        pUTC,
			wantTime: ptrTime(time.Date(2026, 10, 11, 14, 35, 0, 0, time.UTC)),
		},
		{
			name:     "time only: later today (no rollover)",
			raw:      "quota resets at 14:35",
			now:      now, // 10:00 UTC
			p:        pUTC,
			wantTime: ptrTime(time.Date(2026, 10, 11, 14, 35, 0, 0, time.UTC)),
		},
		{
			name:     "time only: earlier today -> rolls over to tomorrow",
			raw:      "resets at 08:30",
			now:      now, // 10:00 UTC
			p:        pUTC,
			wantTime: ptrTime(time.Date(2026, 10, 12, 8, 30, 0, 0, time.UTC)),
		},
		{
			name:     "tomorrow at 08:00",
			raw:      "resets at tomorrow at 08:00",
			now:      now, // 2026-10-11
			p:        pUTC,
			wantTime: ptrTime(time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)),
		},
		{
			name: "interpreted in America/New_York (UTC-4 in October)",
			raw:  "resets at 14:35",
			now:  now, // 10:00 UTC = 06:00 EDT
			p:    pNY,
			// 14:35 EDT = 18:35 UTC
			wantTime: ptrTime(time.Date(2026, 10, 11, 18, 35, 0, 0, time.UTC)),
		},
		{
			name:    "past timestamp rejected",
			raw:     "resets at 2026-10-10T14:35:00Z",
			now:     now, // 2026-10-11
			p:       pUTC,
			wantNil: true,
		},
		{
			name:    "> 8 days out rejected",
			raw:     "resets in 9 days",
			now:     now,
			p:       pUTC,
			wantNil: true,
		},
		{
			name:    "unknown/garbage text returns nil",
			raw:     "some generic error occurred",
			now:     now,
			p:       pUTC,
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseResetTime(tc.raw, tc.now, tc.p)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %v, got nil", tc.wantTime)
			}
			if !got.Equal(*tc.wantTime) {
				t.Fatalf("got %v (%s), want %v (%s)", got, got.Location(), tc.wantTime, tc.wantTime.Location())
			}
		})
	}
}

func TestParseResetTime_F041_DSTTransition(t *testing.T) {
	// DST transition in US: Nov 1, 2026 (clocks fall back 2am -> 1am)
	// EDT is UTC-4 before 2am Nov 1; EST is UTC-5 after 2am Nov 1.
	pNY := LimitPatterns{
		ResetRegexes: []string{
			`(?i)reset[s]? (?:at|on) (?P<abs>[^.\n]+)`,
		},
		Timezone: "America/New_York",
	}

	// Saturday Oct 31, 2026 23:00 EDT (UTC: Nov 1 03:00)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("America/New_York location not available on host")
	}

	now := time.Date(2026, 10, 31, 23, 0, 0, 0, loc)
	// Reset is tomorrow at 10:00 (which will be in EST, UTC-5)
	raw := "resets at tomorrow at 10:00"

	got := ParseResetTime(raw, now.UTC(), pNY)
	if got == nil {
		t.Fatalf("expected reset time across DST, got nil")
	}

	// In America/New_York, Nov 1 10:00 is EST (UTC-5), so UTC is 15:00
	wantUTC := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	if !got.Equal(wantUTC) {
		t.Fatalf("DST test got %v, want %v", got, wantUTC)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
