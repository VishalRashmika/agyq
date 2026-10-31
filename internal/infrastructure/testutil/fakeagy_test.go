package testutil

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFakeAgy_Scenarios(t *testing.T) {
	bin := BuildFakeAgy(t)

	tests := []struct {
		name         string
		scenario     string
		expectedExit int
		wantStatus   string
		wantStderr   string
	}{
		{
			name:         "ok scenario",
			scenario:     "ok",
			expectedExit: 0,
			wantStatus:   "SUCCESS",
		},
		{
			name:         "slow scenario",
			scenario:     "slow",
			expectedExit: 0,
			wantStatus:   "SUCCESS",
		},
		{
			name:         "limit-session scenario",
			scenario:     "limit-session",
			expectedExit: 1,
			wantStatus:   "RESOURCE_EXHAUSTED",
			wantStderr:   "usage limit reached",
		},
		{
			name:         "limit-weekly scenario",
			scenario:     "limit-weekly",
			expectedExit: 1,
			wantStatus:   "RESOURCE_EXHAUSTED",
			wantStderr:   "weekly usage limit",
		},
		{
			name:         "crash scenario",
			scenario:     "crash",
			expectedExit: 2,
			wantStderr:   "fatal error: process crashed",
		},
		{
			name:         "bad-json scenario",
			scenario:     "bad-json",
			expectedExit: 0,
		},
		{
			name:         "partial-writes scenario",
			scenario:     "partial-writes",
			expectedExit: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "-p", "hello")
			cmd.Env = append(os.Environ(), "FAKEAGY_SCENARIO="+tc.scenario)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected execution error: %v", err)
				}
			}

			if exitCode != tc.expectedExit {
				t.Errorf("exit code got %d, want %d", exitCode, tc.expectedExit)
			}

			if tc.wantStatus != "" {
				var env struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
					t.Fatalf("stdout unmarshal failed: %v, raw stdout: %s", err, stdout.String())
				}
				if env.Status != tc.wantStatus {
					t.Errorf("status got %q, want %q", env.Status, tc.wantStatus)
				}
			}

			if tc.wantStderr != "" && !bytes.Contains(stderr.Bytes(), []byte(tc.wantStderr)) {
				t.Errorf("stderr got %q, want to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

func TestFakeAgy_RecordArgs(t *testing.T) {
	bin := BuildFakeAgy(t)
	recFile := filepath.Join(t.TempDir(), "args.jsonl")

	cmd := exec.Command(bin, "-p", "test prompt with \"quotes\" and $VAR", "--output-format", "json", "--model", "gemini-3.8-flash-high")
	cmd.Env = append(os.Environ(), "FAKEAGY_RECORD_FILE="+recFile, "FAKEAGY_SCENARIO=ok")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(recFile)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}

	var rec struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}

	if len(rec.Args) != 6 {
		t.Fatalf("expected 6 args, got %d: %v", len(rec.Args), rec.Args)
	}
	if rec.Args[1] != "test prompt with \"quotes\" and $VAR" {
		t.Errorf("arg[1] got %q", rec.Args[1])
	}
}

func TestFakeAgy_ScriptProgression(t *testing.T) {
	bin := BuildFakeAgy(t)
	scriptFile := filepath.Join(t.TempDir(), "scenario.script")
	if err := os.WriteFile(scriptFile, []byte("limit-session\nok\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// First invocation should run limit-session
	cmd1 := exec.Command(bin, "-p", "run 1")
	cmd1.Env = append(os.Environ(), "FAKEAGY_SCRIPT="+scriptFile)
	var out1 bytes.Buffer
	cmd1.Stdout = &out1
	_ = cmd1.Run()
	var env1 struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(out1.Bytes(), &env1)
	if env1.Status != "RESOURCE_EXHAUSTED" {
		t.Errorf("call 1 status got %q, want RESOURCE_EXHAUSTED", env1.Status)
	}

	// Second invocation should run ok
	cmd2 := exec.Command(bin, "-p", "run 2")
	cmd2.Env = append(os.Environ(), "FAKEAGY_SCRIPT="+scriptFile)
	var out2 bytes.Buffer
	cmd2.Stdout = &out2
	if err := cmd2.Run(); err != nil {
		t.Fatalf("call 2 failed: %v", err)
	}
	var env2 struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(out2.Bytes(), &env2)
	if env2.Status != "SUCCESS" {
		t.Errorf("call 2 status got %q, want SUCCESS", env2.Status)
	}
}

func TestFakeAgy_IgnoreSigterm(t *testing.T) {
	bin := BuildFakeAgy(t)

	cmd := exec.Command(bin, "-p", "hang")
	cmd.Env = append(os.Environ(), "FAKEAGY_SCENARIO=ignore-sigterm")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	pgid := cmd.Process.Pid

	time.Sleep(100 * time.Millisecond)

	// Send SIGTERM to process group
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Wait 200ms - process should still be alive
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		t.Fatalf("process exited on SIGTERM when it was supposed to ignore it")
	case <-time.After(200 * time.Millisecond):
		// Process is still alive as expected
	}

	// Send SIGKILL to process group
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	select {
	case <-done:
		// Killed successfully
	case <-time.After(2 * time.Second):
		t.Fatalf("process did not exit on SIGKILL")
	}
}
