package infrastructure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
)

func TestParseConfig_Defaults(t *testing.T) {
	// Empty JSON object should yield all default values
	data := []byte(`{}`)
	cfg, err := ParseConfig(data, "/custom/dir")
	if err != nil {
		t.Fatalf("unexpected error parsing empty config: %v", err)
	}

	def := application.DefaultConfig()

	if cfg.PromptTimeout != def.PromptTimeout {
		t.Errorf("PromptTimeout = %v, want default %v", cfg.PromptTimeout, def.PromptTimeout)
	}
	if cfg.Recovery != def.Recovery {
		t.Errorf("Recovery = %v, want default %v", cfg.Recovery, def.Recovery)
	}
	if cfg.OnFailure != def.OnFailure {
		t.Errorf("OnFailure = %v, want default %v", cfg.OnFailure, def.OnFailure)
	}
	if cfg.Agy.Binary != def.Agy.Binary {
		t.Errorf("Agy.Binary = %q, want %q", cfg.Agy.Binary, def.Agy.Binary)
	}
	if cfg.Agy.PermissionMode != def.Agy.PermissionMode {
		t.Errorf("Agy.PermissionMode = %q, want %q", cfg.Agy.PermissionMode, def.Agy.PermissionMode)
	}
	if cfg.Retry.MaxAttempts != def.Retry.MaxAttempts {
		t.Errorf("Retry.MaxAttempts = %d, want %d", cfg.Retry.MaxAttempts, def.Retry.MaxAttempts)
	}
	if len(cfg.Retry.Backoff) != len(def.Retry.Backoff) {
		t.Errorf("Retry.Backoff length = %d, want %d", len(cfg.Retry.Backoff), len(def.Retry.Backoff))
	}
	if cfg.Limits.Timezone != def.Limits.Timezone {
		t.Errorf("Limits.Timezone = %q, want %q", cfg.Limits.Timezone, def.Limits.Timezone)
	}
	if cfg.Limits.Fallback.SessionProbe != def.Limits.Fallback.SessionProbe {
		t.Errorf("Fallback.SessionProbe = %v, want %v", cfg.Limits.Fallback.SessionProbe, def.Limits.Fallback.SessionProbe)
	}
}

func TestParseConfig_UnknownKeyRejection(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "top-level unknown key",
			json: `{"unknown_top_field": 123}`,
		},
		{
			name: "unknown key in agy",
			json: `{"agy": {"unknown_agy_field": "val"}}`,
		},
		{
			name: "unknown key in retry",
			json: `{"retry": {"bogus": true}}`,
		},
		{
			name: "unknown key in limits",
			json: `{"limits": {"extra_limits": []}}`,
		},
		{
			name: "unknown key in limits.fallback",
			json: `{"limits": {"fallback": {"extra": 1}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.json), "")
			if err == nil {
				t.Fatalf("expected error for unknown key in %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("expected error message to mention 'unknown field', got %v", err)
			}
		})
	}
}

func TestParseConfig_BadDuration(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "bad prompt_timeout string",
			json: `{"prompt_timeout": "invalid-duration"}`,
		},
		{
			name: "negative prompt_timeout",
			json: `{"prompt_timeout": "-10m"}`,
		},
		{
			name: "zero prompt_timeout",
			json: `{"prompt_timeout": "0s"}`,
		},
		{
			name: "bad retry backoff string",
			json: `{"retry": {"backoff": ["1m", "not-a-duration"]}}`,
		},
		{
			name: "negative retry backoff",
			json: `{"retry": {"backoff": ["-5s"]}}`,
		},
		{
			name: "bad session_probe",
			json: `{"limits": {"fallback": {"session_probe": "bad"}}}`,
		},
		{
			name: "bad weekly_probe",
			json: `{"limits": {"fallback": {"weekly_probe": "123foobar"}}}`,
		},
		{
			name: "bad max_total",
			json: `{"limits": {"fallback": {"max_total": "inf"}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tc.json), "")
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestParseConfig_PathResolution(t *testing.T) {
	base := "/var/app/conf"
	json := `{
		"queue_file": "prompts.md",
		"work_dir": "../repo",
		"state_dir": "/var/lib/agyq"
	}`

	cfg, err := ParseConfig([]byte(json), base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantQueue := "/var/app/conf/prompts.md"
	if cfg.QueueFile != wantQueue {
		t.Errorf("QueueFile = %q, want %q", cfg.QueueFile, wantQueue)
	}

	wantWork := "/var/app/repo"
	if cfg.WorkDir != wantWork {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, wantWork)
	}

	wantState := "/var/lib/agyq"
	if cfg.StateDir != wantState {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, wantState)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agyq.json")

	content := `{
		"queue_file": "sub/queue.md",
		"work_dir": "target",
		"state_dir": "state",
		"prompt_timeout": "45m",
		"recovery": "restart-clean",
		"on_failure": "continue",
		"agy": {
			"binary": "/usr/local/bin/agy",
			"model": "gemini-3.8-flash-high",
			"effort": "high",
			"permission_mode": "dangerously-skip-permissions",
			"use_pty": true
		},
		"retry": {
			"max_attempts": 5,
			"backoff": ["2m", "10m"]
		}
	}`

	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfigFile failed: %v", err)
	}

	if cfg.QueueFile != filepath.Join(dir, "sub/queue.md") {
		t.Errorf("QueueFile = %q, want %q", cfg.QueueFile, filepath.Join(dir, "sub/queue.md"))
	}
	if cfg.WorkDir != filepath.Join(dir, "target") {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, filepath.Join(dir, "target"))
	}
	if cfg.StateDir != filepath.Join(dir, "state") {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, filepath.Join(dir, "state"))
	}
	if cfg.PromptTimeout != 45*time.Minute {
		t.Errorf("PromptTimeout = %v, want 45m", cfg.PromptTimeout)
	}
	if cfg.Recovery != application.RecoveryRestartClean {
		t.Errorf("Recovery = %v, want %v", cfg.Recovery, application.RecoveryRestartClean)
	}
	if cfg.OnFailure != application.FailureContinue {
		t.Errorf("OnFailure = %v, want %v", cfg.OnFailure, application.FailureContinue)
	}
	if cfg.Agy.Model != "gemini-3.8-flash-high" {
		t.Errorf("Agy.Model = %q, want %q", cfg.Agy.Model, "gemini-3.8-flash-high")
	}
	if cfg.Agy.Effort != "high" {
		t.Errorf("Agy.Effort = %q, want %q", cfg.Agy.Effort, "high")
	}
	if cfg.Agy.PermissionMode != "dangerously-skip-permissions" {
		t.Errorf("Agy.PermissionMode = %q, want %q", cfg.Agy.PermissionMode, "dangerously-skip-permissions")
	}
	if !cfg.Agy.UsePTY {
		t.Errorf("Agy.UsePTY = %v, want true", cfg.Agy.UsePTY)
	}
	if cfg.Retry.MaxAttempts != 5 {
		t.Errorf("Retry.MaxAttempts = %d, want 5", cfg.Retry.MaxAttempts)
	}
	if len(cfg.Retry.Backoff) != 2 || cfg.Retry.Backoff[0] != 2*time.Minute || cfg.Retry.Backoff[1] != 10*time.Minute {
		t.Errorf("Retry.Backoff = %v, want [2m, 10m]", cfg.Retry.Backoff)
	}
}

func TestLoadConfigFile_NonExistent(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/agyq.json")
	if err == nil {
		t.Fatal("expected error loading non-existent config, got nil")
	}
}

func TestFindGitRoot(t *testing.T) {
	tmp := t.TempDir()
	gitDir := filepath.Join(tmp, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(tmp, "sub1", "sub2")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	found := FindGitRoot(nested)
	if found != tmp {
		t.Errorf("FindGitRoot got %q, want %q", found, tmp)
	}

	// Worktree or submodule (.git as a file)
	tmpWorktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpWorktree, ".git"), []byte("gitdir: ..."), 0644); err != nil {
		t.Fatal(err)
	}
	foundWorktree := FindGitRoot(tmpWorktree)
	if foundWorktree != tmpWorktree {
		t.Errorf("FindGitRoot for worktree got %q, want %q", foundWorktree, tmpWorktree)
	}

	// Outside any git repo
	nonGit := t.TempDir()
	foundNon := FindGitRoot(nonGit)
	if foundNon != "" {
		t.Errorf("expected empty string for non-git dir, got %q", foundNon)
	}
}

func TestEnsureGitIgnore(t *testing.T) {
	tmp := t.TempDir()
	// Not a git repo -> no-op
	if err := EnsureGitIgnore(tmp, ".agyq/"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("expected no .gitignore in non-git directory")
	}

	// Git repo
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitIgnore(tmp, ".agyq/"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".agyq/") {
		t.Fatalf(".gitignore does not contain .agyq/: %q", string(data))
	}

	// Idempotency: call again
	if err := EnsureGitIgnore(tmp, ".agyq/"); err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data2), ".agyq")
	if count != 1 {
		t.Fatalf("expected 1 occurrence of .agyq in .gitignore, got %d:\n%s", count, string(data2))
	}
}

func TestLoadConfigOrDefault_ZeroConfig(t *testing.T) {
	origWd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(tmp, "queue.md")
	if err := os.WriteFile(queuePath, []byte("# #1 Test\nDo something\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg, err := LoadConfigOrDefault("")
	if err != nil {
		t.Fatalf("LoadConfigOrDefault failed: %v", err)
	}

	if cfg.WorkDir != tmp {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, tmp)
	}
	if cfg.QueueFile != queuePath {
		t.Errorf("QueueFile = %q, want %q", cfg.QueueFile, queuePath)
	}
	if cfg.StateDir != filepath.Join(tmp, ".agyq") {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, filepath.Join(tmp, ".agyq"))
	}
	if cfg.Agy.Binary == "" {
		t.Errorf("Agy.Binary should not be empty")
	}

	// Check that .gitignore was automatically updated
	giData, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if err != nil {
		t.Fatalf("failed to read auto-created .gitignore: %v", err)
	}
	if !strings.Contains(string(giData), ".agyq/") {
		t.Errorf(".gitignore does not contain .agyq/: %q", string(giData))
	}
}
