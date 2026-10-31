package domain_test

import (
	"strings"
	"testing"

	"agyq/internal/domain"
)

func TestResumePreamble_GoldenText(t *testing.T) {
	rec := domain.PromptRecord{
		Number: 7,
		Attempts: []domain.Attempt{
			{N: 1, Outcome: domain.OutcomeInterrupted},
		},
	}

	preamble := domain.ResumePreamble(rec)

	expectedGolden := `NOTE FROM THE AUTOMATION RUNNER: A previous attempt at this task (attempt 1) was interrupted
(power loss, crash or timeout) before it was confirmed finished. The working tree may contain partial
or complete work from that attempt. Before doing anything else: run ` + "`git status` and `git diff`" + ` (and
look at recent commits tagged agyq/cp-7 and later), work out what is already done, and then
finish ONLY what remains. Do not redo or duplicate completed work. Do not revert existing changes
unless they are clearly broken.`

	if preamble != expectedGolden {
		t.Fatalf("ResumePreamble mismatch.\nExpected:\n%s\nGot:\n%s", expectedGolden, preamble)
	}

	// Verify with higher attempt number and different prompt number
	rec2 := domain.PromptRecord{
		Number: 42,
		Attempts: []domain.Attempt{
			{N: 1, Outcome: domain.OutcomeInterrupted},
			{N: 2, Outcome: domain.OutcomeInterrupted},
			{N: 3, Outcome: domain.OutcomeInterrupted},
		},
	}
	preamble2 := domain.ResumePreamble(rec2)
	if !strings.Contains(preamble2, "attempt 3") {
		t.Errorf("Expected attempt 3 in preamble, got: %s", preamble2)
	}
	if !strings.Contains(preamble2, "agyq/cp-42") {
		t.Errorf("Expected agyq/cp-42 in preamble, got: %s", preamble2)
	}
}
