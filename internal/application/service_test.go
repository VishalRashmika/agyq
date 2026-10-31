package application

import (
	"strings"
	"testing"
)

func TestGenerateServiceUnit(t *testing.T) {
	unit := GenerateServiceUnit(ServiceUnitParams{
		User:       "testuser",
		Group:      "testuser",
		WorkDir:    "/home/testuser/work",
		Home:       "/home/testuser",
		Path:       "/home/testuser/.local/bin:/usr/bin",
		Executable: "/usr/local/bin/agyq",
		ConfigPath: "/etc/agyq/agyq.json",
	})

	expectedSubstrings := []string{
		"[Unit]",
		"Description=agyq - sequential prompt queue runner",
		"After=network-online.target time-sync.target",
		"[Service]",
		"User=testuser",
		"Group=testuser",
		"WorkingDirectory=/home/testuser/work",
		`Environment="HOME=/home/testuser"`,
		`Environment="PATH=/home/testuser/.local/bin:/usr/bin"`,
		"ExecStart=/usr/local/bin/agyq run --config /etc/agyq/agyq.json",
		"Restart=on-failure",
		"RestartSec=5s",
		"TimeoutStopSec=45",
		"KillMode=mixed",
		"[Install]",
		"WantedBy=multi-user.target",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(unit, sub) {
			t.Errorf("generated unit missing expected snippet %q:\n%s", sub, unit)
		}
	}
}

func TestBuildServicePath(t *testing.T) {
	p := BuildServicePath("/home/myuser", "/usr/bin:/bin:/custom/bin")

	expectedParts := []string{
		"/home/myuser/.local/bin",
		"/home/myuser/.gemini/antigravity-cli/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/custom/bin",
	}

	for _, part := range expectedParts {
		if !strings.Contains(p, part) {
			t.Errorf("service path missing expected directory %q:\n%s", part, p)
		}
	}

	// Verify no duplicates
	parts := strings.Split(p, ":")
	seen := map[string]bool{}
	for _, part := range parts {
		if seen[part] {
			t.Errorf("duplicate entry %q in service path: %s", part, p)
		}
		seen[part] = true
	}
}
