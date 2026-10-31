package domain

import (
	"strings"
	"testing"
)

func TestValidateQueue_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMessage string
	}{
		{
			name: "Q-E1 duplicate prompt numbers",
			input: `# #1 First
Body one.

# #1 Duplicate
Body two.
`,
			wantMessage: "line 4: duplicate prompt number #1 (first defined on line 1)",
		},
		{
			name: "Q-E2 non-ascending numbers",
			input: `# #5 Task five
Body five.

# #2 Task two
Body two.
`,
			wantMessage: "line 4: prompt #2 is out of order (previous was #5); numbers must be strictly ascending",
		},
		{
			name: "Q-E3 empty body",
			input: `# #1 Empty body

# #2 Next task
Has content.
`,
			wantMessage: "line 1: prompt #1 has an empty body",
		},
		{
			name: "Q-E4 invalid prompt number (zero)",
			input: `# #0 Task zero
Body content.
`,
			wantMessage: `line 1: invalid prompt number "0" (must be 1–999999)`,
		},
		{
			name: "Q-E4 invalid prompt number (out of range)",
			input: `# #1000000 Task huge
Body content.
`,
			wantMessage: `line 1: invalid prompt number "1000000" (must be 1–999999)`,
		},
		{
			name: "Q-E5 no prompts found",
			input: `<!-- comments only -->
# Not a prompt heading
Just general documentation.
`,
			wantMessage: `no prompts found: expected headings like "# #1 Title"`,
		},
		{
			name:        "Q-E6 unterminated code fence",
			input:       "# #1 Task\n" + "```markdown\n" + "# #2 Unterminated fence\n",
			wantMessage: "line 2: unterminated code fence started on this line swallows the rest of the file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateQueueText(tc.input)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			valErr, ok := err.(*QueueValidationError)
			if !ok {
				t.Fatalf("expected *QueueValidationError, got %T: %v", err, err)
			}
			found := false
			for _, msg := range valErr.Errors {
				if strings.Contains(msg, tc.wantMessage) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("error did not contain %q\nGot errors:\n%s", tc.wantMessage, strings.Join(valErr.Errors, "\n"))
			}
		})
	}
}

func TestValidateQueue_CollectsMultipleErrors(t *testing.T) {
	input := `# #0 Invalid
# #2 Empty body

# #1 Out of order
`
	_, err := ValidateQueueText(input)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	valErr, ok := err.(*QueueValidationError)
	if !ok {
		t.Fatalf("expected *QueueValidationError, got %T", err)
	}

	// Should report invalid number 0, empty body for #2, and out of order for #1
	if len(valErr.Errors) < 3 {
		t.Errorf("expected at least 3 errors reported together, got %d: %v", len(valErr.Errors), valErr.Errors)
	}
}
