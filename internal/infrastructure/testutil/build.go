package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	builtBinary string
	buildOnce   sync.Once
	buildErr    error
)

// BuildFakeAgy compiles the fakeagy test double binary once and returns its path.
func BuildFakeAgy(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "fakeagy-build-*")
		if err != nil {
			buildErr = err
			return
		}
		binPath := filepath.Join(tmpDir, "fakeagy")
		cmd := exec.Command("go", "build", "-o", binPath, "agyq/internal/infrastructure/testutil/fakeagy")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("build fakeagy failed: %s", string(out))
			return
		}
		builtBinary = binPath
	})

	if buildErr != nil {
		t.Fatalf("failed to build fakeagy: %v", buildErr)
	}
	return builtBinary
}
