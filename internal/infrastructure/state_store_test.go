package infrastructure

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

type mockEventLog struct {
	mu     sync.Mutex
	events []application.Event
}

func (m *mockEventLog) Emit(ctx context.Context, e application.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *mockEventLog) Events() []application.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]application.Event, len(m.events))
	copy(res, m.events)
	return res
}

func sampleState(promptID domain.PromptNumber, title string) *domain.RunState {
	return &domain.RunState{
		SchemaVersion: 1,
		BootCount:     1,
		CreatedAt:     time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 10, 10, 12, 5, 0, 0, time.UTC),
		Records: []domain.PromptRecord{
			{
				Number: promptID,
				Title:  title,
				Hash:   "abc12345",
				Status: domain.StatusPending,
			},
		},
	}
}

func TestFileStateStore_SaveAndLoad_Basic(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStateStore(dir, nil)
	ctx := context.Background()

	// Initial load returns ErrNoState
	_, err := store.Load(ctx)
	if !errors.Is(err, application.ErrNoState) {
		t.Fatalf("expected ErrNoState, got %v", err)
	}

	// Save state S1
	s1 := sampleState(1, "First prompt")
	if err := store.Save(ctx, s1); err != nil {
		t.Fatalf("Save s1 failed: %v", err)
	}

	// Check file mode 0600 on state.json (AC 3)
	st, err := os.Stat(store.StateFilePath())
	if err != nil {
		t.Fatalf("stat state.json failed: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("expected state.json perm 0600, got %#o", perm)
	}

	// Verify file content has 2-space indentation
	raw, err := os.ReadFile(store.StateFilePath())
	if err != nil {
		t.Fatalf("read state.json failed: %v", err)
	}
	if !strings.Contains(string(raw), "  \"schema_version\": 1") {
		t.Errorf("expected 2-space indent in state.json, got:\n%s", string(raw))
	}

	// Load S1 back
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.SchemaVersion != 1 || len(loaded.Records) != 1 || loaded.Records[0].Title != "First prompt" {
		t.Errorf("unexpected loaded state: %+v", loaded)
	}

	// Save state S2, creating .bak of S1
	s2 := sampleState(2, "Second prompt")
	if err := store.Save(ctx, s2); err != nil {
		t.Fatalf("Save s2 failed: %v", err)
	}

	// Verify .bak exists and has perm 0600
	bakSt, err := os.Stat(store.BakFilePath())
	if err != nil {
		t.Fatalf("stat state.json.bak failed: %v", err)
	}
	if perm := bakSt.Mode().Perm(); perm != 0600 {
		t.Errorf("expected state.json.bak perm 0600, got %#o", perm)
	}

	// Load returns S2
	loaded2, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load s2 failed: %v", err)
	}
	if loaded2.Records[0].Title != "Second prompt" {
		t.Errorf("expected 'Second prompt', got %s", loaded2.Records[0].Title)
	}
}

func TestFileStateStore_FallbackToBackup_OnCorruptState(t *testing.T) {
	// AC 1: Test truncates state.json mid-JSON -> load returns .bak state + warning.
	dir := t.TempDir()
	evLog := &mockEventLog{}
	store := NewFileStateStore(dir, evLog)
	ctx := context.Background()

	s1 := sampleState(1, "Original prompt")
	if err := store.Save(ctx, s1); err != nil {
		t.Fatalf("Save s1 failed: %v", err)
	}

	s2 := sampleState(2, "Updated prompt")
	if err := store.Save(ctx, s2); err != nil {
		t.Fatalf("Save s2 failed: %v", err)
	}

	// Truncate state.json mid-JSON
	if err := os.WriteFile(store.StateFilePath(), []byte("{\"schema_version\": 1, \"rec"), 0600); err != nil {
		t.Fatalf("corrupting state.json failed: %v", err)
	}

	// Load should fall back to .bak (which holds S1)
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("expected fallback to .bak, but Load failed: %v", err)
	}
	if loaded.Records[0].Title != "Original prompt" {
		t.Errorf("expected prompt 'Original prompt' from .bak, got %s", loaded.Records[0].Title)
	}

	// Verify warning event was emitted
	events := evLog.Events()
	var foundFallbackEvent bool
	for _, e := range events {
		if e.Kind == "state.corrupt_fallback" && e.Level == "warn" {
			foundFallbackEvent = true
			break
		}
	}
	if !foundFallbackEvent {
		t.Errorf("expected state.corrupt_fallback warning event, got: %+v", events)
	}
}

// faultInjectFS wraps FS and fails at a specified operation index
type faultInjectFS struct {
	base        FS
	failAtOp    int
	currentOp   int
	mu          sync.Mutex
	injectedErr error
}

func newFaultInjectFS(failAtOp int) *faultInjectFS {
	return &faultInjectFS{
		base:        osFS{},
		failAtOp:    failAtOp,
		injectedErr: errors.New("injected I/O crash"),
	}
}

func (f *faultInjectFS) checkFail() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.currentOp++
	if f.currentOp == f.failAtOp {
		return f.injectedErr
	}
	return nil
}

func (f *faultInjectFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.WriteFile(name, data, perm)
}

func (f *faultInjectFS) SyncFile(name string) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.SyncFile(name)
}

func (f *faultInjectFS) Rename(oldpath, newpath string) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.Rename(oldpath, newpath)
}

func (f *faultInjectFS) Remove(name string) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.Remove(name)
}

func (f *faultInjectFS) Link(oldname, newname string) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.Link(oldname, newname)
}

func (f *faultInjectFS) CopyFile(src, dst string, perm os.FileMode) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.CopyFile(src, dst, perm)
}

func (f *faultInjectFS) SyncDir(dir string) error {
	if err := f.checkFail(); err != nil {
		return err
	}
	return f.base.SyncDir(dir)
}

func (f *faultInjectFS) ReadFile(name string) ([]byte, error) {
	return f.base.ReadFile(name)
}

func (f *faultInjectFS) Stat(name string) (os.FileInfo, error) {
	return f.base.Stat(name)
}

func TestFileStateStore_CrashInjectionAtEachStep(t *testing.T) {
	// AC 2: Crash-injection test (fault-injecting FS wrapper) at each step yields a loadable state.
	// Step sequence during Save with pre-existing state:
	// op 1: WriteFile(tmp)
	// op 2: SyncFile(tmp)
	// op 3: Remove(bakTmp)
	// op 4: Link(state, bakTmp)
	// op 5: Rename(bakTmp, bak)
	// op 6: Rename(tmp, state)
	// op 7: SyncDir(stateDir)

	for failOp := 1; failOp <= 7; failOp++ {
		t.Run("FailAtOp_"+string(rune('0'+failOp)), func(t *testing.T) {
			dir := t.TempDir()
			normalStore := NewFileStateStore(dir, nil)
			ctx := context.Background()

			s1 := sampleState(1, "Initial good state")
			if err := normalStore.Save(ctx, s1); err != nil {
				t.Fatalf("preparatory Save failed: %v", err)
			}

			// Now attempt second save with fault injection
			faultFS := newFaultInjectFS(failOp)
			crashStore := NewFileStateStoreWithFS(dir, nil, faultFS)

			s2 := sampleState(2, "Crashed state attempt")
			_ = crashStore.Save(ctx, s2) // error expected

			// Loading state must still succeed and yield either s1 or s2
			recoveredStore := NewFileStateStore(dir, nil)
			loaded, err := recoveredStore.Load(ctx)
			if err != nil {
				t.Fatalf("Load after crash at op %d failed: %v", failOp, err)
			}
			if loaded.Records[0].Title != "Initial good state" && loaded.Records[0].Title != "Crashed state attempt" {
				t.Errorf("recovered unexpected prompt state: %+v", loaded)
			}
		})
	}
}

func TestFileStateStore_SchemaVersionValidationAndMigration(t *testing.T) {
	// F-011 AC: Test: version 99 -> error mentioning "newer"; version 1 loads.
	dir := t.TempDir()
	store := NewFileStateStore(dir, nil)
	ctx := context.Background()

	// Write version 99 state
	v99JSON := `{"schema_version": 99, "records": []}`
	if err := os.WriteFile(store.StateFilePath(), []byte(v99JSON), 0600); err != nil {
		t.Fatalf("write v99 state failed: %v", err)
	}

	_, err := store.Load(ctx)
	if err == nil {
		t.Fatalf("expected error for schema version 99, got nil")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("expected error message to contain 'newer', got: %v", err)
	}

	// Write version 1 state: loads cleanly
	v1JSON := `{"schema_version": 1, "records": []}`
	if err := os.WriteFile(store.StateFilePath(), []byte(v1JSON), 0600); err != nil {
		t.Fatalf("write v1 state failed: %v", err)
	}
	s, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("expected version 1 to load cleanly, got: %v", err)
	}
	if s.SchemaVersion != 1 {
		t.Errorf("expected SchemaVersion 1, got %d", s.SchemaVersion)
	}

	// Test invalid version 0
	v0JSON := `{"schema_version": 0, "records": []}`
	if err := os.WriteFile(store.StateFilePath(), []byte(v0JSON), 0600); err != nil {
		t.Fatalf("write v0 state failed: %v", err)
	}
	_, err = store.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid state schema version") {
		t.Errorf("expected invalid schema version error, got: %v", err)
	}
}

func TestFileStateStore_MigrationHook(t *testing.T) {
	// Test migration hook when fromVersion < targetVersion
	defer ResetMigrationsForTest()

	// Register migration for version 1 to 2 that adds a custom field
	RegisterMigration(1, func(raw []byte) ([]byte, error) {
		s := string(raw)
		s = strings.Replace(s, `"schema_version": 1`, `"schema_version": 2, "migrated": true`, 1)
		return []byte(s), nil
	})

	data := []byte(`{"schema_version": 1, "records": []}`)
	migrated, err := Migrate(data, 1, 2)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if !strings.Contains(string(migrated), `"migrated": true`) {
		t.Errorf("expected migrated data to contain '\"migrated\": true', got: %s", string(migrated))
	}
}
