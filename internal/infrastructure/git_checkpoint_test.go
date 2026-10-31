package infrastructure

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v (out: %s)", strings.Join(args, " "), err, string(out))
		}
	}

	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	// Create initial file & commit
	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	return dir
}

func TestGitCheckpointer_F053_Checkpoint(t *testing.T) {
	repoDir := initTestGitRepo(t)
	cp := NewGitCheckpointer()
	ctx := context.Background()

	// 1. Create first checkpoint before Prompt 1
	ref1, err := cp.Checkpoint(ctx, repoDir, 1)
	if err != nil {
		t.Fatalf("checkpoint 1 failed: %v", err)
	}
	if ref1 != "agyq/cp-1" {
		t.Errorf("expected tag agyq/cp-1, got %s", ref1)
	}

	// Verify tag exists in repo
	tagOut, err := exec.Command("git", "-C", repoDir, "tag", "-l", "agyq/cp-1").Output()
	if err != nil || strings.TrimSpace(string(tagOut)) != "agyq/cp-1" {
		t.Errorf("tag agyq/cp-1 not found in repo: %s", string(tagOut))
	}

	// Verify commit author is agyq <agyq@localhost>
	authorOut, err := exec.Command("git", "-C", repoDir, "log", "-1", "--format=%an <%ae>", "agyq/cp-1").Output()
	if err != nil || strings.TrimSpace(string(authorOut)) != "agyq <agyq@localhost>" {
		t.Errorf("expected author 'agyq <agyq@localhost>', got: %q", string(authorOut))
	}

	// Verify commit message
	msgOut, err := exec.Command("git", "-C", repoDir, "log", "-1", "--format=%s", "agyq/cp-1").Output()
	if err != nil || strings.TrimSpace(string(msgOut)) != "agyq: checkpoint before #1" {
		t.Errorf("expected message 'agyq: checkpoint before #1', got: %q", string(msgOut))
	}

	// 2. Add edits and checkpoint Prompt 2
	err = os.WriteFile(filepath.Join(repoDir, "feature.txt"), []byte("prompt 1 work"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	ref2, err := cp.Checkpoint(ctx, repoDir, 2)
	if err != nil {
		t.Fatalf("checkpoint 2 failed: %v", err)
	}
	if ref2 != "agyq/cp-2" {
		t.Errorf("expected tag agyq/cp-2, got %s", ref2)
	}

	// Verify history is linear (cp-1 is ancestor of cp-2)
	ancestorCmd := exec.Command("git", "-C", repoDir, "merge-base", "--is-ancestor", "agyq/cp-1", "agyq/cp-2")
	if err := ancestorCmd.Run(); err != nil {
		t.Errorf("expected agyq/cp-1 to be ancestor of agyq/cp-2 (linear history)")
	}
}

func TestGitCheckpointer_F052_ResetTo(t *testing.T) {
	repoDir := initTestGitRepo(t)
	cp := NewGitCheckpointer()
	ctx := context.Background()

	// Checkpoint before prompt 1
	ref1, err := cp.Checkpoint(ctx, repoDir, 1)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate dirty partial work by prompt 1 (new file + edits)
	dirtyFile := filepath.Join(repoDir, "dirty.txt")
	_ = os.WriteFile(dirtyFile, []byte("uncommitted junk"), 0644)
	_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Corrupted\n"), 0644)

	// ResetTo ref1 with force=true (restart-clean policy)
	if err := cp.ResetTo(ctx, repoDir, ref1, true); err != nil {
		t.Fatalf("ResetTo failed: %v", err)
	}

	// Verify dirty.txt was removed
	if _, err := os.Stat(dirtyFile); !os.IsNotExist(err) {
		t.Errorf("expected dirty.txt to be removed by git clean")
	}

	// Verify README.md was restored
	content, err := os.ReadFile(filepath.Join(repoDir, "README.md"))
	if err != nil || string(content) != "# Hello\n" {
		t.Errorf("expected README.md restored to # Hello, got: %q", string(content))
	}

	// Verify status is clean
	statusOut, err := exec.Command("git", "-C", repoDir, "status", "--porcelain").Output()
	if err != nil || len(strings.TrimSpace(string(statusOut))) > 0 {
		t.Errorf("expected clean working tree after ResetTo, got: %s", string(statusOut))
	}
}
