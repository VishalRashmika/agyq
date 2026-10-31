package presentation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agyq/internal/application"
	"agyq/internal/domain"
)

func TestRunControl_PauseAndResume(t *testing.T) {
	var submitted []application.ControlCommand
	submitter := func(stateDir string, cmd application.ControlCommand) error {
		submitted = append(submitted, cmd)
		return nil
	}

	app := NewApp(nil, func(path string) (*application.Config, error) {
		def := application.DefaultConfig()
		def.StateDir = "/custom/statedir"
		return &def, nil
	}).WithControlSubmitter(submitter)

	// Test pause
	var out, errb bytes.Buffer
	code := app.Run([]string{"pause"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("pause exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "submitted pause command") {
		t.Fatalf("pause output mismatch: %s", out.String())
	}
	if len(submitted) != 1 || submitted[0].Kind != application.CommandPause {
		t.Fatalf("expected 1 pause command, got %+v", submitted)
	}

	// Test resume
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"resume"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("resume exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "submitted resume command") {
		t.Fatalf("resume output mismatch: %s", out.String())
	}
	if len(submitted) != 2 || submitted[1].Kind != application.CommandResume {
		t.Fatalf("expected resume command, got %+v", submitted)
	}
}

func TestRunControl_SkipAndRetry(t *testing.T) {
	var submitted []application.ControlCommand
	submitter := func(stateDir string, cmd application.ControlCommand) error {
		submitted = append(submitted, cmd)
		return nil
	}

	app := NewApp(nil, func(path string) (*application.Config, error) {
		def := application.DefaultConfig()
		return &def, nil
	}).WithControlSubmitter(submitter)

	// Test skip 3
	var out, errb bytes.Buffer
	code := app.Run([]string{"skip", "3"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("skip exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "submitted skip command for prompt #3") {
		t.Fatalf("skip output mismatch: %s", out.String())
	}
	if len(submitted) != 1 || submitted[0].Kind != application.CommandSkip || submitted[0].Prompt != 3 {
		t.Fatalf("expected skip 3, got %+v", submitted)
	}

	// Test retry 2
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"retry", "2"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("retry exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "submitted retry command for prompt #2") {
		t.Fatalf("retry output mismatch: %s", out.String())
	}
	if len(submitted) != 2 || submitted[1].Kind != application.CommandRetry || submitted[1].Prompt != 2 {
		t.Fatalf("expected retry 2, got %+v", submitted)
	}
}

func TestRunControl_RefusesOnRunningPrompt(t *testing.T) {
	var submitted []application.ControlCommand
	submitter := func(stateDir string, cmd application.ControlCommand) error {
		submitted = append(submitted, cmd)
		return nil
	}

	mockStore := &mockStateStore{
		state: &domain.RunState{
			Records: []domain.PromptRecord{
				{Number: 1, Status: domain.StatusRunning},
				{Number: 2, Status: domain.StatusFailed},
			},
		},
	}

	app := NewApp(nil, func(path string) (*application.Config, error) {
		def := application.DefaultConfig()
		return &def, nil
	}).WithControlSubmitter(submitter).
		WithStateStoreFactory(func(stateDir string) (application.StateStore, error) {
			return mockStore, nil
		})

	// Skip prompt 1 (running) -> refuses
	var out, errb bytes.Buffer
	code := app.Run([]string{"skip", "1"}, &out, &errb)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage on skip running prompt, got %d", code)
	}
	if !strings.Contains(errb.String(), "cannot skip running prompt #1 (refusing)") {
		t.Fatalf("stderr mismatch: %s", errb.String())
	}
	if len(submitted) != 0 {
		t.Fatalf("expected no command submitted when refused, got %+v", submitted)
	}

	// Retry prompt 1 (running) -> refuses
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"retry", "1"}, &out, &errb)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage on retry running prompt, got %d", code)
	}
	if !strings.Contains(errb.String(), "cannot retry running prompt #1 (refusing)") {
		t.Fatalf("stderr mismatch: %s", errb.String())
	}
	if len(submitted) != 0 {
		t.Fatalf("expected no command submitted when refused, got %+v", submitted)
	}
}

func TestRunControl_Errors(t *testing.T) {
	app := NewApp(nil, nil)

	// Skip without prompt number
	var out, errb bytes.Buffer
	if code := app.Run([]string{"skip"}, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage", code)
	}

	// Retry without prompt number
	errb.Reset()
	if code := app.Run([]string{"retry"}, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage", code)
	}

	// Skip with negative prompt number
	errb.Reset()
	if code := app.Run([]string{"skip", "-1"}, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage", code)
	}

	// Skip with non-numeric prompt
	errb.Reset()
	if code := app.Run([]string{"skip", "abc"}, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want ExitUsage", code)
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out, errb bytes.Buffer
		if code := Run([]string{arg}, &out, &errb); code != ExitOK {
			t.Fatalf("arg %q: exit = %d", arg, code)
		}
		got := out.String()
		if !strings.HasPrefix(got, "agyq ") {
			t.Fatalf("arg %q: unexpected output %q", arg, got)
		}
		if !strings.Contains(got, "commit") {
			t.Fatalf("arg %q: missing commit in %q", arg, got)
		}
		if !strings.Contains(got, "go") {
			t.Fatalf("arg %q: missing go version in %q", arg, got)
		}
	}
}

func TestRunUnknownAndEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"bogus"}, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if code := Run(nil, &out, &errb); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

type mockChecker struct {
	report application.PreflightReport
	err    error
}

func (m *mockChecker) Check(ctx context.Context, cfg *application.Config) (application.PreflightReport, error) {
	return m.report, m.err
}

func TestRunDoctor_Pass(t *testing.T) {
	checker := &mockChecker{
		report: application.PreflightReport{
			Checks: []application.CheckResult{
				{Name: "agy binary", Status: application.CheckPass, Detail: "/usr/bin/agy"},
				{Name: "agy version", Status: application.CheckPass, Detail: "1.3.3"},
			},
		},
	}
	app := NewApp(checker, func(path string) (*application.Config, error) {
		def := application.DefaultConfig()
		return &def, nil
	})

	var out, errb bytes.Buffer
	code := app.Run([]string{"doctor"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	got := out.String()
	if !strings.Contains(got, "CHECK") || !strings.Contains(got, "STATUS") {
		t.Errorf("missing table header in output %q", got)
	}
	if !strings.Contains(got, "ALL CHECKS PASSED") {
		t.Errorf("missing success banner in output %q", got)
	}
}

func TestRunDoctor_Fail(t *testing.T) {
	checker := &mockChecker{
		report: application.PreflightReport{
			Checks: []application.CheckResult{
				{Name: "agy binary", Status: application.CheckFail, Detail: "not found"},
			},
		},
	}
	app := NewApp(checker, func(path string) (*application.Config, error) {
		def := application.DefaultConfig()
		return &def, nil
	})

	var out, errb bytes.Buffer
	code := app.Run([]string{"doctor"}, &out, &errb)
	if code != ExitPreflight {
		t.Fatalf("exit = %d, want %d", code, ExitPreflight)
	}
	errStr := errb.String()
	if !strings.Contains(errStr, "PREFLIGHT FAILED") {
		t.Errorf("missing failure banner in stderr %q", errStr)
	}
}

func TestRunDoctor_ConfigFlag(t *testing.T) {
	var loadedPath string
	checker := &mockChecker{
		report: application.PreflightReport{
			Checks: []application.CheckResult{
				{Name: "agy binary", Status: application.CheckPass, Detail: "ok"},
			},
		},
	}
	app := NewApp(checker, func(path string) (*application.Config, error) {
		loadedPath = path
		def := application.DefaultConfig()
		return &def, nil
	})

	var out, errb bytes.Buffer
	code := app.Run([]string{"doctor", "--config", "/etc/custom/agyq.json"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if loadedPath != "/etc/custom/agyq.json" {
		t.Errorf("loadedPath = %q, want /etc/custom/agyq.json", loadedPath)
	}
}

func TestRunDoctor_Errors(t *testing.T) {
	// 1. Missing arg for --config
	app := NewApp(&mockChecker{}, nil)
	var out, errb bytes.Buffer
	if code := app.Run([]string{"doctor", "--config"}, &out, &errb); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}

	// 2. Unknown flag
	errb.Reset()
	if code := app.Run([]string{"doctor", "--invalid"}, &out, &errb); code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}

	// 3. Config loader returns error
	failingLoader := func(path string) (*application.Config, error) {
		return nil, errors.New("cannot read file")
	}
	app2 := NewApp(&mockChecker{}, failingLoader)
	errb.Reset()
	if code := app2.Run([]string{"doctor"}, &out, &errb); code != ExitPreflight {
		t.Errorf("exit = %d, want %d", code, ExitPreflight)
	}

	// 4. No checker configured
	app3 := NewApp(nil, nil)
	errb.Reset()
	if code := app3.Run([]string{"doctor"}, &out, &errb); code != ExitPreflight {
		t.Errorf("exit = %d, want %d", code, ExitPreflight)
	}
}

func TestRunInit_Scaffolding(t *testing.T) {
	tmp := t.TempDir()
	// Create mock .git dir
	if err := os.Mkdir(filepath.Join(tmp, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	// 1. Initial scaffold
	code := app.Run([]string{"init", tmp}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("init exit = %d, want %d; stderr=%s", code, ExitOK, errb.String())
	}
	if !strings.Contains(out.String(), "Created queue.md") {
		t.Errorf("expected 'Created queue.md', got: %s", out.String())
	}
	if !strings.Contains(out.String(), "Created agyq.json") {
		t.Errorf("expected 'Created agyq.json', got: %s", out.String())
	}
	if !strings.Contains(out.String(), "Configured .gitignore") {
		t.Errorf("expected 'Configured .gitignore', got: %s", out.String())
	}

	// Verify file contents
	queueData, err := os.ReadFile(filepath.Join(tmp, "queue.md"))
	if err != nil || !strings.Contains(string(queueData), "# #1 Scaffold starter prompt") {
		t.Errorf("queue.md content mismatch: %s", string(queueData))
	}
	cfgData, err := os.ReadFile(filepath.Join(tmp, "agyq.json"))
	if err != nil || !strings.Contains(string(cfgData), "dangerously-skip-permissions") {
		t.Errorf("agyq.json content mismatch: %s", string(cfgData))
	}

	// 2. Re-run without force should skip existing files
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"init", tmp}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("init re-run exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "queue.md already exists") {
		t.Errorf("expected 'queue.md already exists', got: %s", out.String())
	}
	if !strings.Contains(out.String(), "agyq.json already exists") {
		t.Errorf("expected 'agyq.json already exists', got: %s", out.String())
	}

	// 3. Re-run with --force should overwrite
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"init", "--force", tmp}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("init --force exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "Overwrote queue.md") {
		t.Errorf("expected 'Overwrote queue.md', got: %s", out.String())
	}
	if !strings.Contains(out.String(), "Overwrote agyq.json") {
		t.Errorf("expected 'Overwrote agyq.json', got: %s", out.String())
	}
}

func TestRunInit_FlagsAndErrors(t *testing.T) {
	tmp := t.TempDir()
	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	// Test custom permission mode
	code := app.Run([]string{"init", "--permission-mode", "settings-allowlist", tmp}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("init with permission-mode exit = %d, want %d", code, ExitOK)
	}
	cfgData, err := os.ReadFile(filepath.Join(tmp, "agyq.json"))
	if err != nil || !strings.Contains(string(cfgData), "settings-allowlist") {
		t.Errorf("expected settings-allowlist in agyq.json, got: %s", string(cfgData))
	}

	// Test missing argument for --permission-mode
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"init", "--permission-mode"}, &out, &errb)
	if code != ExitUsage {
		t.Errorf("expected ExitUsage for missing mode value, got %d", code)
	}

	// Test unknown flag
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"init", "--unknown-flag"}, &out, &errb)
	if code != ExitUsage {
		t.Errorf("expected ExitUsage for unknown flag, got %d", code)
	}
}

func TestRunReset_CleanArchive(t *testing.T) {
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"schema_version":1,"prompts":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(stateDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(`{"kind":"boot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	controlDir := filepath.Join(stateDir, "control")
	if err := os.MkdirAll(controlDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, "pause"), []byte("pause"), 0600); err != nil {
		t.Fatal(err)
	}

	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	code := app.Run([]string{"reset", "--state-dir", stateDir}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("reset exit = %d, want %d; stderr=%s", code, ExitOK, errb.String())
	}
	if !strings.Contains(out.String(), "archived previous run to") {
		t.Errorf("output missing archive notice: %s", out.String())
	}

	// Verify state.json is gone from stateDir
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state.json still exists in stateDir")
	}
	// Verify control file was cleaned
	if _, err := os.Stat(filepath.Join(controlDir, "pause")); !os.IsNotExist(err) {
		t.Errorf("control file still exists")
	}

	// Verify archive exists and contains state.json and events.jsonl
	archiveBase := filepath.Join(stateDir, "archive")
	entries, err := os.ReadDir(archiveBase)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 archive folder, got entries: %v (err: %v)", entries, err)
	}
	archivedState := filepath.Join(archiveBase, entries[0].Name(), "state.json")
	if _, err := os.Stat(archivedState); err != nil {
		t.Errorf("expected archived state.json to exist at %s", archivedState)
	}
}

func TestRunReset_NoArchive(t *testing.T) {
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"schema_version":1}`), 0600); err != nil {
		t.Fatal(err)
	}

	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	code := app.Run([]string{"reset", "--no-archive", "--state-dir", stateDir}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("reset exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "cleared state.json and runtime logs (no archive)") {
		t.Errorf("output mismatch: %s", out.String())
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("state.json still exists")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "archive")); !os.IsNotExist(err) {
		t.Errorf("archive folder should not exist")
	}
}

func TestRunReset_RefusesOnRunningPrompt(t *testing.T) {
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	mockStore := &mockStateStore{
		state: &domain.RunState{
			Records: []domain.PromptRecord{
				{Number: 1, Status: domain.StatusRunning},
			},
		},
	}

	factory := func(dir string) (application.StateStore, error) {
		return mockStore, nil
	}

	app := NewApp(nil, nil).WithStateStoreFactory(factory)
	var out, errb bytes.Buffer

	// 1. Refuses without --force
	code := app.Run([]string{"reset", "--state-dir", stateDir}, &out, &errb)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage on running prompt, got %d", code)
	}
	if !strings.Contains(errb.String(), "is currently running") {
		t.Errorf("stderr missing running warning: %s", errb.String())
	}

	// 2. Succeeds with --force
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"reset", "--force", "--state-dir", stateDir}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("expected ExitOK with --force, got %d; stderr=%s", code, errb.String())
	}
}

func TestRunReset_AlreadyClean(t *testing.T) {
	stateDir := t.TempDir()
	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	code := app.Run([]string{"reset", "--state-dir", stateDir}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK", code)
	}
	if !strings.Contains(out.String(), "already clean") {
		t.Errorf("output missing already clean: %s", out.String())
	}
}

func TestRunService_Install_DryRun(t *testing.T) {
	app := NewApp(nil, nil)
	var out, errb bytes.Buffer

	code := app.Run([]string{"service", "install", "--dry-run", "--user", "customuser", "--unit-name", "myagyq"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("service install dry-run exit = %d, want ExitOK; stderr=%s", code, errb.String())
	}

	outStr := out.String()
	if !strings.Contains(outStr, "User=customuser") {
		t.Errorf("output missing User=customuser: %s", outStr)
	}
	if !strings.Contains(outStr, `Environment="PATH=`) {
		t.Errorf("output missing Environment=PATH: %s", outStr)
	}
	if !strings.Contains(outStr, "myagyq.service") {
		t.Errorf("output missing unit name: %s", outStr)
	}
}

func TestRunService_Actions(t *testing.T) {
	var calledAction, calledUnit string
	runner := func(action string, unitName string) (string, error) {
		calledAction = action
		calledUnit = unitName
		return "Active: active (running)", nil
	}

	app := NewApp(nil, nil).WithServiceRunner(runner)
	var out, errb bytes.Buffer

	// 1. Status
	code := app.Run([]string{"service", "status", "agyq-custom"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("service status exit = %d, want ExitOK", code)
	}
	if calledAction != "status" || calledUnit != "agyq-custom" {
		t.Errorf("service runner called with %s %s, want status agyq-custom", calledAction, calledUnit)
	}
	if !strings.Contains(out.String(), "Active: active (running)") {
		t.Errorf("output mismatch: %s", out.String())
	}

	// 2. Start
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"service", "start"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("service start exit = %d, want ExitOK", code)
	}
	if calledAction != "start" || calledUnit != "agyq" {
		t.Errorf("service runner called with %s %s, want start agyq", calledAction, calledUnit)
	}

	// 3. Stop
	out.Reset()
	errb.Reset()
	code = app.Run([]string{"service", "stop"}, &out, &errb)
	if code != ExitOK {
		t.Fatalf("service stop exit = %d, want ExitOK", code)
	}
	if calledAction != "stop" || calledUnit != "agyq" {
		t.Errorf("service runner called with %s %s, want stop agyq", calledAction, calledUnit)
	}
}
