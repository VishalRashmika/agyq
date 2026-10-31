package application

import (
	"context"
)

// CheckStatus indicates the outcome of an individual preflight check.
type CheckStatus string

const (
	CheckPass CheckStatus = "PASS"
	CheckFail CheckStatus = "FAIL"
	CheckWarn CheckStatus = "WARN"
)

// CheckResult represents the outcome and diagnostic details of one check.
type CheckResult struct {
	Name   string
	Status CheckStatus
	Detail string
}

// PreflightReport aggregates all preflight check outcomes.
type PreflightReport struct {
	Checks []CheckResult
}

// AllPassed returns true if none of the checks resulted in a FAIL status.
func (r PreflightReport) AllPassed() bool {
	for _, c := range r.Checks {
		if c.Status == CheckFail {
			return false
		}
	}
	return true
}

// PreflightChecker runs environment and dependency verification checks (F-025).
type PreflightChecker interface {
	Check(ctx context.Context, cfg *Config) (PreflightReport, error)
}
