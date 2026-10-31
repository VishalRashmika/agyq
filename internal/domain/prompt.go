package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// PromptNumber is the human number from the queue heading "# #12 ..." -> 12.
// Unique, ascending, never reused (docs/07-QUEUE-FORMAT-SPEC.md).
type PromptNumber int

// Prompt is one queue entry as parsed from the markdown file. Immutable.
type Prompt struct {
	Number PromptNumber
	Title  string // text after "#N" on the heading line; may be empty
	Body   string // everything under the heading until the next prompt heading, trimmed
	Hash   string // hex sha256 of Title + "\n" + Body; detects edits after execution
	Skip   bool   // true if prompt had <!-- agyq:skip --> directly under heading
	Line   int    // 1-based line number of the heading in the queue file
}

// ComputePromptHash calculates the sha256 hash of Title + "\n" + Body after CRLF->LF normalization.
func ComputePromptHash(title, body string) string {
	b := strings.Trim(strings.ReplaceAll(body, "\r\n", "\n"), "\n\r ")
	t := strings.TrimSpace(strings.ReplaceAll(title, "\r\n", "\n"))
	sum := sha256.Sum256([]byte(t + "\n" + b))
	return hex.EncodeToString(sum[:])
}

// SortPrompts returns prompts ordered strictly ascending by PromptNumber (F-003).
// Ordering is deterministic and independent of file line order.
func SortPrompts(prompts []Prompt) []Prompt {
	sorted := make([]Prompt, len(prompts))
	copy(sorted, prompts)
	// Simple insertion sort or standard slice sort without forbidden imports
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Number < sorted[j-1].Number; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted
}
