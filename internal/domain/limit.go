package domain

import (
	"regexp"
	"strings"
	"time"
)

// LimitPatterns configures usage-limit classification and reset timing.
type LimitPatterns struct {
	Patterns     []string
	ExitCodes    []int
	StatusValues []string
	WeeklyHint   []string
	ResetRegexes []string
	Timezone     string
	Fallback     BackoffConfig
}

// BackoffConfig configures blind retry backoff when no reset time is parsed.
type BackoffConfig struct {
	SessionProbe time.Duration
	WeeklyProbe  time.Duration
	Jitter       float64
	MaxTotal     time.Duration
}

// RawRunResult holds the unclassified observations from an agy execution.
type RawRunResult struct {
	ExitCode       int
	TimedOut       bool
	Killed         bool
	Stdout         []byte // bounded
	Stderr         []byte // bounded
	Envelope       *Envelope
	TranscriptPath string
}

// Envelope is the JSON structure output by agy in print mode.
type Envelope struct {
	ConversationID string         `json:"conversation_id"`
	Status         string         `json:"status"`
	Response       string         `json:"response"`
	Error          string         `json:"error,omitempty"`
	Usage          map[string]any `json:"usage,omitempty"`
	DeniedActions  []any          `json:"denied_actions,omitempty"`
}

// HasPermissionDenial checks whether the run result indicates tool permission denials
// that were auto-denied by headless execution (e.g. missing permission_mode).
func HasPermissionDenial(r RawRunResult) bool {
	if r.Envelope != nil {
		if len(r.Envelope.DeniedActions) > 0 {
			return true
		}
		if containsPermissionDenial(r.Envelope.Response) || containsPermissionDenial(r.Envelope.Error) {
			return true
		}
	}
	if containsPermissionDenial(string(r.Stderr)) || containsPermissionDenial(string(r.Stdout)) {
		return true
	}
	return false
}

func containsPermissionDenial(text string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, "permission that headless mode cannot prompt for") {
		return true
	}
	if strings.Contains(text, "jetski:") && strings.Contains(text, "auto-denied") {
		return true
	}
	if strings.Contains(text, "re-run with --dangerously-skip-permissions to auto-approve all tools") {
		return true
	}
	if strings.Contains(text, "Add an allow-rule under permissions.allow in settings.json") {
		return true
	}
	if strings.Contains(text, "tool required the") && strings.Contains(text, "permission") && strings.Contains(text, "auto-denied") {
		return true
	}
	return false
}

// ClassifyOutcome determines the domain outcome and optional LimitSignal from a RawRunResult.
// It enforces the precedence rules from docs/05 §4 and §5.
func ClassifyOutcome(r RawRunResult, p LimitPatterns) (Outcome, *LimitSignal) {
	// Rule 1: context cancelled by us (shutdown)
	if r.Killed {
		return OutcomeInterrupted, nil
	}

	// Rule 2: TimedOut (our hard timeout)
	if r.TimedOut {
		return OutcomeTransient, nil
	}

	// Rule 2.5: Tool permissions auto-denied by headless mode (fatal configuration/env error)
	if HasPermissionDenial(r) {
		return OutcomeFatal, nil
	}

	// Rule 3: matches a limit pattern in status (always checked)
	status := ""
	if r.Envelope != nil {
		status = r.Envelope.Status
	}
	for _, sv := range p.StatusValues {
		if sv != "" && strings.EqualFold(status, sv) {
			kind := detectLimitKind(r, p)
			raw := "status: " + status
			if r.Envelope != nil && r.Envelope.Error != "" {
				raw = r.Envelope.Error
			} else if r.Envelope != nil && r.Envelope.Response != "" {
				raw = r.Envelope.Response
			} else if len(r.Stderr) > 0 {
				raw = string(r.Stderr)
			}
			return OutcomeLimit, &LimitSignal{
				Kind: kind,
				Raw:  raw,
			}
		}
	}

	// Safety valve (docs/05 §5): clean exit 0 + SUCCESS is protected from broad regexes
	isCleanSuccess := r.ExitCode == 0 && r.Envelope != nil && strings.EqualFold(r.Envelope.Status, "SUCCESS")
	if isCleanSuccess {
		return OutcomeSuccess, nil
	}

	// Non-clean run: check exit code mapping for limit
	for _, ec := range p.ExitCodes {
		if r.ExitCode == ec {
			kind := detectLimitKind(r, p)
			raw := string(r.Stderr)
			if raw == "" && r.Envelope != nil {
				if r.Envelope.Error != "" {
					raw = r.Envelope.Error
				} else {
					raw = r.Envelope.Response
				}
			}
			return OutcomeLimit, &LimitSignal{
				Kind: kind,
				Raw:  raw,
			}
		}
	}

	// Check response (bounded to first 4 KiB per docs/05 §4), error, and stderr for limit patterns
	respHead := ""
	envError := ""
	if r.Envelope != nil {
		resp := r.Envelope.Response
		if len(resp) > 4096 {
			respHead = resp[:4096]
		} else {
			respHead = resp
		}
		if len(r.Envelope.Error) > 4096 {
			envError = r.Envelope.Error[:4096]
		} else {
			envError = r.Envelope.Error
		}
	}
	stderrStr := string(r.Stderr)

	for _, pat := range p.Patterns {
		if pat == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			// Fallback to substring match
			patLower := strings.ToLower(pat)
			if strings.Contains(strings.ToLower(stderrStr), patLower) ||
				strings.Contains(strings.ToLower(respHead), patLower) ||
				strings.Contains(strings.ToLower(envError), patLower) ||
				strings.Contains(strings.ToLower(status), patLower) {
				kind := detectLimitKind(r, p)
				raw := stderrStr
				if raw == "" {
					if envError != "" {
						raw = envError
					} else {
						raw = respHead
					}
				}
				return OutcomeLimit, &LimitSignal{
					Kind: kind,
					Raw:  raw,
				}
			}
			continue
		}

		if re.MatchString(stderrStr) || re.MatchString(respHead) || re.MatchString(envError) || re.MatchString(status) {
			kind := detectLimitKind(r, p)
			raw := stderrStr
			if raw == "" {
				if envError != "" {
					raw = envError
				} else {
					raw = respHead
				}
			}
			return OutcomeLimit, &LimitSignal{
				Kind: kind,
				Raw:  raw,
			}
		}
	}

	// Rule 4: launch failure or auth pattern in stderr/error
	if r.ExitCode == 127 {
		return OutcomeFatal, nil
	}
	authPatterns := []string{"not signed in", "authentication", "login required", "unauthenticated"}
	for _, ap := range authPatterns {
		if strings.Contains(strings.ToLower(stderrStr), ap) || strings.Contains(strings.ToLower(envError), ap) {
			return OutcomeFatal, nil
		}
	}

	// Rule 5: exit 0 and envelope non-nil and envelope.status == "SUCCESS" (already handled by safety valve)

	// Rule 6: exit 0 but envelope nil or status not in success set
	if r.ExitCode == 0 {
		return OutcomeTransient, nil
	}

	// Rule 7: exit != 0 matching transient pattern in stderr, error, stdout, or response
	if MatchesTransientPattern(stderrStr) || MatchesTransientPattern(envError) || MatchesTransientPattern(respHead) || MatchesTransientPattern(string(r.Stdout)) {
		return OutcomeTransient, nil
	}

	// Rule 8: unknown text != limit -> returns OutcomeTransient (first attempt)
	return OutcomeTransient, nil
}

// TransientPatterns are the standard network/service transient substrings from docs/05 §4 Rule 7.
var TransientPatterns = []string{
	"timeout", "econnreset", "502", "503", "unavailable", "deadline exceeded", "network", "try again",
	"connection reset", "connection refused", "broken pipe", "eof",
	"500", "504", "bad gateway", "gateway timeout", "internal server error", "internal error",
	"service temporarily unavailable", "server overloaded", "overloaded",
	"socket hang up", "fetch failed", "tls handshake", "temporary failure",
	"try later", "interrupted", "no route to host", "dns resolution failed",
	"etimedout", "ehostunreach", "enetunreach", "rate limit", "resource exhausted",
	"quota exceeded", "too many requests",
}

// MatchesTransientPattern reports whether text matches any transient pattern.
func MatchesTransientPattern(text string) bool {
	lower := strings.ToLower(text)
	for _, tp := range TransientPatterns {
		if strings.Contains(lower, tp) {
			return true
		}
	}
	return false
}

func detectLimitKind(r RawRunResult, p LimitPatterns) LimitKind {
	texts := []string{string(r.Stderr)}
	if r.Envelope != nil {
		texts = append(texts, r.Envelope.Response, r.Envelope.Status, r.Envelope.Error)
	}
	for _, h := range p.WeeklyHint {
		if h == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + h)
		for _, text := range texts {
			if err == nil && re.MatchString(text) {
				return LimitWeekly
			} else if strings.Contains(strings.ToLower(text), strings.ToLower(h)) {
				return LimitWeekly
			}
		}
	}
	return LimitSession
}
