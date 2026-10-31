package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileQueueSource_Load(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.md")
	content := `# #1 First Task
Body of first task.

# #2 Second Task
Body of second task.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test queue file: %v", err)
	}

	src := NewFileQueueSource()
	prompts, err := src.Load(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	if prompts[0].Number != 1 || prompts[1].Number != 2 {
		t.Errorf("prompt numbers: got %d and %d, want 1 and 2", prompts[0].Number, prompts[1].Number)
	}
}

func TestFileQueueSource_NotFound(t *testing.T) {
	src := NewFileQueueSource()
	_, err := src.Load(context.Background(), "/nonexistent/queue.md")
	if err == nil {
		t.Fatal("expected error loading nonexistent queue file, got nil")
	}
}

func TestFileQueueSource_GoldenFile(t *testing.T) {
	src := NewFileQueueSource()
	prompts, err := src.Load(context.Background(), "../../examples/queue.example.md")
	if err != nil {
		t.Fatalf("failed to load golden examples/queue.example.md: %v", err)
	}
	if len(prompts) != 5 {
		t.Fatalf("expected 5 prompts, got %d", len(prompts))
	}
	for i, wantNum := range []int{1, 2, 3, 4, 5} {
		if int(prompts[i].Number) != wantNum {
			t.Errorf("prompt[%d].Number = %d, want %d", i, prompts[i].Number, wantNum)
		}
	}
}
