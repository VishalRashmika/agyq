package infrastructure

import (
	"context"
	"time"

	"agyq/internal/application"
)

// SystemClock is the production application.Clock (always UTC).
type SystemClock struct{}

// Now returns the current wall-clock time in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

var _ application.Clock = SystemClock{}

// SystemSleeper pauses execution using timers and respects context cancellation.
type SystemSleeper struct{}

// Sleep pauses for duration d or until ctx is cancelled.
func (SystemSleeper) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var _ application.Sleeper = SystemSleeper{}
