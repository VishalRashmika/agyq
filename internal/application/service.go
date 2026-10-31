package application

import (
	"fmt"
	"strings"
)

// ServiceUnitParams holds variables for generating a systemd service unit.
type ServiceUnitParams struct {
	User       string
	Group      string
	WorkDir    string
	Home       string
	Path       string
	Executable string
	ConfigPath string
}

// GenerateServiceUnit creates a systemd unit file content from params.
func GenerateServiceUnit(p ServiceUnitParams) string {
	if p.User == "" {
		p.User = "root"
	}
	if p.Group == "" {
		p.Group = p.User
	}
	if p.WorkDir == "" {
		p.WorkDir = "/var/lib/agyq"
	}
	if p.Executable == "" {
		p.Executable = "/usr/local/bin/agyq"
	}
	if p.ConfigPath == "" {
		p.ConfigPath = "/etc/agyq/agyq.json"
	}

	return fmt.Sprintf(`[Unit]
Description=agyq - sequential prompt queue runner for Antigravity CLI
Documentation=https://github.com/google/agyq
After=network-online.target time-sync.target
Wants=network-online.target time-sync.target

[Service]
Type=simple
User=%s
Group=%s
WorkingDirectory=%s
Environment="HOME=%s"
Environment="PATH=%s"
ExecStart=%s run --config %s
Restart=on-failure
RestartSec=5s
TimeoutStopSec=45
KillMode=mixed
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, p.User, p.Group, p.WorkDir, p.Home, p.Path, p.Executable, p.ConfigPath)
}

// BuildServicePath resolves an enriched PATH string containing standard agy locations.
func BuildServicePath(home string, existingPath string) string {
	seen := map[string]bool{}
	var parts []string

	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir != "" && !seen[dir] {
			seen[dir] = true
			parts = append(parts, dir)
		}
	}

	if home != "" {
		add(home + "/.local/bin")
		add(home + "/.gemini/antigravity-cli/bin")
	}
	add("/usr/local/bin")
	add("/usr/bin")
	add("/bin")

	for _, p := range strings.Split(existingPath, ":") {
		add(p)
	}

	return strings.Join(parts, ":")
}
