package infrastructure

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// OSStrayProcessCleaner terminates leftover agy processes referencing workDir (F-050).
type OSStrayProcessCleaner struct{}

// NewOSStrayProcessCleaner constructs an OSStrayProcessCleaner.
func NewOSStrayProcessCleaner() *OSStrayProcessCleaner {
	return &OSStrayProcessCleaner{}
}

// CleanStrayProcesses terminates any agy process operating in or referencing workDir.
func (c *OSStrayProcessCleaner) CleanStrayProcesses(ctx context.Context, workDir string) error {
	if workDir == "" {
		return nil
	}

	// Find processes matching agy
	cmd := exec.CommandContext(ctx, "pgrep", "-f", "agy")
	out, err := cmd.Output()
	if err != nil {
		// exit code 1 means no processes matched; return cleanly
		return nil
	}

	myPid := os.Getpid()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid == myPid {
			continue
		}

		// Inspect /proc/<pid>/cmdline to ensure it belongs to the target workDir
		cmdlineBytes, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err != nil {
			continue
		}

		cmdline := string(bytes.ReplaceAll(cmdlineBytes, []byte{0}, []byte(" ")))
		if strings.Contains(cmdline, workDir) {
			// Found stray process holding workDir: send SIGTERM
			_ = syscall.Kill(pid, syscall.SIGTERM)

			// Wait up to 100ms for exit, then SIGKILL
			done := false
			for i := 0; i < 10; i++ {
				time.Sleep(10 * time.Millisecond)
				if err := syscall.Kill(pid, 0); err != nil {
					done = true
					break
				}
			}
			if !done {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}

	return nil
}
