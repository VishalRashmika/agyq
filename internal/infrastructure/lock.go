package infrastructure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"agyq/internal/application"
)

// FileLocker implements application.Locker using flock on state_dir/agyq.lock (F-013).
type FileLocker struct {
	stateDir string
}

// NewFileLocker creates a FileLocker for the specified state directory.
func NewFileLocker(stateDir string) *FileLocker {
	return &FileLocker{stateDir: stateDir}
}

// LockPath returns the path to agyq.lock.
func (l *FileLocker) LockPath() string {
	return filepath.Join(l.stateDir, "agyq.lock")
}

// Acquire attempts to acquire an exclusive, non-blocking advisory lock on agyq.lock.
// If another instance holds the lock, it returns an error wrapping application.ErrLocked
// naming the PID of the holding instance if readable.
func (l *FileLocker) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lockPath := l.LockPath()
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// Read PID from lock file
		pidBytes, _ := os.ReadFile(lockPath)
		pidStr := strings.TrimSpace(string(pidBytes))
		_ = f.Close()

		if pidStr != "" {
			return nil, fmt.Errorf("another instance of agyq is already running (PID %s): %w", pidStr, application.ErrLocked)
		}
		return nil, fmt.Errorf("another instance of agyq is already running: %w", application.ErrLocked)
	}

	// Lock acquired: truncate and record PID
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	_ = f.Sync()

	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		})
	}

	return release, nil
}
