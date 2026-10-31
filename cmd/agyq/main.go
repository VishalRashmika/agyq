// Command agyq is the composition root: the only place allowed to import every
// layer. Concrete adapters are wired into use cases here as phases add them.
package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"agyq/internal/application"
	"agyq/internal/infrastructure"
	"agyq/internal/presentation"
)

func main() {
	checker := infrastructure.NewPreflightService(nil)
	queueSource := infrastructure.NewFileQueueSource()
	clock := infrastructure.SystemClock{}
	sleeper := infrastructure.SystemSleeper{}

	stateStoreFactory := func(stateDir string) (application.StateStore, error) {
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			return nil, err
		}
		return infrastructure.NewFileStateStore(stateDir, nil), nil
	}

	orchestratorFactory := func(cfg application.Config) (presentation.OrchestratorRunner, error) {
		if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
			return nil, err
		}
		store := infrastructure.NewFileStateStore(cfg.StateDir, nil)
		runner := infrastructure.NewAgyProcessRunner(cfg)
		locker := infrastructure.NewFileLocker(cfg.StateDir)
		eventLog, err := infrastructure.NewFileEventLog(filepath.Join(cfg.StateDir, "events.jsonl"))
		if err != nil {
			return nil, err
		}
		cleaner := infrastructure.NewOSStrayProcessCleaner()
		checkpointer := infrastructure.NewGitCheckpointer()
		controlChannel := infrastructure.NewFileControlChannel(cfg.StateDir)
		return application.NewOrchestrator(
			cfg,
			queueSource,
			store,
			runner,
			eventLog,
			locker,
			clock,
			sleeper,
		).WithStrayProcessCleaner(cleaner).
			WithCheckpointer(checkpointer).
			WithControlChannel(controlChannel), nil
	}

	eventsReader := func(stateDir string, limit int) ([]application.Event, error) {
		eventsPath := filepath.Join(stateDir, "events.jsonl")
		events, err := infrastructure.ReadEventsFile(eventsPath)
		if err != nil {
			return nil, err
		}
		if limit > 0 && len(events) > limit {
			events = events[len(events)-limit:]
		}
		return events, nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serviceRunner := func(action string, unitName string) (string, error) {
		var args []string
		if action == "daemon-reload" {
			args = []string{"daemon-reload"}
		} else {
			args = []string{action, unitName}
		}
		cmd := exec.Command("systemctl", args...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	app := presentation.NewApp(checker, infrastructure.LoadConfigOrDefault).
		WithContext(ctx).
		WithQueueSource(queueSource).
		WithStateStoreFactory(stateStoreFactory).
		WithClock(clock).
		WithOrchestratorFactory(orchestratorFactory).
		WithEventsReader(eventsReader).
		WithControlSubmitter(infrastructure.WriteControlCommand).
		WithServiceRunner(serviceRunner)

	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}
