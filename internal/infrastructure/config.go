package infrastructure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agyq/internal/application"
)

type jsonConfig struct {
	QueueFile     *string           `json:"queue_file"`
	WorkDir       *string           `json:"work_dir"`
	StateDir      *string           `json:"state_dir"`
	PromptTimeout *string           `json:"prompt_timeout"`
	Recovery      *string           `json:"recovery"`
	OnFailure     *string           `json:"on_failure"`
	LogLevel      *string           `json:"log_level"`
	MaxResumes    *int              `json:"max_resumes"`
	Agy           *jsonAgyConfig    `json:"agy"`
	Retry         *jsonRetryConfig  `json:"retry"`
	Limits        *jsonLimitsConfig `json:"limits"`
}

type jsonAgyConfig struct {
	Binary          *string   `json:"binary"`
	Model           *string   `json:"model"`
	Effort          *string   `json:"effort"`
	ExtraArgs       *[]string `json:"extra_args"`
	PermissionMode  *string   `json:"permission_mode"`
	SuccessStatuses *[]string `json:"success_statuses"`
	UsePTY          *bool     `json:"use_pty"`
}

type jsonRetryConfig struct {
	MaxAttempts *int      `json:"max_attempts"`
	MaxResumes  *int      `json:"max_resumes"`
	Backoff     *[]string `json:"backoff"`
}

type jsonLimitsConfig struct {
	Patterns     *[]string           `json:"patterns"`
	ExitCodes    *[]int              `json:"exit_codes"`
	StatusValues *[]string           `json:"status_values"`
	WeeklyHint   *[]string           `json:"weekly_hint"`
	ResetRegexes *[]string           `json:"reset_regexes"`
	Timezone     *string             `json:"timezone"`
	Fallback     *jsonFallbackConfig `json:"fallback"`
}

type jsonFallbackConfig struct {
	SessionProbe *string  `json:"session_probe"`
	WeeklyProbe  *string  `json:"weekly_probe"`
	Jitter       *float64 `json:"jitter"`
	MaxTotal     *string  `json:"max_total"`
}

// FindGitRoot traverses upwards from startDir searching for a .git directory or file.
// Returns the absolute path of the repository root, or empty string if not found.
func FindGitRoot(startDir string) string {
	if startDir == "" {
		startDir, _ = os.Getwd()
	}
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// FindAgyBinary locates the agy executable in PATH or standard user/system directories.
// Returns the resolved path or "agy" fallback.
func FindAgyBinary() string {
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "agy"),
			filepath.Join(home, ".gemini", "antigravity-cli", "bin", "agy"),
		)
	}
	candidates = append(candidates,
		"/usr/local/bin/agy",
		"/usr/bin/agy",
	)
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			if fi.Mode()&0111 != 0 {
				return c
			}
		}
	}
	return "agy"
}

// StandardAgyqGitIgnoreEntries lists the runtime, cache, queue, and log patterns
// that should never be tracked in git repositories running agyq.
var StandardAgyqGitIgnoreEntries = []string{
	".agyq/",
	"queue.md",
	"QUEUE.md",
	"queue.*.md",
	"*.queue.md",
	".queue.md",
	"agyq.json",
	"agyq.local.json",
	"*.agyq.json",
	"errors.log",
	"*.log",
	".cache/",
	"*.cache",
}

// EnsureGitIgnore ensures that the given entry exists in <repoRoot>/.gitignore.
// Does nothing if <repoRoot> is not a git repository.
func EnsureGitIgnore(repoRoot, entry string) error {
	gitDir := filepath.Join(repoRoot, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return nil
	}
	giPath := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	cleanEntry := strings.Trim(entry, "/")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmedClean := strings.Trim(trimmed, "/")
		if trimmedClean == cleanEntry {
			return nil
		}
	}

	var toAppend string
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		toAppend += "\n"
	}
	toAppend += entry + "\n"
	f, err := os.OpenFile(giPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(toAppend)
	return err
}

// EnsureAgyqGitIgnore ensures that all standard agyq files and directories are ignored in <repoRoot>/.gitignore.
func EnsureAgyqGitIgnore(repoRoot string) error {
	gitDir := filepath.Join(repoRoot, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return nil
	}
	giPath := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	existing := make(map[string]bool)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			existing[trimmed] = true
			existing[strings.Trim(trimmed, "/")] = true
		}
	}

	var missing []string
	for _, entry := range StandardAgyqGitIgnoreEntries {
		clean := strings.Trim(entry, "/")
		if !existing[entry] && !existing[clean] {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	var toAppend string
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		toAppend += "\n"
	}
	toAppend += "# agyq runtime state, queues, cache, and logs\n"
	for _, m := range missing {
		toAppend += m + "\n"
	}

	f, err := os.OpenFile(giPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(toAppend)
	return err
}

// EnrichConfigDefaults populates zero-config defaults (WorkDir, QueueFile, StateDir, Binary)
// and ensures .agyq/ is in .gitignore if operating inside a git repository.
func EnrichConfigDefaults(cfg *application.Config, baseDir string) {
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	gitRoot := FindGitRoot(baseDir)

	if cfg.WorkDir == "" {
		if gitRoot != "" {
			cfg.WorkDir = gitRoot
		} else if baseDir != "" {
			cfg.WorkDir = baseDir
		} else {
			cwd, _ := os.Getwd()
			cfg.WorkDir = cwd
		}
	}
	if absWork, err := filepath.Abs(cfg.WorkDir); err == nil {
		cfg.WorkDir = absWork
	}

	if cfg.QueueFile == "" {
		candQueue := filepath.Join(cfg.WorkDir, "queue.md")
		candUpper := filepath.Join(cfg.WorkDir, "QUEUE.md")
		if _, err := os.Stat(candUpper); err == nil {
			cfg.QueueFile = candUpper
		} else {
			cfg.QueueFile = candQueue
		}
	} else if !filepath.IsAbs(cfg.QueueFile) {
		cfg.QueueFile = filepath.Join(cfg.WorkDir, cfg.QueueFile)
	}

	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(cfg.WorkDir, ".agyq")
	} else if !filepath.IsAbs(cfg.StateDir) {
		cfg.StateDir = filepath.Join(cfg.WorkDir, cfg.StateDir)
	}

	if cfg.Agy.Binary == "" || cfg.Agy.Binary == "agy" {
		detected := FindAgyBinary()
		if detected != "" {
			cfg.Agy.Binary = detected
		}
	}

	if gitRoot != "" {
		rel, err := filepath.Rel(gitRoot, cfg.StateDir)
		if err == nil && !strings.HasPrefix(rel, "..") {
			_ = EnsureAgyqGitIgnore(gitRoot)
		}
	}
}

// LoadConfigFile reads and parses a JSON config file from disk.
// Relative paths within the config are resolved against the directory containing the file.
func LoadConfigFile(path string) (*application.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	baseDir := filepath.Dir(absPath)
	cfg, err := ParseConfig(data, baseDir)
	if err != nil {
		return nil, err
	}
	EnrichConfigDefaults(cfg, baseDir)
	return cfg, nil
}

// LoadConfigOrDefault loads the configuration from path if specified,
// or auto-discovers configuration from standard repository and system locations.
// If no configuration file is found, it provides zero-config defaults.
func LoadConfigOrDefault(path string) (*application.Config, error) {
	if path != "" {
		return LoadConfigFile(path)
	}

	candidates := []string{
		"agyq.json",
		".agyq.json",
	}
	if gitRoot := FindGitRoot("."); gitRoot != "" {
		candidates = append(candidates,
			filepath.Join(gitRoot, "agyq.json"),
			filepath.Join(gitRoot, ".agyq.json"),
		)
	}
	candidates = append(candidates, "/etc/agyq/agyq.json")

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return LoadConfigFile(cand)
		}
	}

	def := application.DefaultConfig()
	EnrichConfigDefaults(&def, "")
	return &def, nil
}

// ParseConfig decodes raw JSON bytes into application.Config.
// Unknown keys are rejected. Unspecified fields take defaults from application.DefaultConfig().
// Relative paths in queue_file, work_dir, state_dir are resolved against baseDir.
func ParseConfig(data []byte, baseDir string) (*application.Config, error) {
	cfg := application.DefaultConfig()

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var raw jsonConfig
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid json config: %w", err)
	}

	if raw.QueueFile != nil {
		cfg.QueueFile = resolvePath(baseDir, *raw.QueueFile)
	}
	if raw.WorkDir != nil {
		cfg.WorkDir = resolvePath(baseDir, *raw.WorkDir)
	}
	if raw.StateDir != nil {
		cfg.StateDir = resolvePath(baseDir, *raw.StateDir)
	}

	if raw.PromptTimeout != nil {
		d, err := time.ParseDuration(*raw.PromptTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid prompt_timeout %q: %w", *raw.PromptTimeout, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("prompt_timeout must be positive: %v", d)
		}
		cfg.PromptTimeout = d
	}

	if raw.Recovery != nil {
		policy := application.RecoveryPolicy(*raw.Recovery)
		switch policy {
		case application.RecoveryResume, application.RecoveryRestartClean:
			cfg.Recovery = policy
		default:
			return nil, fmt.Errorf("invalid recovery policy %q (must be %q or %q)",
				*raw.Recovery, application.RecoveryResume, application.RecoveryRestartClean)
		}
	}

	if raw.OnFailure != nil {
		policy := application.FailurePolicy(*raw.OnFailure)
		switch policy {
		case application.FailureStop, application.FailureContinue:
			cfg.OnFailure = policy
		default:
			return nil, fmt.Errorf("invalid on_failure policy %q (must be %q or %q)",
				*raw.OnFailure, application.FailureStop, application.FailureContinue)
		}
	}

	if raw.LogLevel != nil {
		cfg.LogLevel = *raw.LogLevel
	}

	if raw.Agy != nil {
		if raw.Agy.Binary != nil {
			cfg.Agy.Binary = *raw.Agy.Binary
		}
		if raw.Agy.Model != nil {
			cfg.Agy.Model = *raw.Agy.Model
		}
		if raw.Agy.Effort != nil {
			cfg.Agy.Effort = *raw.Agy.Effort
		}
		if raw.Agy.ExtraArgs != nil {
			cfg.Agy.ExtraArgs = *raw.Agy.ExtraArgs
		}
		if raw.Agy.PermissionMode != nil {
			mode := *raw.Agy.PermissionMode
			if mode != "settings-allowlist" && mode != "dangerously-skip-permissions" {
				return nil, fmt.Errorf("invalid agy.permission_mode %q (must be 'settings-allowlist' or 'dangerously-skip-permissions')", mode)
			}
			cfg.Agy.PermissionMode = mode
		}
		if raw.Agy.SuccessStatuses != nil {
			cfg.Agy.SuccessStatuses = *raw.Agy.SuccessStatuses
		}
		if raw.Agy.UsePTY != nil {
			cfg.Agy.UsePTY = *raw.Agy.UsePTY
		}
	}

	if raw.Retry != nil {
		if raw.Retry.MaxAttempts != nil {
			if *raw.Retry.MaxAttempts < 1 {
				return nil, fmt.Errorf("retry.max_attempts must be >= 1, got %d", *raw.Retry.MaxAttempts)
			}
			cfg.Retry.MaxAttempts = *raw.Retry.MaxAttempts
		}
		if raw.Retry.MaxResumes != nil {
			if *raw.Retry.MaxResumes < 1 {
				return nil, fmt.Errorf("retry.max_resumes must be >= 1, got %d", *raw.Retry.MaxResumes)
			}
			cfg.MaxResumes = *raw.Retry.MaxResumes
		}
		if raw.Retry.Backoff != nil {
			backoffs := make([]time.Duration, len(*raw.Retry.Backoff))
			for i, bStr := range *raw.Retry.Backoff {
				d, err := time.ParseDuration(bStr)
				if err != nil {
					return nil, fmt.Errorf("invalid retry.backoff[%d] %q: %w", i, bStr, err)
				}
				if d < 0 {
					return nil, fmt.Errorf("retry.backoff[%d] must be non-negative: %v", i, d)
				}
				backoffs[i] = d
			}
			cfg.Retry.Backoff = backoffs
		}
	}

	if raw.MaxResumes != nil {
		if *raw.MaxResumes < 1 {
			return nil, fmt.Errorf("max_resumes must be >= 1, got %d", *raw.MaxResumes)
		}
		cfg.MaxResumes = *raw.MaxResumes
	}

	if raw.Limits != nil {
		if raw.Limits.Patterns != nil {
			cfg.Limits.Patterns = *raw.Limits.Patterns
		}
		if raw.Limits.ExitCodes != nil {
			cfg.Limits.ExitCodes = *raw.Limits.ExitCodes
		}
		if raw.Limits.StatusValues != nil {
			cfg.Limits.StatusValues = *raw.Limits.StatusValues
		}
		if raw.Limits.WeeklyHint != nil {
			cfg.Limits.WeeklyHint = *raw.Limits.WeeklyHint
		}
		if raw.Limits.ResetRegexes != nil {
			cfg.Limits.ResetRegexes = *raw.Limits.ResetRegexes
		}
		if raw.Limits.Timezone != nil {
			tz := *raw.Limits.Timezone
			if _, err := time.LoadLocation(tz); err != nil {
				return nil, fmt.Errorf("invalid limits.timezone %q: %w", tz, err)
			}
			cfg.Limits.Timezone = tz
		}
		if raw.Limits.Fallback != nil {
			fb := raw.Limits.Fallback
			if fb.SessionProbe != nil {
				d, err := time.ParseDuration(*fb.SessionProbe)
				if err != nil {
					return nil, fmt.Errorf("invalid limits.fallback.session_probe %q: %w", *fb.SessionProbe, err)
				}
				cfg.Limits.Fallback.SessionProbe = d
			}
			if fb.WeeklyProbe != nil {
				d, err := time.ParseDuration(*fb.WeeklyProbe)
				if err != nil {
					return nil, fmt.Errorf("invalid limits.fallback.weekly_probe %q: %w", *fb.WeeklyProbe, err)
				}
				cfg.Limits.Fallback.WeeklyProbe = d
			}
			if fb.Jitter != nil {
				j := *fb.Jitter
				if j < 0 || j > 1.0 {
					return nil, fmt.Errorf("limits.fallback.jitter must be between 0.0 and 1.0, got %v", j)
				}
				cfg.Limits.Fallback.Jitter = j
			}
			if fb.MaxTotal != nil {
				d, err := time.ParseDuration(*fb.MaxTotal)
				if err != nil {
					return nil, fmt.Errorf("invalid limits.fallback.max_total %q: %w", *fb.MaxTotal, err)
				}
				cfg.Limits.Fallback.MaxTotal = d
			}
		}
	}

	return &cfg, nil
}

func resolvePath(baseDir, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if baseDir == "" {
		abs, err := filepath.Abs(p)
		if err == nil {
			return abs
		}
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}
