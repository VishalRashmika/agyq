package infrastructure

import (
	"testing"
	"time"
)

func TestSystemClockIsUTC(t *testing.T) {
	now := SystemClock{}.Now()
	if now.Location() != time.UTC {
		t.Fatalf("expected UTC, got %v", now.Location())
	}
}
