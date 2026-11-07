package presentation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

// Set at build time via -ldflags (see Makefile).
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
)

// Exit codes (docs/03 F-060).
const (
	ExitOK        = 0
	ExitUsage     = 1
	ExitStopped   = 2
	ExitLocked    = 3
	ExitPreflight = 4
)

// ConfigLoaderFunc loads or provides configuration from a given file path.
type ConfigLoaderFunc func(path string) (*application.Config, error)

// StateStoreFactory creates an application.StateStore for the given state directory.
type StateStoreFactory func(stateDir string) (application.StateStore, error)

// OrchestratorRunner is the runnable orchestrator interface.
type OrchestratorRunner interface {
	Run(ctx context.Context) error
}

// OrchestratorFactory instantiates an OrchestratorRunner from configuration.
type OrchestratorFactory func(cfg application.Config) (OrchestratorRunner, error)

// EventsReaderFunc reads recent events from state directory.
type EventsReaderFunc func(stateDir string, limit int) ([]application.Event, error)

// ErrorsReaderFunc reads recent lines from the errors log file.
type ErrorsReaderFunc func(stateDir string, limit int) ([]string, error)

// ControlSubmitterFunc submits an operator control command to the state directory.
type ControlSubmitterFunc func(stateDir string, cmd application.ControlCommand) error

// ServiceRunnerFunc executes a system service action (e.g., systemctl action unit).
type ServiceRunnerFunc func(action string, unitName string) (string, error)

// App is the presentation-layer application coordinating CLI commands.
type App struct {
	Checker          application.PreflightChecker
	Loader           ConfigLoaderFunc
	QueueSource      application.QueueSource
	NewStateStore    StateStoreFactory
	Clock            application.Clock
	NewOrchestrator  OrchestratorFactory
	ReadRecentEvents EventsReaderFunc
	ReadRecentErrors ErrorsReaderFunc
	ControlSubmitter ControlSubmitterFunc
	ServiceRunner    ServiceRunnerFunc
	Context          context.Context
}

// NewApp constructs an App instance with injected dependencies.
func NewApp(checker application.PreflightChecker, loader ConfigLoaderFunc) *App {
	return &App{
		Checker: checker,
		Loader:  loader,
	}
}

// WithQueueSource configures the QueueSource adapter.
func (a *App) WithQueueSource(qs application.QueueSource) *App {
	a.QueueSource = qs
	return a
}

// WithStateStoreFactory configures the StateStore factory.
func (a *App) WithStateStoreFactory(ssf StateStoreFactory) *App {
	a.NewStateStore = ssf
	return a
}

// WithClock configures the Clock adapter.
func (a *App) WithClock(clk application.Clock) *App {
	a.Clock = clk
	return a
}

// WithOrchestratorFactory configures the Orchestrator factory.
func (a *App) WithOrchestratorFactory(of OrchestratorFactory) *App {
	a.NewOrchestrator = of
	return a
}

// WithEventsReader configures the EventsReader func.
func (a *App) WithEventsReader(er EventsReaderFunc) *App {
	a.ReadRecentEvents = er
	return a
}

// WithErrorsReader configures the ErrorsReader func.
func (a *App) WithErrorsReader(er ErrorsReaderFunc) *App {
	a.ReadRecentErrors = er
	return a
}

// WithControlSubmitter configures the ControlSubmitter func (F-062, F-063).
func (a *App) WithControlSubmitter(cs ControlSubmitterFunc) *App {
	a.ControlSubmitter = cs
	return a
}

// WithServiceRunner configures the system service command runner.
func (a *App) WithServiceRunner(sr ServiceRunnerFunc) *App {
	a.ServiceRunner = sr
	return a
}

// WithContext configures the execution context for cancellation and graceful shutdown.
func (a *App) WithContext(ctx context.Context) *App {
	a.Context = ctx
	return a
}

// Run executes the CLI and returns the process exit code using a default App.
func Run(args []string, stdout, stderr io.Writer) int {
	return NewApp(nil, nil).Run(args, stdout, stderr)
}

// Run executes the CLI against the App instance.
func (a *App) Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "agyq %s (commit %s, %s)\n", Version, Commit, runtime.Version())
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	case "doctor":
		return a.runDoctor(args[1:], stdout, stderr)
	case "init":
		return a.runInit(args[1:], stdout, stderr)
	case "plan":
		return a.runPlan(args[1:], stdout, stderr)
	case "run":
		return a.runRun(args[1:], stdout, stderr)
	case "status":
		return a.runStatus(args[1:], stdout, stderr)
	case "errors":
		return a.runErrors(args[1:], stdout, stderr)
	case "pause":
		return a.runPause(args[1:], stdout, stderr)
	case "resume":
		return a.runResume(args[1:], stdout, stderr)
	case "skip":
		return a.runSkip(args[1:], stdout, stderr)
	case "retry":
		return a.runRetry(args[1:], stdout, stderr)
	case "reset":
		return a.runReset(args[1:], stdout, stderr)
	case "service":
		return a.runService(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "agyq: unknown command %q\n\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

func (a *App) runDoctor(args []string, stdout, stderr io.Writer) int {
	if a.Checker == nil {
		fmt.Fprintln(stderr, "agyq doctor: preflight checker not configured")
		return ExitPreflight
	}

	var configPath string
	var queuePath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq doctor: --config requires a path argument")
				return ExitUsage
			}
		case "--queue", "-q":
			if i+1 < len(args) {
				queuePath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq doctor: --queue requires a path argument")
				return ExitUsage
			}
		default:
			fmt.Fprintf(stderr, "agyq doctor: unknown flag %q\n", args[i])
			return ExitUsage
		}
	}

	var cfg *application.Config
	if a.Loader != nil {
		var err error
		cfg, err = a.Loader(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "agyq doctor: failed to load config: %v\n", err)
			return ExitPreflight
		}
	} else {
		def := application.DefaultConfig()
		cfg = &def
	}

	if queuePath != "" {
		cfg.QueueFile = queuePath
	}

	ctx := context.Background()
	report, err := a.Checker.Check(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "agyq doctor: check failed: %v\n", err)
		return ExitPreflight
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CHECK\tSTATUS\tDETAIL")
	for _, c := range report.Checks {
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Status, c.Detail)
	}
	_ = w.Flush()

	if !report.AllPassed() {
		fmt.Fprintln(stderr, "\nDoctor result: PREFLIGHT FAILED")
		return ExitPreflight
	}

	fmt.Fprintln(stdout, "\nDoctor result: ALL CHECKS PASSED")
	return ExitOK
}

func (a *App) runPlan(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var queuePath string
	var stateDir string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq plan: --config requires a path argument")
				return ExitUsage
			}
		case "--queue", "-q":
			if i+1 < len(args) {
				queuePath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq plan: --queue requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq plan: --state-dir requires a path argument")
				return ExitUsage
			}
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "agyq plan: unknown flag %q\n", args[i])
				return ExitUsage
			}
			if queuePath == "" {
				queuePath = args[i]
			} else {
				fmt.Fprintf(stderr, "agyq plan: unexpected argument %q\n", args[i])
				return ExitUsage
			}
		}
	}

	var cfg *application.Config
	if a.Loader != nil {
		var err error
		cfg, err = a.Loader(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "agyq plan: failed to load config: %v\n", err)
			return ExitUsage
		}
	} else {
		def := application.DefaultConfig()
		cfg = &def
	}

	if queuePath != "" {
		cfg.QueueFile = queuePath
	}
	if stateDir != "" {
		cfg.StateDir = stateDir
	}

	if cfg.QueueFile == "" {
		fmt.Fprintln(stderr, "agyq plan: queue file must be specified via argument, flag, or config")
		return ExitUsage
	}
	if cfg.StateDir == "" {
		cfg.StateDir = ".agyq"
	}

	if a.QueueSource == nil {
		fmt.Fprintln(stderr, "agyq plan: queue source not configured")
		return ExitStopped
	}
	if a.NewStateStore == nil {
		fmt.Fprintln(stderr, "agyq plan: state store not configured")
		return ExitStopped
	}

	store, err := a.NewStateStore(cfg.StateDir)
	if err != nil {
		fmt.Fprintf(stderr, "agyq plan: failed to initialize state store: %v\n", err)
		return ExitStopped
	}

	svc := application.NewPlanService(a.QueueSource, store, a.Clock)
	ctx := context.Background()
	result, err := svc.Execute(ctx, application.PlanRequest{
		QueueFile: cfg.QueueFile,
		WorkDir:   cfg.WorkDir,
		StateDir:  cfg.StateDir,
		DryRun:    dryRun,
	})
	if err != nil {
		var valErr *domain.QueueValidationError
		if errors.As(err, &valErr) {
			fmt.Fprintln(stderr, "agyq plan: queue validation failed:")
			for _, e := range valErr.Errors {
				fmt.Fprintf(stderr, "  %s\n", e)
			}
			return ExitUsage
		}
		fmt.Fprintf(stderr, "agyq plan: %v\n", err)
		return ExitUsage
	}

	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "#\tSTATUS\tTITLE")
	statusCounts := make(map[domain.Status]int)
	for _, r := range result.State.Records {
		title := r.Title
		if title == "" {
			title = "(no title)"
		}
		statusCounts[r.Status]++
		fmt.Fprintf(w, "#%d\t%s\t%s\n", r.Number, r.Status, title)
	}
	_ = w.Flush()

	drySuffix := ""
	if dryRun {
		drySuffix = " [dry-run]"
	}
	fmt.Fprintf(stdout, "\nPlan: %d prompts (%d pending)%s\n", len(result.State.Records), statusCounts[domain.StatusPending], drySuffix)
	return ExitOK
}

func (a *App) runRun(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var queuePath string
	var stateDir string
	var workDir string
	var onFailureOverride application.FailurePolicy

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq run: --config requires a path argument")
				return ExitUsage
			}
		case "--queue", "-q":
			if i+1 < len(args) {
				queuePath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq run: --queue requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq run: --state-dir requires a path argument")
				return ExitUsage
			}
		case "--work-dir":
			if i+1 < len(args) {
				workDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq run: --work-dir requires a path argument")
				return ExitUsage
			}
		case "--on-failure":
			if i+1 < len(args) {
				policy := application.FailurePolicy(args[i+1])
				if policy != application.FailureStop && policy != application.FailureContinue {
					fmt.Fprintf(stderr, "agyq run: invalid --on-failure policy %q (must be stop or continue)\n", args[i+1])
					return ExitUsage
				}
				onFailureOverride = policy
				i++
			} else {
				fmt.Fprintln(stderr, "agyq run: --on-failure requires a policy (stop or continue)")
				return ExitUsage
			}
		case "--continue-on-failure":
			onFailureOverride = application.FailureContinue
		default:
			fmt.Fprintf(stderr, "agyq run: unknown flag %q\n", args[i])
			return ExitUsage
		}
	}

	var cfg *application.Config
	if a.Loader != nil {
		var err error
		cfg, err = a.Loader(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "agyq run: failed to load config: %v\n", err)
			return ExitUsage
		}
	} else {
		def := application.DefaultConfig()
		cfg = &def
	}

	if onFailureOverride != "" {
		cfg.OnFailure = onFailureOverride
	}

	if queuePath != "" {
		cfg.QueueFile = queuePath
	}
	if stateDir != "" {
		cfg.StateDir = stateDir
	}
	if workDir != "" {
		cfg.WorkDir = workDir
	}

	if cfg.QueueFile == "" {
		fmt.Fprintln(stderr, "agyq run: queue file must be specified via argument, flag, or config")
		return ExitUsage
	}
	if cfg.StateDir == "" {
		cfg.StateDir = ".agyq"
	}

	ctx := a.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Preflight checks (F-025, F-060 exit code 4)
	if a.Checker != nil {
		report, err := a.Checker.Check(ctx, cfg)
		if err != nil || !report.AllPassed() {
			fmt.Fprintln(stderr, "agyq run: preflight checks failed (exit code 4):")
			for _, c := range report.Checks {
				if c.Status == application.CheckFail {
					fmt.Fprintf(stderr, "  FAIL [%s]: %s\n", c.Name, c.Detail)
				}
			}
			return ExitPreflight
		}
	}

	if a.NewOrchestrator == nil {
		fmt.Fprintln(stderr, "agyq run: orchestrator not configured")
		return ExitStopped
	}

	orch, err := a.NewOrchestrator(*cfg)
	if err != nil {
		if errors.Is(err, application.ErrLocked) {
			fmt.Fprintln(stderr, "agyq run: lock already held by another process")
			return ExitLocked
		}
		fmt.Fprintf(stderr, "agyq run: initialization failed: %v\n", err)
		return ExitStopped
	}

	if err := orch.Run(ctx); err != nil {
		if errors.Is(err, application.ErrLocked) {
			fmt.Fprintln(stderr, "agyq run: lock already held by another process")
			return ExitLocked
		}
		if errors.Is(err, application.ErrQueueHalted) {
			fmt.Fprintln(stderr, "agyq run: stopped on failure (exit code 2). Remaining prompts stay pending. See .agyq/errors.log or 'agyq status' for details.")
			return ExitStopped
		}
		var valErr *domain.QueueValidationError
		if errors.As(err, &valErr) {
			fmt.Fprintf(stderr, "agyq run: queue validation error: %v\n", err)
			return ExitUsage
		}
		fmt.Fprintf(stderr, "agyq run: %v\n", err)
		return ExitStopped
	}

	if ctx.Err() != nil {
		fmt.Fprintln(stdout, "agyq run: interrupted (clean stop)")
		return ExitOK
	}

	fmt.Fprintln(stdout, "agyq run: queue completed successfully")
	return ExitOK
}

func (a *App) runStatus(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string
	var jsonOutput bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq status: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq status: --state-dir requires a path argument")
				return ExitUsage
			}
		default:
			fmt.Fprintf(stderr, "agyq status: unknown flag %q\n", args[i])
			return ExitUsage
		}
	}

	if stateDir == "" && a.Loader != nil {
		if cfg, err := a.Loader(configPath); err == nil && cfg != nil {
			stateDir = cfg.StateDir
		}
	}
	if stateDir == "" {
		stateDir = ".agyq"
	}

	if a.NewStateStore == nil {
		fmt.Fprintln(stderr, "agyq status: state store factory not configured")
		return ExitStopped
	}

	store, err := a.NewStateStore(stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "agyq status: %v\n", err)
		return ExitStopped
	}

	ctx := context.Background()
	state, err := store.Load(ctx)
	if err != nil {
		if errors.Is(err, application.ErrNoState) {
			fmt.Fprintf(stderr, "agyq status: no active run state found in %s\n", stateDir)
			return ExitOK
		}
		fmt.Fprintf(stderr, "agyq status: failed to load state: %v\n", err)
		return ExitStopped
	}

	var events []application.Event
	if a.ReadRecentEvents != nil {
		if evs, err := a.ReadRecentEvents(stateDir, 5); err == nil {
			events = evs
		}
	}

	if jsonOutput {
		outData := struct {
			State        *domain.RunState    `json:"state"`
			RecentEvents []application.Event `json:"recent_events"`
		}{
			State:        state,
			RecentEvents: events,
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(outData); err != nil {
			fmt.Fprintf(stderr, "agyq status: failed to marshal json: %v\n", err)
			return ExitStopped
		}
		return ExitOK
	}

	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "#\tSTATUS\tATTEMPTS\tLAST CHANGE\tTITLE")
	for _, r := range state.Records {
		title := r.Title
		if title == "" {
			title = "(no title)"
		}
		lastChange := r.UpdatedAt.UTC().Format("2006-01-02 15:04:05 UTC")
		if r.UpdatedAt.IsZero() {
			lastChange = "-"
		}
		fmt.Fprintf(w, "#%d\t%s\t%d\t%s\t%s\n", r.Number, r.Status, len(r.Attempts), lastChange, title)
	}
	_ = w.Flush()

	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}

	if state.Pause != nil && state.Pause.Until.After(now) {
		rem := state.Pause.Until.Sub(now).Round(time.Second)
		fmt.Fprintf(stdout, "\nPause: waiting until %s (reason: %s, %s remaining)\n", state.Pause.Until.Format("15:04:05 UTC"), state.Pause.Reason, rem)
	}
	if state.Operator.Paused {
		fmt.Fprintln(stdout, "\nOperator: paused")
	}

	fmt.Fprintf(stdout, "\nBoot count: %d\n", state.BootCount)

	if len(events) > 0 {
		fmt.Fprintln(stdout, "\nRecent events:")
		for _, ev := range events {
			fmt.Fprintf(stdout, "  %s [%s] %s #%d: %s\n", ev.Time.UTC().Format("15:04:05"), ev.Level, ev.Kind, ev.Prompt, ev.Msg)
		}
	}

	var failedPrompts []domain.PromptRecord
	hasPendingAfterFailure := false
	hasFailed := false
	for _, r := range state.Records {
		if r.Status == domain.StatusFailed {
			failedPrompts = append(failedPrompts, r)
			hasFailed = true
		} else if hasFailed && r.Status == domain.StatusPending {
			hasPendingAfterFailure = true
		}
	}

	if len(failedPrompts) > 0 {
		fmt.Fprintln(stdout, "\nFailed Prompts:")
		for _, fp := range failedPrompts {
			errMsg := "(no error details)"
			transcript := ""
			attemptN := len(fp.Attempts)
			if attemptN > 0 {
				lastAtt := fp.Attempts[attemptN-1]
				if lastAtt.Error != "" {
					errMsg = lastAtt.Error
				}
				if lastAtt.TranscriptPath != "" {
					transcript = lastAtt.TranscriptPath
				}
			}
			fmt.Fprintf(stdout, "  #%d [attempt %d]: %s\n", fp.Number, attemptN, errMsg)
			if transcript != "" {
				fmt.Fprintf(stdout, "      Transcript: %s\n", transcript)
			}
		}
		if hasPendingAfterFailure {
			fmt.Fprintln(stdout, "\nNotice: Queue halted on failure; subsequent prompts remain in pending status.")
			fmt.Fprintln(stdout, "To resume: run 'agyq retry <number>' or 'agyq skip <number>', or set \"on_failure\": \"continue\" in agyq.json.")
		}
		fmt.Fprintf(stdout, "Diagnostic error log: %s\n", filepath.Join(stateDir, "errors.log"))
	}

	return ExitOK
}

func (a *App) runErrors(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string
	tailLines := 50

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq errors: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq errors: --state-dir requires a path argument")
				return ExitUsage
			}
		case "--tail", "-n":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n > 0 {
					tailLines = n
				} else {
					fmt.Fprintf(stderr, "agyq errors: invalid --tail count %q\n", args[i+1])
					return ExitUsage
				}
				i++
			} else {
				fmt.Fprintln(stderr, "agyq errors: --tail requires a count argument")
				return ExitUsage
			}
		default:
			fmt.Fprintf(stderr, "agyq errors: unknown flag %q\n", args[i])
			return ExitUsage
		}
	}

	stateDir = a.resolveStateDir(configPath, stateDir)

	if a.ReadRecentErrors != nil {
		lines, err := a.ReadRecentErrors(stateDir, tailLines)
		if err != nil {
			fmt.Fprintf(stderr, "agyq errors: failed to read error log: %v\n", err)
			return ExitStopped
		}
		if len(lines) == 0 {
			fmt.Fprintln(stdout, "No error entries recorded.")
			return ExitOK
		}
		for _, line := range lines {
			fmt.Fprintln(stdout, line)
		}
		return ExitOK
	}

	fmt.Fprintln(stderr, "agyq errors: error log reader not configured")
	return ExitStopped
}

func (a *App) resolveStateDir(configPath, explicitStateDir string) string {
	if explicitStateDir != "" {
		return explicitStateDir
	}
	if a.Loader != nil {
		if cfg, err := a.Loader(configPath); err == nil && cfg != nil && cfg.StateDir != "" {
			return cfg.StateDir
		}
	}
	return ".agyq"
}

func (a *App) runPause(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq pause: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq pause: --state-dir requires a path argument")
				return ExitUsage
			}
		default:
			fmt.Fprintf(stderr, "agyq pause: unknown flag or argument %q\n", args[i])
			return ExitUsage
		}
	}

	stateDir = a.resolveStateDir(configPath, stateDir)
	if a.ControlSubmitter == nil {
		fmt.Fprintln(stderr, "agyq pause: control submitter not configured")
		return ExitStopped
	}

	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	cmd := application.ControlCommand{
		Kind:      application.CommandPause,
		CreatedAt: now,
	}
	if err := a.ControlSubmitter(stateDir, cmd); err != nil {
		fmt.Fprintf(stderr, "agyq pause: failed to submit command: %v\n", err)
		return ExitStopped
	}

	fmt.Fprintln(stdout, "agyq pause: submitted pause command")
	return ExitOK
}

func (a *App) runResume(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq resume: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq resume: --state-dir requires a path argument")
				return ExitUsage
			}
		default:
			fmt.Fprintf(stderr, "agyq resume: unknown flag or argument %q\n", args[i])
			return ExitUsage
		}
	}

	stateDir = a.resolveStateDir(configPath, stateDir)
	if a.ControlSubmitter == nil {
		fmt.Fprintln(stderr, "agyq resume: control submitter not configured")
		return ExitStopped
	}

	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	cmd := application.ControlCommand{
		Kind:      application.CommandResume,
		CreatedAt: now,
	}
	if err := a.ControlSubmitter(stateDir, cmd); err != nil {
		fmt.Fprintf(stderr, "agyq resume: failed to submit command: %v\n", err)
		return ExitStopped
	}

	fmt.Fprintln(stdout, "agyq resume: submitted resume command")
	return ExitOK
}

func (a *App) runSkip(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string
	var promptArg string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq skip: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq skip: --state-dir requires a path argument")
				return ExitUsage
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "agyq skip: unknown flag %q\n", args[i])
				return ExitUsage
			}
			if promptArg == "" {
				promptArg = args[i]
			} else {
				fmt.Fprintf(stderr, "agyq skip: unexpected argument %q\n", args[i])
				return ExitUsage
			}
		}
	}

	if promptArg == "" {
		fmt.Fprintln(stderr, "agyq skip: prompt number required (e.g. agyq skip 3)")
		return ExitUsage
	}

	promptNum, err := strconv.Atoi(promptArg)
	if err != nil || promptNum <= 0 {
		fmt.Fprintf(stderr, "agyq skip: invalid prompt number %q\n", promptArg)
		return ExitUsage
	}

	stateDir = a.resolveStateDir(configPath, stateDir)

	// Refuse on running prompt if state is readable
	if a.NewStateStore != nil {
		if store, err := a.NewStateStore(stateDir); err == nil {
			if st, err := store.Load(context.Background()); err == nil && st != nil {
				for _, r := range st.Records {
					if r.Number == domain.PromptNumber(promptNum) && r.Status == domain.StatusRunning {
						fmt.Fprintf(stderr, "agyq skip: cannot skip running prompt #%d (refusing)\n", promptNum)
						return ExitUsage
					}
				}
			}
		}
	}

	if a.ControlSubmitter == nil {
		fmt.Fprintln(stderr, "agyq skip: control submitter not configured")
		return ExitStopped
	}

	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	cmd := application.ControlCommand{
		Kind:      application.CommandSkip,
		Prompt:    domain.PromptNumber(promptNum),
		CreatedAt: now,
	}
	if err := a.ControlSubmitter(stateDir, cmd); err != nil {
		fmt.Fprintf(stderr, "agyq skip: failed to submit command: %v\n", err)
		return ExitStopped
	}

	fmt.Fprintf(stdout, "agyq skip: submitted skip command for prompt #%d\n", promptNum)
	return ExitOK
}

func (a *App) runRetry(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string
	var promptArg string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq retry: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq retry: --state-dir requires a path argument")
				return ExitUsage
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "agyq retry: unknown flag %q\n", args[i])
				return ExitUsage
			}
			if promptArg == "" {
				promptArg = args[i]
			} else {
				fmt.Fprintf(stderr, "agyq retry: unexpected argument %q\n", args[i])
				return ExitUsage
			}
		}
	}

	if promptArg == "" {
		fmt.Fprintln(stderr, "agyq retry: prompt number required (e.g. agyq retry 3)")
		return ExitUsage
	}

	promptNum, err := strconv.Atoi(promptArg)
	if err != nil || promptNum <= 0 {
		fmt.Fprintf(stderr, "agyq retry: invalid prompt number %q\n", promptArg)
		return ExitUsage
	}

	stateDir = a.resolveStateDir(configPath, stateDir)

	// Refuse on running prompt if state is readable
	if a.NewStateStore != nil {
		if store, err := a.NewStateStore(stateDir); err == nil {
			if st, err := store.Load(context.Background()); err == nil && st != nil {
				for _, r := range st.Records {
					if r.Number == domain.PromptNumber(promptNum) && r.Status == domain.StatusRunning {
						fmt.Fprintf(stderr, "agyq retry: cannot retry running prompt #%d (refusing)\n", promptNum)
						return ExitUsage
					}
				}
			}
		}
	}

	if a.ControlSubmitter == nil {
		fmt.Fprintln(stderr, "agyq retry: control submitter not configured")
		return ExitStopped
	}

	now := time.Now().UTC()
	if a.Clock != nil {
		now = a.Clock.Now()
	}
	cmd := application.ControlCommand{
		Kind:      application.CommandRetry,
		Prompt:    domain.PromptNumber(promptNum),
		CreatedAt: now,
	}
	if err := a.ControlSubmitter(stateDir, cmd); err != nil {
		fmt.Fprintf(stderr, "agyq retry: failed to submit command: %v\n", err)
		return ExitStopped
	}

	fmt.Fprintf(stdout, "agyq retry: submitted retry command for prompt #%d\n", promptNum)
	return ExitOK
}

func (a *App) runInit(args []string, stdout, stderr io.Writer) int {
	var targetDir string
	var force bool
	var permissionMode string = "dangerously-skip-permissions"

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			force = true
		case "--permission-mode":
			if i+1 < len(args) {
				permissionMode = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq init: --permission-mode requires a value")
				return ExitUsage
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(stderr, "agyq init: unknown option %q\n", args[i])
				return ExitUsage
			}
			if targetDir == "" {
				targetDir = args[i]
			} else {
				fmt.Fprintf(stderr, "agyq init: unexpected argument %q\n", args[i])
				return ExitUsage
			}
		}
	}

	if targetDir == "" {
		targetDir = "."
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		fmt.Fprintf(stderr, "agyq init: failed to resolve directory %q: %v\n", targetDir, err)
		return ExitUsage
	}

	if err := os.MkdirAll(absTarget, 0755); err != nil {
		fmt.Fprintf(stderr, "agyq init: failed to create directory %q: %v\n", absTarget, err)
		return ExitUsage
	}

	fmt.Fprintf(stdout, "Initializing agyq project in %s...\n", absTarget)

	// 1. queue.md
	queuePath := filepath.Join(absTarget, "queue.md")
	queueExisted := false
	if _, err := os.Stat(queuePath); err == nil {
		queueExisted = true
	}
	if queueExisted && !force {
		fmt.Fprintln(stdout, "  queue.md already exists (skipped; use --force to overwrite)")
	} else {
		if err := os.WriteFile(queuePath, []byte(application.DefaultStarterQueue), 0644); err != nil {
			fmt.Fprintf(stderr, "agyq init: failed to write queue.md: %v\n", err)
			return ExitUsage
		}
		if queueExisted && force {
			fmt.Fprintln(stdout, "  Overwrote queue.md")
		} else {
			fmt.Fprintln(stdout, "  Created queue.md")
		}
	}

	// 2. agyq.json
	cfgPath := filepath.Join(absTarget, "agyq.json")
	cfgExisted := false
	if _, err := os.Stat(cfgPath); err == nil {
		cfgExisted = true
	}
	if cfgExisted && !force {
		fmt.Fprintln(stdout, "  agyq.json already exists (skipped; use --force to overwrite)")
	} else {
		content := application.DefaultAgyqConfigJSON(permissionMode)
		if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(stderr, "agyq init: failed to write agyq.json: %v\n", err)
			return ExitUsage
		}
		if cfgExisted && force {
			fmt.Fprintln(stdout, "  Overwrote agyq.json")
		} else {
			fmt.Fprintln(stdout, "  Created agyq.json")
		}
	}

	// 3. .gitignore
	gitPath := filepath.Join(absTarget, ".git")
	isGit := false
	if _, err := os.Stat(gitPath); err == nil {
		isGit = true
	}
	if isGit {
		giPath := filepath.Join(absTarget, ".gitignore")
		giContent, _ := os.ReadFile(giPath)
		entries := []string{
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
		var missing []string
		for _, e := range entries {
			if !strings.Contains(string(giContent), e) {
				missing = append(missing, e)
			}
		}
		if len(missing) > 0 {
			var toAppend string
			if len(giContent) > 0 && !strings.HasSuffix(string(giContent), "\n") {
				toAppend += "\n"
			}
			toAppend += "# agyq runtime state, queues, cache, and logs\n"
			for _, m := range missing {
				toAppend += m + "\n"
			}
			f, err := os.OpenFile(giPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				_, _ = f.WriteString(toAppend)
				_ = f.Close()
				fmt.Fprintln(stdout, "  Configured .gitignore (.agyq/, queue.md, cache, logs)")
			}
		} else {
			fmt.Fprintln(stdout, "  .gitignore already configured (.agyq/)")
		}
	} else {
		fmt.Fprintln(stdout, "  Note: not a git repository root (run 'git init' before executing prompts)")
	}

	// 4. AGENTS.md (autonomous coding instructions if not already present)
	agentsPath := filepath.Join(absTarget, "AGENTS.md")
	geminiPath := filepath.Join(absTarget, "GEMINI.md")
	if _, errA := os.Stat(agentsPath); os.IsNotExist(errA) {
		if _, errG := os.Stat(geminiPath); os.IsNotExist(errG) {
			if err := os.WriteFile(agentsPath, []byte(application.DefaultAgentsMD), 0644); err == nil {
				fmt.Fprintln(stdout, "  Created AGENTS.md (autonomous coding instructions)")
			}
		}
	}

	// 5. Run preflight doctor check if configured
	if a.Checker != nil && a.Loader != nil {
		cfg, err := a.Loader(cfgPath)
		if err == nil {
			ctx := a.Context
			if ctx == nil {
				ctx = context.Background()
			}
			report, checkErr := a.Checker.Check(ctx, cfg)

			if checkErr == nil && report.AllPassed() {
				fmt.Fprintf(stdout, "  Preflight checks: %d/%d passed\n", len(report.Checks), len(report.Checks))
			} else if checkErr == nil {
				passed := 0
				for _, c := range report.Checks {
					if c.Status == application.CheckPass {
						passed++
					}
				}
				fmt.Fprintf(stdout, "  Preflight checks: %d/%d passed (run 'agyq doctor' for details)\n", passed, len(report.Checks))
			}
		}
	}

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Next steps:")
	fmt.Fprintln(stdout, "  1. Edit queue.md with your prompts")
	fmt.Fprintln(stdout, "  2. Run 'agyq plan' to preview the execution plan")
	fmt.Fprintln(stdout, "  3. Run 'agyq run' to start sequential execution")

	return ExitOK
}

func (a *App) runReset(args []string, stdout, stderr io.Writer) int {
	var configPath string
	var stateDir string
	var force bool
	var archive bool = true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq reset: --config requires a path argument")
				return ExitUsage
			}
		case "--state-dir":
			if i+1 < len(args) {
				stateDir = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq reset: --state-dir requires a path argument")
				return ExitUsage
			}
		case "--force", "-f":
			force = true
		case "--no-archive":
			archive = false
		case "--archive":
			archive = true
		default:
			fmt.Fprintf(stderr, "agyq reset: unknown option %q\n", args[i])
			return ExitUsage
		}
	}

	stateDir = a.resolveStateDir(configPath, stateDir)
	statePath := filepath.Join(stateDir, "state.json")

	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		fmt.Fprintf(stdout, "agyq reset: no active state found in %s (already clean)\n", stateDir)
		return ExitOK
	}

	// Refusal check: do not reset while a prompt is actively running
	if !force && a.NewStateStore != nil {
		store, err := a.NewStateStore(stateDir)
		if err == nil {
			ctx := a.Context
			if ctx == nil {
				ctx = context.Background()
			}
			st, err := store.Load(ctx)
			if err == nil && st != nil {
				for _, rec := range st.Records {
					if rec.Status == domain.StatusRunning {
						fmt.Fprintf(stderr, "agyq reset: prompt #%d is currently running. Stop agyq first, or use --force.\n", rec.Number)
						return ExitUsage
					}
				}
			}
		}
	}

	if archive {
		now := time.Now().UTC()
		if a.Clock != nil {
			now = a.Clock.Now()
		}
		archiveDir := filepath.Join(stateDir, "archive", now.Format("20060102-150405"))
		if err := os.MkdirAll(archiveDir, 0700); err != nil {
			fmt.Fprintf(stderr, "agyq reset: failed to create archive dir %s: %v\n", archiveDir, err)
			return ExitUsage
		}

		items := []string{
			"state.json",
			"state.json.bak",
			"events.jsonl",
			"transcripts",
			"agyq.log",
		}
		// Also scan for rotated agyq.log.*
		if entries, err := os.ReadDir(stateDir); err == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "agyq.log.") {
					items = append(items, e.Name())
				}
			}
		}

		for _, item := range items {
			src := filepath.Join(stateDir, item)
			if _, err := os.Stat(src); err == nil {
				dst := filepath.Join(archiveDir, item)
				_ = os.Rename(src, dst)
			}
		}
		fmt.Fprintf(stdout, "agyq reset: archived previous run to %s\n", archiveDir)
	} else {
		_ = os.Remove(filepath.Join(stateDir, "state.json"))
		_ = os.Remove(filepath.Join(stateDir, "state.json.bak"))
		_ = os.Remove(filepath.Join(stateDir, "events.jsonl"))
		_ = os.RemoveAll(filepath.Join(stateDir, "transcripts"))
		fmt.Fprintln(stdout, "agyq reset: cleared state.json and runtime logs (no archive)")
	}

	// Clean any pending control files
	controlDir := filepath.Join(stateDir, "control")
	if entries, err := os.ReadDir(controlDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(controlDir, e.Name()))
		}
	}

	fmt.Fprintln(stdout, "State reset successfully.")
	fmt.Fprintln(stdout, "Ready for next prompt batch. Run 'agyq plan' or 'agyq run' to start.")
	return ExitOK
}

func (a *App) runService(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "agyq service: missing subcommand (install, status, start, stop)")
		return ExitUsage
	}

	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "install":
		return a.runServiceInstall(rest, stdout, stderr)
	case "status":
		return a.runServiceAction("status", rest, stdout, stderr)
	case "start":
		return a.runServiceAction("start", rest, stdout, stderr)
	case "stop":
		return a.runServiceAction("stop", rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "agyq service: unknown subcommand %q (expected install, status, start, stop)\n", subcmd)
		return ExitUsage
	}
}

func (a *App) runServiceInstall(args []string, stdout, stderr io.Writer) int {
	var user string
	var unitName string = "agyq"
	var configPath string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--user":
			if i+1 < len(args) {
				user = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq service install: --user requires a username")
				return ExitUsage
			}
		case "--unit-name":
			if i+1 < len(args) {
				unitName = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq service install: --unit-name requires a name")
				return ExitUsage
			}
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				fmt.Fprintln(stderr, "agyq service install: --config requires a path")
				return ExitUsage
			}
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(stderr, "agyq service install: unknown option %q\n", args[i])
			return ExitUsage
		}
	}

	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = "root"
		}
	}

	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/home/" + user
	}

	workDir, _ := os.Getwd()
	if configPath == "" {
		if _, err := os.Stat("agyq.json"); err == nil {
			configPath = filepath.Join(workDir, "agyq.json")
		} else {
			configPath = "/etc/agyq/agyq.json"
		}
	} else if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}

	execPath, err := os.Executable()
	if err != nil {
		execPath = "/usr/local/bin/agyq"
	}

	sysPath := application.BuildServicePath(home, os.Getenv("PATH"))

	unitContent := application.GenerateServiceUnit(application.ServiceUnitParams{
		User:       user,
		Group:      user,
		WorkDir:    workDir,
		Home:       home,
		Path:       sysPath,
		Executable: execPath,
		ConfigPath: configPath,
	})

	if dryRun {
		fmt.Fprintf(stdout, "# Generated systemd unit for %s.service:\n", unitName)
		fmt.Fprint(stdout, unitContent)
		return ExitOK
	}

	targetPath := fmt.Sprintf("/etc/systemd/system/%s.service", unitName)
	err = os.WriteFile(targetPath, []byte(unitContent), 0644)
	if err != nil {
		fmt.Fprintf(stderr, "agyq service install: cannot write to %s (permission denied)\n", targetPath)
		fmt.Fprintln(stderr, "\nRun with sudo or manually install using:")
		fmt.Fprintf(stderr, "  sudo agyq service install\n\nOr create %s with:\n\n", targetPath)
		fmt.Fprint(stderr, unitContent)
		return ExitUsage
	}

	fmt.Fprintf(stdout, "Installed systemd service unit to %s\n", targetPath)

	if a.ServiceRunner != nil {
		_, _ = a.ServiceRunner("daemon-reload", "")
		fmt.Fprintln(stdout, "Reloaded systemd daemon.")
	}

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Next steps:")
	fmt.Fprintf(stdout, "  sudo systemctl start %s\n", unitName)
	fmt.Fprintf(stdout, "  sudo systemctl status %s\n", unitName)
	fmt.Fprintf(stdout, "  sudo journalctl -u %s -f\n", unitName)

	return ExitOK
}

func (a *App) runServiceAction(action string, args []string, stdout, stderr io.Writer) int {
	var unitName string = "agyq"
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			fmt.Fprintf(stderr, "agyq service %s: unknown option %q\n", action, args[i])
			return ExitUsage
		}
		unitName = args[i]
	}

	if a.ServiceRunner != nil {
		out, err := a.ServiceRunner(action, unitName)
		if err != nil {
			fmt.Fprintf(stderr, "systemctl %s %s failed: %v\n%s", action, unitName, err, out)
			return ExitUsage
		}
		if out != "" {
			fmt.Fprint(stdout, out)
		} else {
			fmt.Fprintf(stdout, "systemctl %s %s completed successfully.\n", action, unitName)
		}
		return ExitOK
	}

	fmt.Fprintf(stdout, "Systemd service runner not configured. Run manually:\n  sudo systemctl %s %s\n", action, unitName)
	return ExitOK
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "agyq - sequential prompt queue runner for the Antigravity CLI")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "usage: agyq <command> [flags]")
	fmt.Fprintln(w, "  init [--force] [--permission-mode mode] [dir] scaffold a new agyq project")
	fmt.Fprintln(w, "  version               print version")
	fmt.Fprintln(w, "  doctor [--config path] [--queue path] run preflight environment checks")
	fmt.Fprintln(w, "  plan [--config path] [--dry-run] [queue-file] parse queue and preview plan")
	fmt.Fprintln(w, "  run [--config path] [--on-failure stop|continue] start sequential queue orchestrator")
	fmt.Fprintln(w, "  status [--json]       display queue status and recent events")
	fmt.Fprintln(w, "  errors [--tail N]     display recent error log entries")
	fmt.Fprintln(w, "  pause                 pause queue between prompts")
	fmt.Fprintln(w, "  resume                resume paused queue")
	fmt.Fprintln(w, "  skip <N>              skip pending or interrupted prompt #N")
	fmt.Fprintln(w, "  retry <N>             reset failed prompt #N to pending and clear attempt cap")
	fmt.Fprintln(w, "  reset [--force] [--no-archive] archive previous batch and reset state")
	fmt.Fprintln(w, "  service <install|status|start|stop> manage systemd service unit")
	fmt.Fprintln(w, "  help                  show this help")
}
