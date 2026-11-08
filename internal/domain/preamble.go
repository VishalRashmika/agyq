package domain

import (
	"fmt"
	"strings"
)

// ResumePreamble generates the exact guidance text prepended to an interrupted prompt (docs/06 §6).
func ResumePreamble(rec PromptRecord) string {
	n := len(rec.Attempts)
	if n < 1 {
		n = 1
	}
	template := `NOTE FROM THE AUTOMATION RUNNER: A previous attempt at this task (attempt {{N-1}}) was interrupted
(power loss, crash or timeout) before it was confirmed finished. The working tree may contain partial
or complete work from that attempt. Before doing anything else: run ` + "`git status` and `git diff`" + ` (and
look at recent commits tagged agyq/cp-{{NUM}} and later), work out what is already done, and then
finish ONLY what remains. Do not redo or duplicate completed work. Do not revert existing changes
unless they are clearly broken.`

	result := strings.ReplaceAll(template, "{{N-1}}", fmt.Sprintf("%d", n))
	result = strings.ReplaceAll(result, "{{NUM}}", fmt.Sprintf("%d", rec.Number))
	return result
}

// FailedRetryPreamble generates guidance text prepended to a prompt that previously failed
// and is now being retried at the end of the queue.
func FailedRetryPreamble(rec PromptRecord) string {
	n := len(rec.Attempts)
	lastErr := ""
	if n > 0 {
		lastErr = rec.Attempts[n-1].Error
	}
	template := `NOTE FROM THE AUTOMATION RUNNER: A previous attempt at this task (attempt {{N}}) failed with: {{ERROR}}.
This task is now being retried at the end of the queue. The working tree may contain partial work from previous attempts.
Before proceeding:
1. Inspect ` + "`git status` and `git diff`" + ` (and recent commits tagged agyq/cp-{{NUM}} and later) to see what was already created or modified.
2. Diagnose and fix any issue or error that caused the previous failure.
3. Complete the task as requested. Do not duplicate completed work.`

	result := strings.ReplaceAll(template, "{{N}}", fmt.Sprintf("%d", n))
	result = strings.ReplaceAll(result, "{{NUM}}", fmt.Sprintf("%d", rec.Number))
	if lastErr == "" {
		lastErr = "unspecified error"
	}
	result = strings.ReplaceAll(result, "{{ERROR}}", lastErr)
	return result
}
