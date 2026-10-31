package infrastructure

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"agyq/internal/domain"
)

// GitCheckpointer implements application.Checkpointer using the git CLI (F-052, F-053).
type GitCheckpointer struct{}

// NewGitCheckpointer constructs a GitCheckpointer.
func NewGitCheckpointer() *GitCheckpointer {
	return &GitCheckpointer{}
}

// Checkpoint creates a git commit and tag agyq/cp-N before attempt 1 of prompt N (F-053).
func (g *GitCheckpointer) Checkpoint(ctx context.Context, workDir string, n domain.PromptNumber) (string, error) {
	if workDir == "" {
		return "", fmt.Errorf("workDir cannot be empty")
	}

	// 1. git add -A
	addCmd := exec.CommandContext(ctx, "git", "-c", "user.name=agyq", "-c", "user.email=agyq@localhost", "add", "-A")
	addCmd.Dir = workDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// 2. git commit --allow-empty --no-verify -m "agyq: checkpoint before #N"
	msg := fmt.Sprintf("agyq: checkpoint before #%d", n)
	if n == 0 {
		msg = "agyq: queue completed"
	}
	commitCmd := exec.CommandContext(ctx, "git", "-c", "user.name=agyq", "-c", "user.email=agyq@localhost", "commit", "--allow-empty", "--no-verify", "-m", msg)
	commitCmd.Dir = workDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// 3. git tag -f agyq/cp-N
	tagName := fmt.Sprintf("agyq/cp-%d", n)
	if n == 0 {
		tagName = "agyq/completed"
	}
	tagCmd := exec.CommandContext(ctx, "git", "-c", "user.name=agyq", "-c", "user.email=agyq@localhost", "tag", "-f", tagName)
	tagCmd.Dir = workDir
	if out, err := tagCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git tag failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return tagName, nil
}

// ResetTo rolls back workDir to ref via git reset --hard and git clean -fd (F-052).
func (g *GitCheckpointer) ResetTo(ctx context.Context, workDir, ref string, force bool) error {
	if workDir == "" {
		return fmt.Errorf("workDir cannot be empty")
	}
	if ref == "" {
		return fmt.Errorf("ref cannot be empty")
	}

	if !force {
		// Check for uncommitted changes
		statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		statusCmd.Dir = workDir
		out, err := statusCmd.Output()
		if err != nil {
			return fmt.Errorf("git status failed: %w", err)
		}
		if len(bytes.TrimSpace(out)) > 0 {
			// Check if HEAD has ref in ancestry (i.e. HEAD is on the checkpoint chain)
			ancestorCmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ref, "HEAD")
			ancestorCmd.Dir = workDir
			if err := ancestorCmd.Run(); err != nil {
				// HEAD is not on the checkpoint chain; abort without --force
				return fmt.Errorf("uncommitted changes exist while HEAD is not on checkpoint chain %s; use --force to reset", ref)
			}
		}
	}

	// git reset --hard <ref>
	resetCmd := exec.CommandContext(ctx, "git", "reset", "--hard", ref)
	resetCmd.Dir = workDir
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// git clean -fd
	cleanCmd := exec.CommandContext(ctx, "git", "clean", "-fd")
	cleanCmd.Dir = workDir
	if out, err := cleanCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean -fd failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	return nil
}
