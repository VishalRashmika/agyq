package presentation

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"agyq/internal/application"
	"agyq/internal/domain"
)

type mockOrchestratorRunner struct {
	runErr error
}

func (m *mockOrchestratorRunner) Run(ctx context.Context) error {
	return m.runErr
}

func TestRunCommand_ExitCodes(t *testing.T) {
	goodChecker := &mockChecker{
		report: application.PreflightReport{
			Checks: []application.CheckResult{
				{Name: "binary", Status: application.CheckPass},
			},
		},
	}
	failingChecker := &mockChecker{
		report: application.PreflightReport{
			Checks: []application.CheckResult{
				{Name: "binary", Status: application.CheckFail, Detail: "not found"},
			},
		},
	}

	baseLoader := func(path string) (*application.Config, error) {
		cfg := application.DefaultConfig()
		cfg.QueueFile = "queue.md"
		return &cfg, nil
	}

	tests := []struct {
		name       string
		args       []string
		checker    application.PreflightChecker
		loader     ConfigLoaderFunc
		orchErr    error
		factoryErr error
		wantCode   int
		wantStderr string
	}{
		{
			name:     "exit 0: queue complete / clean stop",
			args:     []string{"run"},
			checker:  goodChecker,
			loader:   baseLoader,
			orchErr:  nil,
			wantCode: ExitOK,
		},
		{
			name:       "exit 1: usage error (unknown flag)",
			args:       []string{"run", "--unknown-flag"},
			checker:    goodChecker,
			loader:     baseLoader,
			wantCode:   ExitUsage,
			wantStderr: "unknown flag",
		},
		{
			name:     "exit 1: usage error (bad queue validation)",
			args:     []string{"run"},
			checker:  goodChecker,
			loader:   baseLoader,
			orchErr:  &domain.QueueValidationError{Errors: []string{"bad prompt"}},
			wantCode: ExitUsage,
		},
		{
			name:       "exit 2: stopped on failure (ErrQueueHalted)",
			args:       []string{"run"},
			checker:    goodChecker,
			loader:     baseLoader,
			orchErr:    application.ErrQueueHalted,
			wantCode:   ExitStopped,
			wantStderr: "stopped on failure",
		},
		{
			name:       "exit 3: already running (factory returns ErrLocked)",
			args:       []string{"run"},
			checker:    goodChecker,
			loader:     baseLoader,
			factoryErr: application.ErrLocked,
			wantCode:   ExitLocked,
			wantStderr: "lock already held",
		},
		{
			name:       "exit 3: already running (orch.Run returns ErrLocked)",
			args:       []string{"run"},
			checker:    goodChecker,
			loader:     baseLoader,
			orchErr:    application.ErrLocked,
			wantCode:   ExitLocked,
			wantStderr: "lock already held",
		},
		{
			name:       "exit 4: preflight failed",
			args:       []string{"run"},
			checker:    failingChecker,
			loader:     baseLoader,
			wantCode:   ExitPreflight,
			wantStderr: "preflight checks failed",
		},
		{
			name:     "flag: --continue-on-failure overrides policy",
			args:     []string{"run", "--continue-on-failure"},
			checker:  goodChecker,
			loader:   baseLoader,
			wantCode: ExitOK,
		},
		{
			name:     "flag: --on-failure continue",
			args:     []string{"run", "--on-failure", "continue"},
			checker:  goodChecker,
			loader:   baseLoader,
			wantCode: ExitOK,
		},
		{
			name:       "exit 1: invalid --on-failure value",
			args:       []string{"run", "--on-failure", "invalid-policy"},
			checker:    goodChecker,
			loader:     baseLoader,
			wantCode:   ExitUsage,
			wantStderr: "invalid --on-failure policy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp(tc.checker, tc.loader)
			app.WithOrchestratorFactory(func(cfg application.Config) (OrchestratorRunner, error) {
				if tc.factoryErr != nil {
					return nil, tc.factoryErr
				}
				return &mockOrchestratorRunner{runErr: tc.orchErr}, nil
			})

			var stdout, stderr bytes.Buffer
			code := app.Run(tc.args, &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit code got %d, want %d (stderr: %s)", code, tc.wantCode, stderr.String())
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr %q does not contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}
