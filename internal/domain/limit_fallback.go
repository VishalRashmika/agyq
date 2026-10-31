package domain

import (
	"time"
)

// FallbackPause computes the next probe deadline when reset time is unknown (F-043).
// It applies jitter to the configured session or weekly probe duration.
func FallbackPause(now time.Time, kind LimitKind, c BackoffConfig, jitter float64) time.Time {
	base := c.SessionProbe
	if kind == LimitWeekly {
		base = c.WeeklyProbe
		if base <= 0 {
			base = 3 * time.Hour
		}
	} else {
		if base <= 0 {
			base = 30 * time.Minute
		}
	}

	j := c.Jitter
	if j <= 0 {
		j = 0.1
	}

	// jitter is in [0.0, 1.0); maps to [-j, +j]
	factor := 1.0 + (jitter*2.0-1.0)*j
	dur := time.Duration(float64(base) * factor)
	if dur < 0 {
		dur = 0
	}

	return now.Add(dur).UTC()
}

// IsWeeklySuspected reports whether the prompt has hit >= 3 consecutive limits within 6 hours (F-043).
func IsWeeklySuspected(attempts []Attempt, now time.Time) bool {
	if len(attempts) < 3 {
		return false
	}

	// Count consecutive limit attempts from the end
	consecutiveCount := 0
	var oldestLimitAttempt *Attempt

	for i := len(attempts) - 1; i >= 0; i-- {
		att := &attempts[i]
		if att.Outcome == OutcomeLimit {
			consecutiveCount++
			oldestLimitAttempt = att
		} else {
			break
		}
	}

	if consecutiveCount < 3 || oldestLimitAttempt == nil {
		return false
	}

	// Must be within 6 hours
	return now.Sub(oldestLimitAttempt.StartedAt) <= 6*time.Hour
}
