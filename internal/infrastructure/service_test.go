package infrastructure

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnit_F070(t *testing.T) {
	// Look up deploy/agyq.service from repo root
	// Since tests run in internal/infrastructure, go up two levels
	unitPath := filepath.Join("..", "..", "deploy", "agyq.service")
	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("failed to read deploy/agyq.service: %v", err)
	}

	content := string(data)

	// Check required directives per F-070 specification
	requiredDirectives := []string{
		"Restart=on-failure",
		"WantedBy=multi-user.target",
		"User=agyq",
		"TimeoutStopSec=45",
		"network-online.target",
		"time-sync.target",
	}

	for _, d := range requiredDirectives {
		if !strings.Contains(content, d) {
			t.Errorf("deploy/agyq.service missing required directive or target: %q", d)
		}
	}

	// Verify using systemd-analyze if installed
	sysAnalyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Logf("systemd-analyze not installed; skipping verification")
		return
	}

	tempRoot := t.TempDir()
	binDir := filepath.Join(tempRoot, "usr", "local", "bin")
	serviceDir := filepath.Join(tempRoot, "etc", "systemd", "system")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create temp bin dir: %v", err)
	}
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		t.Fatalf("failed to create temp service dir: %v", err)
	}

	// Create dummy binary in temp root
	dummyBin := filepath.Join(binDir, "agyq")
	if err := os.WriteFile(dummyBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to write dummy binary: %v", err)
	}

	// Copy service file to temp root
	tempUnit := filepath.Join(serviceDir, "agyq.service")
	if err := os.WriteFile(tempUnit, data, 0644); err != nil {
		t.Fatalf("failed to write temp unit file: %v", err)
	}

	cmd := exec.Command(sysAnalyze, "--root="+tempRoot, "verify", tempUnit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("systemd-analyze verify failed: %v\nOutput: %s", err, string(out))
	}
}
