package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agyq/internal/application"
	"agyq/internal/domain"
)

// PreflightOptions parameterizes check execution so all checks can be tested with fakes.
type PreflightOptions struct {
	LookPath       func(file string) (string, error)
	ExecCmd        func(ctx context.Context, dir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
	ReadFile       func(name string) ([]byte, error)
	WriteProbeFile func(dir string) error
	SkipLiveProbe  bool
}

// DefaultPreflightOptions returns options wired to the real OS environment.
func DefaultPreflightOptions() PreflightOptions {
	return PreflightOptions{
		LookPath: exec.LookPath,
		ExecCmd: func(ctx context.Context, dir string, name string, args ...string) ([]byte, []byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stdout.Bytes(), stderr.Bytes(), err
		},
		ReadFile: os.ReadFile,
		WriteProbeFile: func(dir string) error {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			f, err := os.CreateTemp(dir, ".agyq-probe-*")
			if err != nil {
				return err
			}
			tmpName := f.Name()
			if _, err := f.Write([]byte("probe")); err != nil {
				_ = f.Close()
				_ = os.Remove(tmpName)
				return err
			}
			if err := f.Sync(); err != nil {
				_ = f.Close()
				_ = os.Remove(tmpName)
				return err
			}
			if err := f.Close(); err != nil {
				_ = os.Remove(tmpName)
				return err
			}
			return os.Remove(tmpName)
		},
	}
}

// PreflightService implements application.PreflightChecker.
type PreflightService struct {
	opts PreflightOptions
}

// NewPreflightService creates a preflight service with specified or default options.
func NewPreflightService(opts *PreflightOptions) *PreflightService {
	if opts == nil {
		def := DefaultPreflightOptions()
		return &PreflightService{opts: def}
	}
	// Fill in any nil funcs with defaults
	def := DefaultPreflightOptions()
	resolved := *opts
	if resolved.LookPath == nil {
		resolved.LookPath = def.LookPath
	}
	if resolved.ExecCmd == nil {
		resolved.ExecCmd = def.ExecCmd
	}
	if resolved.ReadFile == nil {
		resolved.ReadFile = def.ReadFile
	}
	if resolved.WriteProbeFile == nil {
		resolved.WriteProbeFile = def.WriteProbeFile
	}
	return &PreflightService{opts: resolved}
}

// Check executes all preflight checks against the supplied configuration.
func (p *PreflightService) Check(ctx context.Context, cfg *application.Config) (application.PreflightReport, error) {
	if cfg == nil {
		def := application.DefaultConfig()
		cfg = &def
	}

	var checks []application.CheckResult

	// 1. agy binary found
	binaryPath, err := p.opts.LookPath(cfg.Agy.Binary)
	if err != nil {
		checks = append(checks, application.CheckResult{
			Name:   "agy binary",
			Status: application.CheckFail,
			Detail: fmt.Sprintf("binary %q not found in PATH: %v", cfg.Agy.Binary, err),
		})
	} else {
		checks = append(checks, application.CheckResult{
			Name:   "agy binary",
			Status: application.CheckPass,
			Detail: binaryPath,
		})
	}

	// 2. agy --version parses
	if binaryPath != "" {
		stdout, stderr, err := p.opts.ExecCmd(ctx, "", binaryPath, "--version")
		if err != nil {
			checks = append(checks, application.CheckResult{
				Name:   "agy version",
				Status: application.CheckFail,
				Detail: fmt.Sprintf("failed running %s --version: %v (stderr: %s)", binaryPath, err, strings.TrimSpace(string(stderr))),
			})
		} else {
			ver := strings.TrimSpace(string(stdout))
			if ver == "" {
				checks = append(checks, application.CheckResult{
					Name:   "agy version",
					Status: application.CheckFail,
					Detail: "empty version output from agy --version",
				})
			} else {
				checks = append(checks, application.CheckResult{
					Name:   "agy version",
					Status: application.CheckPass,
					Detail: ver,
				})
			}
		}
	} else {
		checks = append(checks, application.CheckResult{
			Name:   "agy version",
			Status: application.CheckFail,
			Detail: "skipped because agy binary was not found",
		})
	}

	// 3. workdir is git repo (D-4)
	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = "."
	}
	absWorkDir, err := filepath.Abs(workDir)
	if err != nil {
		absWorkDir = workDir
	}
	gitOut, gitErr, err := p.opts.ExecCmd(ctx, absWorkDir, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(gitOut)) != "true" {
		detail := fmt.Sprintf("%q is not a git repository (D-4 requires git repo)", absWorkDir)
		if len(gitErr) > 0 {
			detail += fmt.Sprintf(": %s", strings.TrimSpace(string(gitErr)))
		}
		checks = append(checks, application.CheckResult{
			Name:   "git workdir",
			Status: application.CheckFail,
			Detail: detail,
		})
	} else {
		checks = append(checks, application.CheckResult{
			Name:   "git workdir",
			Status: application.CheckPass,
			Detail: absWorkDir,
		})
	}

	// 4. state dir writable
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = ".agyq"
	}
	absStateDir, err := filepath.Abs(stateDir)
	if err != nil {
		absStateDir = stateDir
	}
	if err := p.opts.WriteProbeFile(absStateDir); err != nil {
		checks = append(checks, application.CheckResult{
			Name:   "state dir writable",
			Status: application.CheckFail,
			Detail: fmt.Sprintf("%q not writable: %v", absStateDir, err),
		})
	} else {
		checks = append(checks, application.CheckResult{
			Name:   "state dir writable",
			Status: application.CheckPass,
			Detail: absStateDir,
		})
	}

	// 5. CPU has AES-NI (reads /proc/cpuinfo)
	cpuInfoData, err := p.opts.ReadFile("/proc/cpuinfo")
	if err != nil {
		checks = append(checks, application.CheckResult{
			Name:   "CPU AES-NI",
			Status: application.CheckFail,
			Detail: fmt.Sprintf("unable to read /proc/cpuinfo: %v", err),
		})
	} else {
		content := string(cpuInfoData)
		hasAES := false
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "flags") || strings.HasPrefix(line, "Features") {
				words := strings.Fields(line)
				for _, w := range words {
					if strings.EqualFold(w, "aes") || strings.EqualFold(w, "aes-ni") {
						hasAES = true
						break
					}
				}
				if hasAES {
					break
				}
			}
		}
		if hasAES {
			checks = append(checks, application.CheckResult{
				Name:   "CPU AES-NI",
				Status: application.CheckPass,
				Detail: "supported (AES flag present in /proc/cpuinfo)",
			})
		} else {
			checks = append(checks, application.CheckResult{
				Name:   "CPU AES-NI",
				Status: application.CheckFail,
				Detail: "AES-NI flag not found in /proc/cpuinfo (D-9: agy requires AES-NI, use host CPU type on Proxmox)",
			})
		}
	}

	// 6. Permission mode warning if dangerously-skip-permissions is active
	if cfg.Agy.PermissionMode == "dangerously-skip-permissions" {
		checks = append(checks, application.CheckResult{
			Name:   "permission mode",
			Status: application.CheckWarn,
			Detail: "dangerously-skip-permissions enabled (runs without sandbox confirmation)",
		})
	}

	// 7. tiny live 'say OK' print-mode probe within 120 s
	if p.opts.SkipLiveProbe {
		checks = append(checks, application.CheckResult{
			Name:   "live print probe",
			Status: application.CheckPass,
			Detail: "skipped (--skip-live)",
		})
	} else if binaryPath == "" {
		checks = append(checks, application.CheckResult{
			Name:   "live print probe",
			Status: application.CheckFail,
			Detail: "skipped because agy binary was not found",
		})
	} else {
		probeCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()

		args := []string{"-p", "Reply with exactly: OK", "--output-format", "json"}
		if cfg.Agy.Model != "" {
			args = append(args, "--model", cfg.Agy.Model)
		}
		if cfg.Agy.Effort != "" {
			args = append(args, "--effort", cfg.Agy.Effort)
		}
		if cfg.Agy.PermissionMode == "dangerously-skip-permissions" {
			args = append(args, "--dangerously-skip-permissions")
		}

		start := time.Now()
		stdout, stderr, err := p.opts.ExecCmd(probeCtx, absWorkDir, binaryPath, args...)
		elapsed := time.Since(start)

		if err != nil {
			checks = append(checks, application.CheckResult{
				Name:   "live print probe",
				Status: application.CheckFail,
				Detail: fmt.Sprintf("probe exited with error: %v (stderr: %s)", err, strings.TrimSpace(string(stderr))),
			})
		} else {
			var env struct {
				Status   string `json:"status"`
				Response string `json:"response"`
			}
			if jsonErr := json.Unmarshal(stdout, &env); jsonErr != nil {
				checks = append(checks, application.CheckResult{
					Name:   "live print probe",
					Status: application.CheckFail,
					Detail: fmt.Sprintf("invalid json response from agy: %v (raw stdout: %s)", jsonErr, strings.TrimSpace(string(stdout))),
				})
			} else if env.Status != "SUCCESS" {
				checks = append(checks, application.CheckResult{
					Name:   "live print probe",
					Status: application.CheckFail,
					Detail: fmt.Sprintf("envelope status is %q, expected \"SUCCESS\"", env.Status),
				})
			} else {
				resp := strings.TrimSpace(env.Response)
				checks = append(checks, application.CheckResult{
					Name:   "live print probe",
					Status: application.CheckPass,
					Detail: fmt.Sprintf("status: SUCCESS, response: %q (took %v)", resp, elapsed.Round(time.Millisecond)),
				})
			}
		}
	}

	// 8. configuration validation (F-065)
	if err := application.ValidateConfig(cfg); err != nil {
		checks = append(checks, application.CheckResult{
			Name:   "configuration",
			Status: application.CheckFail,
			Detail: fmt.Sprintf("invalid configuration: %v", err),
		})
	} else {
		checks = append(checks, application.CheckResult{
			Name:   "configuration",
			Status: application.CheckPass,
			Detail: "valid (policies, timeout, limits ok)",
		})
	}

	// 9. queue validation (F-065)
	queueFile := cfg.QueueFile
	if queueFile == "" {
		checks = append(checks, application.CheckResult{
			Name:   "queue validation",
			Status: application.CheckWarn,
			Detail: "no queue file configured",
		})
	} else {
		data, err := p.opts.ReadFile(queueFile)
		if err != nil {
			if os.IsNotExist(err) {
				checks = append(checks, application.CheckResult{
					Name:   "queue validation",
					Status: application.CheckWarn,
					Detail: fmt.Sprintf("%s not found (create before running)", queueFile),
				})
			} else {
				checks = append(checks, application.CheckResult{
					Name:   "queue validation",
					Status: application.CheckFail,
					Detail: fmt.Sprintf("failed to read %s: %v", queueFile, err),
				})
			}
		} else {
			prompts, err := domain.ParseQueueBytes(data)
			if err != nil {
				checks = append(checks, application.CheckResult{
					Name:   "queue validation",
					Status: application.CheckFail,
					Detail: fmt.Sprintf("syntax error in %s: %v", queueFile, err),
				})
			} else if valErr := domain.ValidatePrompts(prompts); valErr != nil {
				checks = append(checks, application.CheckResult{
					Name:   "queue validation",
					Status: application.CheckFail,
					Detail: fmt.Sprintf("invalid prompts in %s: %v", queueFile, valErr),
				})
			} else {
				checks = append(checks, application.CheckResult{
					Name:   "queue validation",
					Status: application.CheckPass,
					Detail: fmt.Sprintf("%s (%d prompts valid)", queueFile, len(prompts)),
				})
			}
		}
	}

	return application.PreflightReport{Checks: checks}, nil
}
