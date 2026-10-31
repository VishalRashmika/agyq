package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
	"agyq/internal/infrastructure/testutil"
)

func TestRunner_F020_ArgvAndByteIdenticalPrompt(t *testing.T) {
	fakeAgyBin := testutil.BuildFakeAgy(t)
	tmpDir := t.TempDir()
	recFile := filepath.Join(tmpDir, "argv_record.jsonl")

	workDir := filepath.Join(tmpDir, "workrepo")
	stateDir := filepath.Join(tmpDir, "statedir")
	_ = os.MkdirAll(workDir, 0700)
	_ = os.MkdirAll(stateDir, 0700)

	cfg := application.DefaultConfig()
	cfg.Agy.Binary = fakeAgyBin
	cfg.Agy.Model = "gemini-3.8-flash-high"
	cfg.Agy.Effort = "high"
	cfg.Agy.PermissionMode = "settings-allowlist"
	cfg.PromptTimeout = 90 * time.Minute
	cfg.WorkDir = workDir
	cfg.StateDir = stateDir

	// In test, inject FAKEAGY_RECORD_FILE into environment
	t.Setenv("FAKEAGY_RECORD_FILE", recFile)
	t.Setenv("FAKEAGY_SCENARIO", "ok")

	runner := NewAgyProcessRunner(cfg)

	complexPromptBody := "echo 'quotes \"double\" and `backticks` and $(pwd)'\nline 2 with symbols: $FOO & * < > ;"
	req := application.RunRequest{
		Prompt: domain.Prompt{
			Number: 42,
			Title:  "Complex Prompt Test",
			Body:   complexPromptBody,
		},
		Mode:          domain.RunFresh,
		WorkDir:       workDir,
		StateDir:      stateDir,
		Timeout:       90 * time.Minute,
		AttemptNumber: 1,
	}

	ctx := context.Background()
	res, err := runner.Run(ctx, req)
	if err != nil {
		t.Fatalf("runner.Run failed: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("exit code got %d, want 0", res.ExitCode)
	}
	if res.Envelope == nil {
		t.Fatal("expected non-nil envelope")
	}
	if res.Envelope.Status != "SUCCESS" {
		t.Errorf("envelope status got %q, want SUCCESS", res.Envelope.Status)
	}

	// Read recorded args
	recData, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}
	var rec struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(recData), &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}

	// 1. Verify working directory was set to WorkDir
	if rec.Cwd != workDir {
		t.Errorf("cwd got %q, want %q", rec.Cwd, workDir)
	}

	// 2. Verify prompt was received byte-identical as a single argv element (no shell expansion)
	if len(rec.Args) < 2 || rec.Args[0] != "-p" {
		t.Fatalf("expected args to start with -p, got: %v", rec.Args)
	}
	receivedPrompt := rec.Args[1]
	if receivedPrompt != complexPromptBody {
		t.Fatalf("prompt was altered in transit:\ngot:  %q\nwant: %q", receivedPrompt, complexPromptBody)
	}

	// 3. Golden argv check
	goldenArgs := []string{
		"-p", complexPromptBody,
		"--output-format", "json",
		"--print-timeout", "85m",
		"--model", "gemini-3.8-flash-high",
		"--effort", "high",
		"--add-dir", workDir,
	}
	if !reflect.DeepEqual(rec.Args, goldenArgs) {
		t.Errorf("recorded args do not match golden:\ngot:  %v\nwant: %v", rec.Args, goldenArgs)
	}

	// 4. Check transcript file created (F-024)
	expectedTranscript := filepath.Join(stateDir, "transcripts/0042-a1.log")
	transcriptContent, err := os.ReadFile(expectedTranscript)
	if err != nil {
		t.Fatalf("failed to read transcript file %s: %v", expectedTranscript, err)
	}
	if !strings.Contains(string(transcriptContent), "SUCCESS") {
		t.Errorf("transcript does not contain SUCCESS: %s", string(transcriptContent))
	}
}

func TestRunner_F021_ProcessGroupKillOnTimeout(t *testing.T) {
	fakeAgyBin := testutil.BuildFakeAgy(t)
	tmpDir := t.TempDir()

	cfg := application.DefaultConfig()
	cfg.Agy.Binary = fakeAgyBin

	t.Setenv("FAKEAGY_SCENARIO", "ignore-sigterm")

	// Set short grace period (100ms) for fast test execution
	runner := NewAgyProcessRunner(cfg).WithGracePeriod(100 * time.Millisecond)

	req := application.RunRequest{
		Prompt: domain.Prompt{
			Number: 1,
			Title:  "Hanging task",
			Body:   "hang forever",
		},
		Mode:          domain.RunFresh,
		WorkDir:       tmpDir,
		StateDir:      tmpDir,
		Timeout:       200 * time.Millisecond, // times out quickly
		AttemptNumber: 1,
	}

	start := time.Now()
	res, err := runner.Run(context.Background(), req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("expected TimedOut=true, got %v", res.TimedOut)
	}
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code on timeout kill, got %d", res.ExitCode)
	}
	if elapsed > 3*time.Second {
		t.Errorf("kill took too long: %v", elapsed)
	}
}

func TestRunner_F021_ProcessGroupKillOnContextCancel(t *testing.T) {
	fakeAgyBin := testutil.BuildFakeAgy(t)
	tmpDir := t.TempDir()

	cfg := application.DefaultConfig()
	cfg.Agy.Binary = fakeAgyBin

	t.Setenv("FAKEAGY_SCENARIO", "ignore-sigterm")

	runner := NewAgyProcessRunner(cfg).WithGracePeriod(100 * time.Millisecond)

	req := application.RunRequest{
		Prompt: domain.Prompt{
			Number: 1,
			Title:  "Hanging task",
			Body:   "hang forever",
		},
		Mode:          domain.RunFresh,
		WorkDir:       tmpDir,
		StateDir:      tmpDir,
		Timeout:       10 * time.Minute,
		AttemptNumber: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	res, err := runner.Run(ctx, req)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.Killed {
		t.Errorf("expected Killed=true, got %v", res.Killed)
	}
}

func TestRunner_F026_PermissionModeArgvHandling(t *testing.T) {
	fakeAgyBin := testutil.BuildFakeAgy(t)
	tmpDir := t.TempDir()

	// Mode 1: dangerously-skip-permissions
	cfgSkip := application.DefaultConfig()
	cfgSkip.Agy.Binary = fakeAgyBin
	cfgSkip.Agy.PermissionMode = "dangerously-skip-permissions"
	cfgSkip.WorkDir = tmpDir
	runnerSkip := NewAgyProcessRunner(cfgSkip)

	req := application.RunRequest{
		Prompt: domain.Prompt{Number: 1, Body: "task 1"},
	}
	_, argsSkip, err := runnerSkip.BuildArgv(req)
	if err != nil {
		t.Fatal(err)
	}

	foundSkip := false
	for _, a := range argsSkip {
		if a == "--dangerously-skip-permissions" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("expected --dangerously-skip-permissions in args, got: %v", argsSkip)
	}

	// Mode 2: settings-allowlist (default)
	cfgAllow := application.DefaultConfig()
	cfgAllow.Agy.Binary = fakeAgyBin
	cfgAllow.Agy.PermissionMode = "settings-allowlist"
	cfgAllow.WorkDir = tmpDir
	runnerAllow := NewAgyProcessRunner(cfgAllow)

	_, argsAllow, err := runnerAllow.BuildArgv(req)
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range argsAllow {
		if a == "--dangerously-skip-permissions" {
			t.Errorf("did NOT expect --dangerously-skip-permissions in allowlist mode, got: %v", argsAllow)
		}
	}
}

func TestRunner_PromptLengthCap(t *testing.T) {
	cfg := application.DefaultConfig()
	runner := NewAgyProcessRunner(cfg)

	oversizedBody := strings.Repeat("A", 100_001)
	req := application.RunRequest{
		Prompt: domain.Prompt{Number: 1, Body: oversizedBody},
	}
	_, _, err := runner.BuildArgv(req)
	if err == nil || !strings.Contains(err.Error(), "100KB limit") {
		t.Fatalf("expected 100KB limit error, got: %v", err)
	}
}

func TestRunner_F051_ResumePreambleInArgv(t *testing.T) {
	fakeAgyBin := testutil.BuildFakeAgy(t)
	tmpDir := t.TempDir()
	recFile := filepath.Join(tmpDir, "argv_record.jsonl")

	cfg := application.DefaultConfig()
	cfg.Agy.Binary = fakeAgyBin
	cfg.WorkDir = tmpDir
	runner := NewAgyProcessRunner(cfg)

	t.Setenv("FAKEAGY_RECORD_FILE", recFile)
	t.Setenv("FAKEAGY_SCENARIO", "ok")

	rec := domain.PromptRecord{
		Number: 3,
		Attempts: []domain.Attempt{
			{N: 1, Outcome: domain.OutcomeInterrupted},
		},
	}
	preamble := domain.ResumePreamble(rec)
	originalBody := "Fix the failing database query."

	req := application.RunRequest{
		Prompt: domain.Prompt{
			Number: 3,
			Title:  "Fix DB Query",
			Body:   originalBody,
		},
		Mode:          domain.RunResume,
		Preamble:      preamble,
		WorkDir:       tmpDir,
		StateDir:      tmpDir,
		Timeout:       10 * time.Minute,
		AttemptNumber: 2,
	}

	_, args, err := runner.BuildArgv(req)
	if err != nil {
		t.Fatal(err)
	}

	// Verify argv includes preamble before original body
	promptArg := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-p" && i+1 < len(args) {
			promptArg = args[i+1]
			break
		}
	}
	if promptArg == "" {
		t.Fatal("no -p prompt argument found in argv")
	}

	preambleIdx := strings.Index(promptArg, "NOTE FROM THE AUTOMATION RUNNER")
	bodyIdx := strings.Index(promptArg, originalBody)
	markerIdx := strings.Index(promptArg, "--- ORIGINAL TASK ---")

	if preambleIdx == -1 {
		t.Fatal("preamble not found in prompt argument")
	}
	if bodyIdx == -1 {
		t.Fatal("original body not found in prompt argument")
	}
	if markerIdx == -1 {
		t.Fatal("--- ORIGINAL TASK --- separator not found in prompt argument")
	}
	if !(preambleIdx < markerIdx && markerIdx < bodyIdx) {
		t.Fatalf("expected order preamble < marker < body, but got indices: %d, %d, %d", preambleIdx, markerIdx, bodyIdx)
	}

	// Also execute via Run and verify recorded args
	res, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	data, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &recorded); err != nil {
		t.Fatal(err)
	}
	recordedPrompt := ""
	for i := 0; i < len(recorded.Args); i++ {
		if recorded.Args[i] == "-p" && i+1 < len(recorded.Args) {
			recordedPrompt = recorded.Args[i+1]
			break
		}
	}
	if !strings.HasPrefix(recordedPrompt, "NOTE FROM THE AUTOMATION RUNNER") {
		t.Errorf("recorded prompt does not start with preamble: %s", recordedPrompt)
	}
	if !strings.HasSuffix(recordedPrompt, originalBody) {
		t.Errorf("recorded prompt does not end with original body: %s", recordedPrompt)
	}
}
