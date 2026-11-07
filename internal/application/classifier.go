package application

import (
	"strings"

	"agyq/internal/domain"
)

// Classifier evaluates raw run results into domain outcomes per docs/05 §4 and D-2.
type Classifier struct {
	successStatuses []string
	limits          domain.LimitPatterns
}

// NewClassifier creates a Classifier configured from AgyConfig and LimitPatterns.
func NewClassifier(cfg Config) *Classifier {
	successSet := cfg.Agy.SuccessStatuses
	if len(successSet) == 0 {
		successSet = []string{"SUCCESS"}
	}
	return &Classifier{
		successStatuses: successSet,
		limits:          cfg.Limits,
	}
}

// ClassifyWithSignal determines the domain outcome, human reason, and optional LimitSignal from a RawRunResult.
func (c *Classifier) ClassifyWithSignal(res RawRunResult, consecutiveFailures int) (domain.Outcome, string, *domain.LimitSignal) {
	outcome, sig := domain.ClassifyOutcome(res, c.limits)

	reason := ""
	switch outcome {
	case domain.OutcomeInterrupted:
		reason = "interrupted by context cancellation"

	case domain.OutcomeTransient:
		if res.TimedOut {
			reason = "hard timeout exceeded"
		} else if res.ExitCode == 0 {
			if consecutiveFailures > 0 {
				outcome = domain.OutcomeFatal
				reason = "repeated invalid envelope or non-success status"
			} else {
				reason = "invalid or missing envelope on exit 0"
			}
		} else if isTransientMatched(res) {
			reason = "transient error matched"
			if detail := extractErrorDetail(res); detail != "" {
				reason += ": " + detail
			}
		} else {
			if consecutiveFailures > 0 {
				outcome = domain.OutcomeFatal
				reason = "repeated execution failure"
			} else {
				reason = "transient execution failure"
			}
			if detail := extractErrorDetail(res); detail != "" {
				reason += ": " + detail
			}
		}

	case domain.OutcomeLimit:
		reason = "usage limit hit"
		if sig != nil && sig.Raw != "" {
			reason = "usage limit hit: " + sig.Raw
		}

	case domain.OutcomeFatal:
		if domain.HasPermissionDenial(res) {
			reason = "tool execution was auto-denied due to missing permissions (check permission_mode or settings.json)"
		} else {
			reason = "fatal execution failure"
			if detail := extractErrorDetail(res); detail != "" {
				reason += ": " + detail
			}
		}

	case domain.OutcomeSuccess:
		reason = "completed successfully"
		if res.Envelope != nil {
			reason += " with status " + res.Envelope.Status
		}
	}

	return outcome, reason, sig
}

// Classify determines the domain outcome and human reason from a RawRunResult.
func (c *Classifier) Classify(res RawRunResult, consecutiveFailures int) (domain.Outcome, string) {
	outcome, reason, _ := c.ClassifyWithSignal(res, consecutiveFailures)
	return outcome, reason
}

func isTransientMatched(res RawRunResult) bool {
	if domain.MatchesTransientPattern(string(res.Stderr)) || domain.MatchesTransientPattern(string(res.Stdout)) {
		return true
	}
	if res.Envelope != nil {
		if domain.MatchesTransientPattern(res.Envelope.Error) || domain.MatchesTransientPattern(res.Envelope.Response) {
			return true
		}
	}
	return false
}

func extractErrorDetail(res RawRunResult) string {
	if res.Envelope != nil && res.Envelope.Error != "" {
		return cleanSingleLine(res.Envelope.Error, 200)
	}
	if len(res.Stderr) > 0 {
		return cleanSingleLine(string(res.Stderr), 200)
	}
	if len(res.Stdout) > 0 && res.ExitCode != 0 {
		return cleanSingleLine(string(res.Stdout), 200)
	}
	return ""
}

func cleanSingleLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	first := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			first = l
			break
		}
	}
	if len(first) > maxLen {
		return first[:maxLen] + "..."
	}
	return first
}
