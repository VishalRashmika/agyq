package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"agyq/internal/application"
	"agyq/internal/domain"
)

// CurrentSchemaVersion is the schema version supported by this build.
const CurrentSchemaVersion = 1

// MigrationFunc transforms raw JSON state from version N to N+1.
type MigrationFunc func(raw []byte) ([]byte, error)

var migrations = map[int]MigrationFunc{}

// RegisterMigration registers a migration function from fromVersion to fromVersion+1.
func RegisterMigration(fromVersion int, fn MigrationFunc) {
	migrations[fromVersion] = fn
}

// ResetMigrationsForTest clears registered migrations (used in tests).
func ResetMigrationsForTest() {
	migrations = map[int]MigrationFunc{}
}

// FileStateStore implements application.StateStore with crash-safe persistence (F-010).
type FileStateStore struct {
	stateDir string
	eventLog application.EventLog // optional, for emitting warning events on fallback
	fs       FS
}

// FS is the filesystem interface allowing fault-injection testing.
type FS interface {
	WriteFile(name string, data []byte, perm os.FileMode) error
	SyncFile(name string) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
	Link(oldname, newname string) error
	CopyFile(src, dst string, perm os.FileMode) error
	SyncDir(dir string) error
	ReadFile(name string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
}

type osFS struct{}

func (osFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (osFS) SyncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (osFS) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func (osFS) Remove(name string) error {
	return os.Remove(name)
}

func (osFS) Link(oldname, newname string) error {
	return os.Link(oldname, newname)
}

func (osFS) CopyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (osFS) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// NewFileStateStore constructs a FileStateStore for stateDir.
func NewFileStateStore(stateDir string, eventLog application.EventLog) *FileStateStore {
	return &FileStateStore{
		stateDir: stateDir,
		eventLog: eventLog,
		fs:       osFS{},
	}
}

// NewFileStateStoreWithFS constructs a FileStateStore with a custom FS implementation (for testing).
func NewFileStateStoreWithFS(stateDir string, eventLog application.EventLog, fs FS) *FileStateStore {
	return &FileStateStore{
		stateDir: stateDir,
		eventLog: eventLog,
		fs:       fs,
	}
}

// StateFilePath returns the path to state.json.
func (s *FileStateStore) StateFilePath() string {
	return filepath.Join(s.stateDir, "state.json")
}

// BakFilePath returns the path to state.json.bak.
func (s *FileStateStore) BakFilePath() string {
	return filepath.Join(s.stateDir, "state.json.bak")
}

// Save atomically writes s to state.json following the 4-step recipe in docs/06 §1.
func (s *FileStateStore) Save(ctx context.Context, state *domain.RunState) error {
	if state == nil {
		return errors.New("cannot save nil state")
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = CurrentSchemaVersion
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode state json: %w", err)
	}
	data = append(data, '\n')

	stateFile := s.StateFilePath()
	tmpFile := filepath.Join(s.stateDir, "state.json.tmp")
	bakFile := s.BakFilePath()
	bakTmpFile := filepath.Join(s.stateDir, "state.json.bak.tmp")

	// Step 1: Write to state.json.tmp (mode 0600) and fsync it
	if err := s.fs.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", tmpFile, err)
	}
	if err := s.fs.SyncFile(tmpFile); err != nil {
		return fmt.Errorf("failed to sync %s: %w", tmpFile, err)
	}

	// Step 2: If state.json exists: back it up to state.json.bak atomically via temporary backup
	if _, err := s.fs.Stat(stateFile); err == nil {
		_ = s.fs.Remove(bakTmpFile)
		// Attempt hard link, fallback to copy if link fails
		if err := s.fs.Link(stateFile, bakTmpFile); err != nil {
			if copyErr := s.fs.CopyFile(stateFile, bakTmpFile, 0600); copyErr != nil {
				return fmt.Errorf("failed to create backup tmp %s: %w", bakTmpFile, copyErr)
			}
		}
		if err := s.fs.Rename(bakTmpFile, bakFile); err != nil {
			return fmt.Errorf("failed to rename %s to %s: %w", bakTmpFile, bakFile, err)
		}
	}

	// Step 3: Rename state.json.tmp -> state.json (atomic replace)
	if err := s.fs.Rename(tmpFile, stateFile); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %w", tmpFile, stateFile, err)
	}

	// Step 4: fsync the state directory
	if err := s.fs.SyncDir(s.stateDir); err != nil {
		return fmt.Errorf("failed to sync dir %s: %w", s.stateDir, err)
	}

	return nil
}

type schemaHeader struct {
	SchemaVersion int `json:"schema_version"`
}

// Load loads RunState from state.json, falling back to state.json.bak if state.json is corrupted (F-010).
// It verifies the schema version and refuses future versions (F-011).
func (s *FileStateStore) Load(ctx context.Context) (*domain.RunState, error) {
	stateFile := s.StateFilePath()
	bakFile := s.BakFilePath()

	data, err := s.fs.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, application.ErrNoState
		}
		return nil, fmt.Errorf("failed to read state file %s: %w", stateFile, err)
	}

	state, err := s.parseAndMigrate(data)
	if err != nil {
		// If the error is specifically a newer or invalid schema version, do not fallback to .bak
		var verErr *schemaVersionError
		if errors.As(err, &verErr) {
			return nil, err
		}

		// state.json is corrupt or truncated; try fallback to .bak
		bakData, bakErr := s.fs.ReadFile(bakFile)
		if bakErr == nil {
			bakState, errBak := s.parseAndMigrate(bakData)
			if errBak == nil {
				if s.eventLog != nil {
					s.eventLog.Emit(ctx, application.Event{
						Kind:  "state.corrupt_fallback",
						Level: "warn",
						Msg:   fmt.Sprintf("state.json was corrupt, fell back to %s", bakFile),
					})
				}
				return bakState, nil
			}
		}
		return nil, fmt.Errorf("state.json is corrupt and .bak unavailable: %w", err)
	}

	return state, nil
}

type schemaVersionError struct {
	msg string
}

func (e *schemaVersionError) Error() string {
	return e.msg
}

func (s *FileStateStore) parseAndMigrate(data []byte) (*domain.RunState, error) {
	var hdr schemaHeader
	if err := json.Unmarshal(data, &hdr); err != nil {
		return nil, err
	}
	if hdr.SchemaVersion > CurrentSchemaVersion {
		return nil, &schemaVersionError{
			msg: fmt.Sprintf("state schema version %d is newer than supported version %d", hdr.SchemaVersion, CurrentSchemaVersion),
		}
	}
	if hdr.SchemaVersion < 1 {
		return nil, &schemaVersionError{
			msg: fmt.Sprintf("invalid state schema version %d", hdr.SchemaVersion),
		}
	}

	curData, err := Migrate(data, hdr.SchemaVersion, CurrentSchemaVersion)
	if err != nil {
		return nil, err
	}

	var state domain.RunState
	if err := json.Unmarshal(curData, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state after migration: %w", err)
	}
	return &state, nil
}

// Migrate applies registered migrations sequentially until targetVersion is reached.
func Migrate(data []byte, currentVer, targetVersion int) ([]byte, error) {
	curData := data
	for currentVer < targetVersion {
		migrator, ok := migrations[currentVer]
		if !ok {
			return nil, fmt.Errorf("no migration available from schema version %d to %d", currentVer, currentVer+1)
		}
		var err error
		curData, err = migrator(curData)
		if err != nil {
			return nil, fmt.Errorf("migration from schema version %d to %d failed: %w", currentVer, currentVer+1, err)
		}
		currentVer++
	}
	return curData, nil
}
