package application

import (
	"errors"
	"fmt"
	"time"

	"agyq/internal/domain"
)

// RecoveryPolicy defines how interrupted prompts are handled on restart (D-3).
type RecoveryPolicy string

const (
	RecoveryResume       RecoveryPolicy = "resume"        // default: resume in place with preamble
	RecoveryRestartClean RecoveryPolicy = "restart-clean" // reset git checkpoint before re-run
)

// FailurePolicy defines how the orchestrator behaves when a prompt fails.
type FailurePolicy string

const (
	FailureRetryAtEnd FailurePolicy = "retry-at-end" // default: defer failed prompt, finish rest of queue, retry at end once
	FailureContinue   FailurePolicy = "continue"     // record failure and proceed to next prompt
	FailureStop       FailurePolicy = "stop"         // halt queue execution
)

// Config is the validated, in-memory daemon configuration.
type Config struct {
	QueueFile     string
	WorkDir       string
	StateDir      string
	LogLevel      string
	Agy           AgyConfig
	Retry         RetryConfig
	Limits        LimitPatterns
	Recovery      RecoveryPolicy
	OnFailure     FailurePolicy
	PromptTimeout time.Duration
	MaxResumes    int
}

// AgyConfig holds settings for invoking the agy CLI binary.
type AgyConfig struct {
	Binary          string
	Model           string
	Effort          string
	ExtraArgs       []string
	PermissionMode  string
	SuccessStatuses []string
	UsePTY          bool
}

// RetryConfig configures retry counts and backoff intervals.
type RetryConfig struct {
	MaxAttempts int
	Backoff     []time.Duration
}

// LimitPatterns configures usage-limit classification and reset timing.
type LimitPatterns = domain.LimitPatterns

// BackoffConfig configures blind retry backoff when no reset time is parsed.
type BackoffConfig = domain.BackoffConfig

// DefaultConfig returns a Config populated with all project defaults.
func DefaultConfig() Config {
	return Config{
		LogLevel:      "info",
		Recovery:      RecoveryResume,
		OnFailure:     FailureRetryAtEnd,
		PromptTimeout: 90 * time.Minute,
		MaxResumes:    5,
		Agy: AgyConfig{
			Binary:          "agy",
			PermissionMode:  "dangerously-skip-permissions",
			SuccessStatuses: []string{"SUCCESS"},
			UsePTY:          false,
		},
		Retry: RetryConfig{
			MaxAttempts: 3,
			Backoff: []time.Duration{
				1 * time.Minute,
				5 * time.Minute,
				15 * time.Minute,
			},
		},
		Limits: LimitPatterns{
			Patterns: []string{
				"quota",
				"rate limit",
				"usage limit",
				"resource[_ ]exhausted",
				"429",
				"limit reached",
				"exceeded your",
				"try again (in|after|at)",
			},
			ExitCodes: []int{},
			StatusValues: []string{
				"RESOURCE_EXHAUSTED",
				"QUOTA_EXCEEDED",
				"RATE_LIMITED",
			},
			WeeklyHint: []string{
				"weekly",
				"per week",
				"7 day",
			},
			ResetRegexes: []string{
				`(?i)reset[s]? (?:at|on) (?P<abs>[^.\n]+)`,
				`(?i)(?:in|after) (?P<rel>(?:\d+\s*(?:days|day|d|hours|hour|hrs|hr|h|minutes|minute|mins|min|m)\s*(?:and\s*)?)+)`,
			},
			Timezone: "UTC",
			Fallback: BackoffConfig{
				SessionProbe: 30 * time.Minute,
				WeeklyProbe:  3 * time.Hour,
				Jitter:       0.1,
				MaxTotal:     192 * time.Hour, // 8 days
			},
		},
	}
}

// ValidateConfig validates that configuration values are sound and consistent (F-065).
func ValidateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if cfg.Agy.Binary == "" {
		return errors.New("agy.binary cannot be empty")
	}
	if cfg.Recovery != RecoveryResume && cfg.Recovery != RecoveryRestartClean {
		return fmt.Errorf("invalid recovery policy %q (must be %q or %q)", cfg.Recovery, RecoveryResume, RecoveryRestartClean)
	}
	if cfg.OnFailure != FailureRetryAtEnd && cfg.OnFailure != FailureContinue && cfg.OnFailure != FailureStop {
		return fmt.Errorf("invalid on_failure policy %q (must be %q, %q, or %q)", cfg.OnFailure, FailureRetryAtEnd, FailureContinue, FailureStop)
	}
	if cfg.PromptTimeout <= 0 {
		return errors.New("prompt_timeout must be positive")
	}
	if cfg.Limits.Timezone != "" {
		if _, err := time.LoadLocation(cfg.Limits.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", cfg.Limits.Timezone, err)
		}
	}
	if cfg.Retry.MaxAttempts <= 0 {
		return errors.New("retry.max_attempts must be positive")
	}
	return nil
}
