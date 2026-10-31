package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agyq/internal/application"
)

// FileControlChannel implements application.ControlChannel using JSON files in state_dir/control/.
type FileControlChannel struct {
	mu       sync.Mutex
	stateDir string
}

// NewFileControlChannel creates a FileControlChannel for the given state directory.
func NewFileControlChannel(stateDir string) *FileControlChannel {
	return &FileControlChannel{stateDir: stateDir}
}

// Poll reads and consumes the oldest pending control command from state_dir/control/ (FIFO).
func (f *FileControlChannel) Poll(ctx context.Context) (application.ControlCommand, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	dir := filepath.Join(f.stateDir, "control")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return application.ControlCommand{}, false
	}

	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}

	if len(jsonFiles) == 0 {
		return application.ControlCommand{}, false
	}

	// Lexicographical sort by filename (timestamp prefix ensures FIFO ordering)
	sort.Strings(jsonFiles)

	for _, name := range jsonFiles {
		filePath := filepath.Join(dir, name)
		data, err := os.ReadFile(filePath)
		// Consume by deleting immediately
		_ = os.Remove(filePath)
		if err != nil {
			continue
		}

		var cmd application.ControlCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			continue
		}
		return cmd, true
	}

	return application.ControlCommand{}, false
}

// WriteControlCommand atomically creates a control command file in state_dir/control/.
func WriteControlCommand(stateDir string, cmd application.ControlCommand) error {
	dir := filepath.Join(stateDir, "control")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create control dir: %w", err)
	}

	data, err := json.MarshalIndent(cmd, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal control command: %w", err)
	}

	filename := fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), cmd.Kind)
	tmpFile, err := os.CreateTemp(dir, ".cmd-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp control file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod control file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write control file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("sync control file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close control file: %w", err)
	}

	finalPath := filepath.Join(dir, filename)
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename control file: %w", err)
	}

	return nil
}
