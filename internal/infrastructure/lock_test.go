package infrastructure

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"agyq/internal/application"
)

func TestFileLocker_Basic(t *testing.T) {
	dir := t.TempDir()
	locker := NewFileLocker(dir)
	ctx := context.Background()

	// First acquire succeeds
	release1, err := locker.Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	// Check file mode 0600 on agyq.lock
	st, err := os.Stat(locker.LockPath())
	if err != nil {
		t.Fatalf("stat lock file failed: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("expected lock file perm 0600, got %#o", perm)
	}

	// Second acquire in same process (another FileLocker or same)
	locker2 := NewFileLocker(dir)
	_, err = locker2.Acquire(ctx)
	if err == nil {
		t.Fatalf("expected second Acquire to fail, got nil")
	}
	if !errors.Is(err, application.ErrLocked) {
		t.Errorf("expected ErrLocked, got: %v", err)
	}

	// Release first lock
	release1()

	// Third acquire succeeds
	release3, err := locker2.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after release failed: %v", err)
	}
	release3()
}

// TestHelperProcess_HoldingLock is spawned as a child process by TestFileLocker_IntegrationTwoProcesses
func TestHelperProcess_HoldingLock(t *testing.T) {
	if os.Getenv("TEST_LOCK_CHILD") != "1" {
		return
	}
	dir := os.Getenv("TEST_LOCK_DIR")
	locker := NewFileLocker(dir)
	release, err := locker.Acquire(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "child Acquire failed: %v\n", err)
		os.Exit(1)
	}
	defer release()

	// Print readiness line
	fmt.Println("LOCKED")
	_ = os.Stdout.Sync()

	time.Sleep(1 * time.Second)
	os.Exit(0)
}

func TestFileLocker_IntegrationTwoProcesses(t *testing.T) {
	// F-013 AC: Integration test spawns two processes; second exits with message naming PID.
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_HoldingLock$")
	cmd.Env = append(os.Environ(), "TEST_LOCK_CHILD=1", "TEST_LOCK_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start failed: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait for child to output "LOCKED"
	r := bufio.NewReader(stdout)
	line, err := r.ReadString('\n')
	if err != nil || !strings.Contains(line, "LOCKED") {
		t.Fatalf("child failed to signal LOCKED: line=%q, err=%v", line, err)
	}

	// Main process tries to acquire lock
	locker := NewFileLocker(dir)
	_, err = locker.Acquire(context.Background())
	if err == nil {
		t.Fatalf("expected Acquire to fail while child holds lock, got nil")
	}
	if !errors.Is(err, application.ErrLocked) {
		t.Errorf("expected ErrLocked, got: %v", err)
	}

	childPIDStr := strconv.Itoa(cmd.Process.Pid)
	if !strings.Contains(err.Error(), childPIDStr) {
		t.Errorf("expected error to name child PID %s, got: %v", childPIDStr, err)
	}

	// Wait for child to exit
	_ = cmd.Wait()

	// Now parent acquires lock cleanly
	release, err := locker.Acquire(context.Background())
	if err != nil {
		t.Fatalf("expected Acquire to succeed after child finished, got: %v", err)
	}
	release()
}
