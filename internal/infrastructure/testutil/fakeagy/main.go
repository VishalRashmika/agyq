package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Envelope struct {
	ConversationID string         `json:"conversation_id"`
	Status         string         `json:"status"`
	Response       string         `json:"response"`
	Usage          map[string]any `json:"usage,omitempty"`
	DeniedActions  []any          `json:"denied_actions,omitempty"`
}

type InvocationRecord struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--child-hang" {
		for {
			time.Sleep(1 * time.Hour)
		}
	}

	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Println("agy 1.2.4 (fakeagy)")
			os.Exit(0)
		}
	}

	// Record args if requested
	recordFile := os.Getenv("FAKEAGY_RECORD_FILE")
	if recordFile != "" {
		cwd, _ := os.Getwd()
		rec := InvocationRecord{
			Args: os.Args[1:],
			Cwd:  cwd,
		}
		data, err := json.Marshal(rec)
		if err == nil {
			f, err := os.OpenFile(recordFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err == nil {
				_, _ = f.Write(append(data, '\n'))
				_ = f.Close()
			}
		}
	}

	// Respond to preflight probe
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == "-p" && i+1 < len(os.Args) && strings.Contains(os.Args[i+1], "Reply with exactly: OK") {
			env := Envelope{
				ConversationID: "conv-probe",
				Status:         "SUCCESS",
				Response:       "OK",
			}
			out, _ := json.Marshal(env)
			fmt.Println(string(out))
			os.Exit(0)
		}
	}

	scenario := determineScenario()

	// Parse conversation ID if present in args
	convID := "conv-fake-default"
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == "--conversation" && i+1 < len(os.Args) {
			convID = os.Args[i+1]
			break
		}
	}

	runScenario(scenario, convID)
}

func determineScenario() string {
	scriptPath := os.Getenv("FAKEAGY_SCRIPT")
	if scriptPath != "" {
		scenario, err := nextScriptScenario(scriptPath)
		if err == nil && scenario != "" {
			return scenario
		}
	}

	scenario := os.Getenv("FAKEAGY_SCENARIO")
	if scenario == "" {
		return "ok"
	}
	return scenario
}

func nextScriptScenario(scriptPath string) (string, error) {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "", nil
	}

	idxFile := scriptPath + ".idx"
	idx := 0
	if idxData, err := os.ReadFile(idxFile); err == nil {
		if val, err := strconv.Atoi(strings.TrimSpace(string(idxData))); err == nil {
			idx = val
		}
	}

	var current string
	if idx < len(lines) {
		current = strings.TrimSpace(lines[idx])
	} else {
		current = strings.TrimSpace(lines[len(lines)-1])
	}

	_ = os.WriteFile(idxFile, []byte(strconv.Itoa(idx+1)), 0600)
	return current, nil
}

func runScenario(scenario, convID string) {
	switch scenario {
	case "ok":
		env := Envelope{
			ConversationID: convID,
			Status:         "SUCCESS",
			Response:       "fake response: task completed successfully",
			Usage: map[string]any{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(0)

	case "permissions-denied":
		fmt.Fprintln(os.Stderr, `jetski: no output produced — a tool required the "command" permission that headless mode cannot prompt for, so it was auto-denied. Add an allow-rule under permissions.allow in settings.json (e.g. command(<target>)). Alternatively, re-run with --dangerously-skip-permissions to auto-approve all tools.`)
		env := Envelope{
			ConversationID: convID,
			Status:         "SUCCESS",
			Response:       "",
			DeniedActions:  []any{map[string]any{"action": "command"}},
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(0)

	case "slow":
		sleepDur := 300 * time.Millisecond
		if envSleep := os.Getenv("FAKEAGY_SLEEP"); envSleep != "" {
			if d, err := time.ParseDuration(envSleep); err == nil {
				sleepDur = d
			}
		}
		time.Sleep(sleepDur)
		env := Envelope{
			ConversationID: convID,
			Status:         "SUCCESS",
			Response:       "slow task completed",
			Usage: map[string]any{
				"input_tokens":  150,
				"output_tokens": 75,
			},
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(0)

	case "hang":
		for {
			time.Sleep(1 * time.Hour)
		}

	case "ignore-sigterm":
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM)
		go func() {
			for range sigCh {
				// Ignore SIGTERM
			}
		}()

		// Spawn a child process in the same process group that also hangs
		cmd := exec.Command(os.Args[0], "--child-hang")
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
		select {}

	case "limit-session":
		resetStr := "in 3h 12m"
		if envReset := os.Getenv("FAKEAGY_LIMIT_RESET"); envReset != "" {
			resetStr = envReset
		}
		fmt.Fprintf(os.Stderr, "usage limit reached: try again %s\n", resetStr)
		env := Envelope{
			ConversationID: convID,
			Status:         "RESOURCE_EXHAUSTED",
			Response:       fmt.Sprintf("Usage limit reached. Try again %s.", resetStr),
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(1)

	case "limit-weekly":
		fmt.Fprintln(os.Stderr, "weekly usage limit reached: try again in 5 days")
		env := Envelope{
			ConversationID: convID,
			Status:         "RESOURCE_EXHAUSTED",
			Response:       "Weekly usage limit exceeded. Resets on Sunday.",
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(1)

	case "crash":
		fmt.Fprintln(os.Stderr, "fatal error: process crashed abruptly")
		os.Exit(2)

	case "bad-json":
		fmt.Println(`{"status": "SUCCESS", "response": malformed json output`)
		os.Exit(0)

	case "partial-writes":
		fmt.Print(`{"conversation_id": "partial-response"`)
		os.Exit(1)

	default:
		// Unknown scenario, fallback to ok
		env := Envelope{
			ConversationID: convID,
			Status:         "SUCCESS",
			Response:       fmt.Sprintf("unknown scenario %s ran as ok", scenario),
		}
		out, _ := json.Marshal(env)
		fmt.Println(string(out))
		os.Exit(0)
	}
}
