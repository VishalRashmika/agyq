package infrastructure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"agyq/internal/application"
)

// AgyProcessRunner implements application.AgyRunner using the headless print mode of agy.
type AgyProcessRunner struct {
	cfg         application.Config
	checker     application.PreflightChecker
	gracePeriod time.Duration
	tailLimit   int
}

// NewAgyProcessRunner constructs an AgyProcessRunner.
func NewAgyProcessRunner(cfg application.Config) *AgyProcessRunner {
	return &AgyProcessRunner{
		cfg:         cfg,
		gracePeriod: 10 * time.Second,
		tailLimit:   1024 * 1024, // 1 MiB
	}
}

// WithGracePeriod sets the shutdown grace period between SIGTERM and SIGKILL.
func (r *AgyProcessRunner) WithGracePeriod(d time.Duration) *AgyProcessRunner {
	r.gracePeriod = d
	return r
}

// WithTailLimit sets the in-memory bounded tail limit for stdout and stderr.
func (r *AgyProcessRunner) WithTailLimit(limit int) *AgyProcessRunner {
	r.tailLimit = limit
	return r
}

// WithChecker injects a custom preflight checker.
func (r *AgyProcessRunner) WithChecker(c application.PreflightChecker) *AgyProcessRunner {
	r.checker = c
	return r
}

// Preflight runs environment checks using the preflight checker.
func (r *AgyProcessRunner) Preflight(ctx context.Context) (application.PreflightReport, error) {
	if r.checker != nil {
		return r.checker.Check(ctx, &r.cfg)
	}
	checker := NewPreflightService(nil)
	return checker.Check(ctx, &r.cfg)
}

// BuildArgv constructs the command line arguments for the agy invocation per docs/05 §2.
func (r *AgyProcessRunner) BuildArgv(req application.RunRequest) (string, []string, error) {
	promptText := req.Prompt.Body
	if req.Preamble != "" {
		promptText = req.Preamble + "\n\n--- ORIGINAL TASK ---\n\n" + req.Prompt.Body
	}

	if len(promptText) > 100_000 {
		return "", nil, fmt.Errorf("prompt exceeds 100KB limit: %d bytes", len(promptText))
	}

	binary := r.cfg.Agy.Binary
	if binary == "" {
		binary = "agy"
	}

	args := []string{
		"-p", promptText,
		"--output-format", "json",
	}

	printTimeout := "85m"
	if req.Timeout > 5*time.Minute {
		printTimeout = formatDuration(req.Timeout - 5*time.Minute)
	} else if req.Timeout > 0 {
		printTimeout = formatDuration(req.Timeout)
	}
	args = append(args, "--print-timeout", printTimeout)

	if r.cfg.Agy.Model != "" {
		args = append(args, "--model", r.cfg.Agy.Model)
	}
	if r.cfg.Agy.Effort != "" {
		args = append(args, "--effort", r.cfg.Agy.Effort)
	}

	workDir := req.WorkDir
	if workDir == "" {
		workDir = r.cfg.WorkDir
	}
	if workDir != "" {
		args = append(args, "--add-dir", workDir)
	}

	if req.ConversationID != "" {
		args = append(args, "--conversation", req.ConversationID)
	}

	if r.cfg.Agy.PermissionMode == "dangerously-skip-permissions" {
		args = append(args, "--dangerously-skip-permissions")
	}

	if len(r.cfg.Agy.ExtraArgs) > 0 {
		args = append(args, r.cfg.Agy.ExtraArgs...)
	}

	return binary, args, nil
}

// Run executes a prompt turn against agy in headless print mode.
func (r *AgyProcessRunner) Run(ctx context.Context, req application.RunRequest) (application.RawRunResult, error) {
	binary, args, err := r.BuildArgv(req)
	if err != nil {
		return application.RawRunResult{}, err
	}

	stateDir := req.StateDir
	if stateDir == "" {
		stateDir = r.cfg.StateDir
	}
	if stateDir == "" {
		stateDir = ".agyq"
	}
	attempt := req.AttemptNumber
	if attempt <= 0 {
		attempt = 1
	}
	transcriptRel := fmt.Sprintf("transcripts/%04d-a%d.log", req.Prompt.Number, attempt)
	transcriptPath := filepath.Join(stateDir, transcriptRel)

	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0700); err != nil {
		return application.RawRunResult{}, fmt.Errorf("create transcript dir: %w", err)
	}

	transcriptFile, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return application.RawRunResult{}, fmt.Errorf("create transcript file: %w", err)
	}
	defer transcriptFile.Close()

	stdoutTail := NewTailBuffer(r.tailLimit)
	stderrTail := NewTailBuffer(r.tailLimit)
	syncedTranscript := &syncWriter{w: transcriptFile}

	stdoutWriter := io.MultiWriter(syncedTranscript, stdoutTail)
	stderrWriter := io.MultiWriter(syncedTranscript, stderrTail)

	workDir := req.WorkDir
	if workDir == "" {
		workDir = r.cfg.WorkDir
	}

	cmd := exec.Command(binary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdin = bytes.NewReader(nil)
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return application.RawRunResult{
			ExitCode:       127,
			TranscriptPath: transcriptRel,
		}, fmt.Errorf("launch agy: %w", err)
	}

	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var timedOut bool
	var killed bool

	var timeoutCh <-chan time.Time
	if req.Timeout > 0 {
		timer := time.NewTimer(req.Timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case err := <-done:
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		return buildRunResult(exitCode, false, false, stdoutTail.Bytes(), stderrTail.Bytes(), transcriptRel), nil

	case <-timeoutCh:
		timedOut = true
	case <-ctx.Done():
		killed = true
	}

	// Send SIGTERM to process group
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	graceTimer := time.NewTimer(r.gracePeriod)
	defer graceTimer.Stop()

	select {
	case err := <-done:
		exitCode := 1
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		return buildRunResult(exitCode, timedOut, killed, stdoutTail.Bytes(), stderrTail.Bytes(), transcriptRel), nil

	case <-graceTimer.C:
		// Grace period expired, escalate to SIGKILL on process group
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		err := <-done
		exitCode := 137
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}
		return buildRunResult(exitCode, timedOut, killed, stdoutTail.Bytes(), stderrTail.Bytes(), transcriptRel), nil
	}
}

func buildRunResult(exitCode int, timedOut, killed bool, stdout, stderr []byte, transcriptRel string) application.RawRunResult {
	env := ParseEnvelope(stdout)
	return application.RawRunResult{
		ExitCode:       exitCode,
		TimedOut:       timedOut,
		Killed:         killed,
		Stdout:         stdout,
		Stderr:         stderr,
		Envelope:       env,
		TranscriptPath: transcriptRel,
	}
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%time.Minute == 0 && d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d%time.Second == 0 && d >= time.Second {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return d.String()
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
