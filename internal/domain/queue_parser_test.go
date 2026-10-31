package domain

import (
	"strings"
	"testing"
)

const queueExampleMD = `<!-- Example queue. Anything before the first "# #N" heading is ignored. -->

# #1 Scaffold the project
Create a Go module named ` + "`demo`" + ` with a ` + "`main.go`" + ` that prints "hello".
Done when ` + "`go build ./...`" + ` succeeds.

# #2 Add a greeting package
Create package ` + "`greet`" + ` with ` + "`func Hello(name string) string`" + ` and a table-driven test.

Example of a fenced block that must NOT be parsed as a heading:

` + "```markdown" + `
# #9 this is just text inside a code fence
` + "```" + `

Done when ` + "`go test ./...`" + ` passes.

# #3
A prompt with no title text. Add a README section describing the ` + "`greet`" + ` package.

# #4: Colon-style title
Wire ` + "`greet.Hello`" + ` into ` + "`main.go`" + ` and accept a ` + "`-name`" + ` flag.

# #5 - Dash-style title
Add a GitHub Actions workflow that runs ` + "`go vet`" + ` and ` + "`go test`" + `.
`

func TestParseQueue_GoldenExample(t *testing.T) {
	prompts, err := ParseQueueText(queueExampleMD)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(prompts) != 5 {
		t.Fatalf("expected 5 prompts, got %d", len(prompts))
	}

	// Prompt 1
	if prompts[0].Number != 1 {
		t.Errorf("prompt 0 number = %d, want 1", prompts[0].Number)
	}
	if prompts[0].Title != "Scaffold the project" {
		t.Errorf("prompt 0 title = %q, want %q", prompts[0].Title, "Scaffold the project")
	}
	wantBody1 := "Create a Go module named `demo` with a `main.go` that prints \"hello\".\nDone when `go build ./...` succeeds."
	if prompts[0].Body != wantBody1 {
		t.Errorf("prompt 0 body = %q, want %q", prompts[0].Body, wantBody1)
	}

	// Prompt 2 (contains code fence with # #9)
	if prompts[1].Number != 2 {
		t.Errorf("prompt 1 number = %d, want 2", prompts[1].Number)
	}
	if prompts[1].Title != "Add a greeting package" {
		t.Errorf("prompt 1 title = %q, want %q", prompts[1].Title, "Add a greeting package")
	}
	wantBody2 := "Create package `greet` with `func Hello(name string) string` and a table-driven test.\n\nExample of a fenced block that must NOT be parsed as a heading:\n\n```markdown\n# #9 this is just text inside a code fence\n```\n\nDone when `go test ./...` passes."
	if prompts[1].Body != wantBody2 {
		t.Errorf("prompt 1 body = %q, want %q", prompts[1].Body, wantBody2)
	}

	// Prompt 3 (empty title)
	if prompts[2].Number != 3 {
		t.Errorf("prompt 2 number = %d, want 3", prompts[2].Number)
	}
	if prompts[2].Title != "" {
		t.Errorf("prompt 2 title = %q, want empty", prompts[2].Title)
	}
	wantBody3 := "A prompt with no title text. Add a README section describing the `greet` package."
	if prompts[2].Body != wantBody3 {
		t.Errorf("prompt 2 body = %q, want %q", prompts[2].Body, wantBody3)
	}

	// Prompt 4 (Colon-style title)
	if prompts[3].Number != 4 {
		t.Errorf("prompt 3 number = %d, want 4", prompts[3].Number)
	}
	if prompts[3].Title != "Colon-style title" {
		t.Errorf("prompt 3 title = %q, want %q", prompts[3].Title, "Colon-style title")
	}
	wantBody4 := "Wire `greet.Hello` into `main.go` and accept a `-name` flag."
	if prompts[3].Body != wantBody4 {
		t.Errorf("prompt 3 body = %q, want %q", prompts[3].Body, wantBody4)
	}

	// Prompt 5 (Dash-style title)
	if prompts[4].Number != 5 {
		t.Errorf("prompt 4 number = %d, want 5", prompts[4].Number)
	}
	if prompts[4].Title != "Dash-style title" {
		t.Errorf("prompt 4 title = %q, want %q", prompts[4].Title, "Dash-style title")
	}
	wantBody5 := "Add a GitHub Actions workflow that runs `go vet` and `go test`."
	if prompts[4].Body != wantBody5 {
		t.Errorf("prompt 4 body = %q, want %q", prompts[4].Body, wantBody5)
	}
}

func TestParseQueue_FencedCodeBlocks(t *testing.T) {
	md := `# #1 First
Some body.

` + "```" + `
# #2 Fake heading in backticks
` + "```" + `

More body.

~~~
# #3 Fake heading in tildes
~~~

End of prompt 1.

# #4 Second
Second prompt body.
`

	prompts, err := ParseQueueText(md)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	if prompts[0].Number != 1 || prompts[1].Number != 4 {
		t.Fatalf("expected prompts 1 and 4, got %d and %d", prompts[0].Number, prompts[1].Number)
	}
	if !strings.Contains(prompts[0].Body, "# #2 Fake heading in backticks") {
		t.Errorf("prompt 1 body should contain fenced line")
	}
	if !strings.Contains(prompts[0].Body, "# #3 Fake heading in tildes") {
		t.Errorf("prompt 1 body should contain tilde fenced line")
	}
}

func TestParseQueue_CRLFandLF_IdenticalHash(t *testing.T) {
	lf := "# #1 Task Title\nLine one of task.\nLine two of task.\n"
	crlf := "# #1 Task Title\r\nLine one of task.\r\nLine two of task.\r\n"

	pLF, err := ParseQueueText(lf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pCRLF, err := ParseQueueText(crlf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(pLF) != 1 || len(pCRLF) != 1 {
		t.Fatalf("expected 1 prompt from both, got %d and %d", len(pLF), len(pCRLF))
	}
	if pLF[0].Hash != pCRLF[0].Hash {
		t.Errorf("hash mismatch: LF=%q, CRLF=%q", pLF[0].Hash, pCRLF[0].Hash)
	}
	if pLF[0].Body != pCRLF[0].Body {
		t.Errorf("body mismatch: LF=%q, CRLF=%q", pLF[0].Body, pCRLF[0].Body)
	}
}

func TestParseQueue_PreambleAndBOM(t *testing.T) {
	mdWithBOM := "\xef\xbb\xbf<!-- preamble note -->\nSome initial remarks.\n\n# #1 Real Task\nTask content.\n"
	prompts, err := ParseQueueText(mdWithBOM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	if prompts[0].Number != 1 {
		t.Errorf("prompt number = %d, want 1", prompts[0].Number)
	}
	if prompts[0].Title != "Real Task" {
		t.Errorf("prompt title = %q, want Real Task", prompts[0].Title)
	}
	if prompts[0].Body != "Task content." {
		t.Errorf("prompt body = %q, want Task content.", prompts[0].Body)
	}
}
