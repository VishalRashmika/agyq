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
