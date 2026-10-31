package infrastructure

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agyq/internal/application"
)

func TestPreflight_AllPassWithFakes(t *testing.T) {
	opts := PreflightOptions{
		LookPath: func(file string) (string, error) {
			return "/fake/bin/agy", nil
		},
		ExecCmd: func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("1.3.3\n"), nil, nil
			}
			if len(args) >= 2 && args[0] == "rev-parse" {
				return []byte("true\n"), nil, nil
			}
			// live probe
			if len(args) >= 2 && args[0] == "-p" {
				return []byte(`{"status":"SUCCESS","response":"OK"}`), nil, nil
			}
			return nil, nil, nil
		},
		ReadFile: func(name string) ([]byte, error) {
			if name == "/proc/cpuinfo" {
				return []byte("flags : fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush mmx fxsr sse sse2 ss ht syscall nx mmxext fxsr_opt pdpe1gb rdtscp lm constant_tsc rep_good nopl nonstop_tsc cpuid extd_apicid aperfmperf rapl pni pclmulqdq monitor ssse3 fma cx16 sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand hypervisor lahf_lm cmp_legacy svm extapic cr8_legacy abm sse4a misalignsse 3dnowprefetch osvw topoext perfctr_core perfctr_nb bpext perfctr_llc mwaitx cpb cat_l3 cdp_l3 hw_pstate ssbd mba ibrs ibpb stibp vmmcall fsgsbase bmi1 avx2 smep bmi2 erms invpcid cqm rdt_a rdseed adx smap clflushopt clwb sha_ni xsaveopt xsavec xgetbv1 xsaves cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local user_shstk clzero irperf xsaveerptr rdpru wbnoinvd arat npt lbrv svm_lock nrip_save tsc_scale vmcb_clean flushbyasid decodeassists pausefilter pfthreshold avic v_vmsave_vmload vgif v_spec_ctrl vnmi umip pku ospke vaes vpclmulqdq rdpid overflow_recov succor\n"), nil
			}
			if name == "/fake/queue.md" {
				return []byte("# #1 Test\n\nPrompt body here\n"), nil
			}
			return nil, errors.New("not found")
		},
		WriteProbeFile: func(dir string) error {
			return nil
		},
	}

	svc := NewPreflightService(&opts)
	cfg := application.DefaultConfig()
	cfg.WorkDir = "/fake/repo"
	cfg.StateDir = "/fake/state"
	cfg.QueueFile = "/fake/queue.md"

	report, err := svc.Check(context.Background(), &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !report.AllPassed() {
		for _, c := range report.Checks {
			t.Errorf("check %s: %s (%s)", c.Name, c.Status, c.Detail)
		}
		t.Fatalf("expected all checks to pass")
	}

	// Verify we checked binary, version, git, state, aes, live probe, configuration, queue validation
	names := map[string]application.CheckStatus{}
	for _, c := range report.Checks {
		names[c.Name] = c.Status
	}

	expected := []string{"agy binary", "agy version", "git workdir", "state dir writable", "CPU AES-NI", "live print probe", "configuration", "queue validation"}
	for _, exp := range expected {
		if status, ok := names[exp]; !ok || status != application.CheckPass {
			t.Errorf("check %q: got status %v, want PASS", exp, status)
		}
	}
}

func TestPreflight_Failures(t *testing.T) {
	tests := []struct {
		name            string
		modifyOpts      func(opts *PreflightOptions)
		modifyCfg       func(cfg *application.Config)
		wantFailedCheck string
	}{
		{
			name: "agy binary not found",
			modifyOpts: func(opts *PreflightOptions) {
				opts.LookPath = func(file string) (string, error) {
					return "", errors.New("not found in path")
				}
			},
			wantFailedCheck: "agy binary",
		},
		{
			name: "invalid configuration policy",
			modifyCfg: func(cfg *application.Config) {
				cfg.Recovery = "unknown-recovery"
			},
			wantFailedCheck: "configuration",
		},
		{
			name: "queue validation duplicate prompt",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ReadFile = func(name string) ([]byte, error) {
					if name == "/fake/queue.md" {
						return []byte("# #1 First\n\nBody\n\n# #1 Duplicate\n\nBody\n"), nil
					}
					if name == "/proc/cpuinfo" {
						return []byte("flags : aes\n"), nil
					}
					return nil, errors.New("not found")
				}
			},
			modifyCfg: func(cfg *application.Config) {
				cfg.QueueFile = "/fake/queue.md"
			},
			wantFailedCheck: "queue validation",
		},
		{
			name: "queue validation empty prompt body",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ReadFile = func(name string) ([]byte, error) {
					if name == "/fake/queue.md" {
						return []byte("# #1 First\n\n"), nil
					}
					if name == "/proc/cpuinfo" {
						return []byte("flags : aes\n"), nil
					}
					return nil, errors.New("not found")
				}
			},
			modifyCfg: func(cfg *application.Config) {
				cfg.QueueFile = "/fake/queue.md"
			},
			wantFailedCheck: "queue validation",
		},
		{
			name: "agy version exit non-zero",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ExecCmd = func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
					if len(args) == 1 && args[0] == "--version" {
						return nil, []byte("fatal error"), errors.New("exit 1")
					}
					return []byte("true\n"), nil, nil
				}
			},
			wantFailedCheck: "agy version",
		},
		{
			name: "git workdir not a repo",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ExecCmd = func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
					if len(args) >= 2 && args[0] == "rev-parse" {
						return []byte("fatal: not a git repository\n"), nil, errors.New("exit 128")
					}
					return []byte("1.3.3\n"), nil, nil
				}
			},
			wantFailedCheck: "git workdir",
		},
		{
			name: "state dir not writable",
			modifyOpts: func(opts *PreflightOptions) {
				opts.WriteProbeFile = func(dir string) error {
					return errors.New("permission denied")
				}
			},
			wantFailedCheck: "state dir writable",
		},
		{
			name: "CPU missing AES-NI",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ReadFile = func(name string) ([]byte, error) {
					return []byte("flags : fpu vme de sse sse2\n"), nil
				}
			},
			wantFailedCheck: "CPU AES-NI",
		},
		{
			name: "live probe returns non-SUCCESS status",
			modifyOpts: func(opts *PreflightOptions) {
				opts.ExecCmd = func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
					if len(args) == 1 && args[0] == "--version" {
						return []byte("1.3.3\n"), nil, nil
					}
					if len(args) >= 2 && args[0] == "rev-parse" {
						return []byte("true\n"), nil, nil
					}
					if len(args) >= 2 && args[0] == "-p" {
						return []byte(`{"status":"FAILED","response":"Quota exceeded"}`), nil, nil
					}
					return nil, nil, nil
				}
			},
			wantFailedCheck: "live print probe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := PreflightOptions{
				LookPath: func(file string) (string, error) {
					return "/fake/bin/agy", nil
				},
				ExecCmd: func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
					if len(args) == 1 && args[0] == "--version" {
						return []byte("1.3.3\n"), nil, nil
					}
					if len(args) >= 2 && args[0] == "rev-parse" {
						return []byte("true\n"), nil, nil
					}
					if len(args) >= 2 && args[0] == "-p" {
						return []byte(`{"status":"SUCCESS","response":"OK"}`), nil, nil
					}
					return nil, nil, nil
				},
				ReadFile: func(name string) ([]byte, error) {
					return []byte("flags : sse sse2 aes\n"), nil
				},
				WriteProbeFile: func(dir string) error {
					return nil
				},
			}

			if tc.modifyOpts != nil {
				tc.modifyOpts(&opts)
			}

			cfg := application.DefaultConfig()
			if tc.modifyCfg != nil {
				tc.modifyCfg(&cfg)
			}

			svc := NewPreflightService(&opts)
			report, err := svc.Check(context.Background(), &cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if report.AllPassed() {
				t.Fatalf("expected report.AllPassed() == false for failure scenario %s", tc.name)
			}

			found := false
			for _, c := range report.Checks {
				if c.Name == tc.wantFailedCheck && c.Status == application.CheckFail {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected check %q to FAIL, checks were: %+v", tc.wantFailedCheck, report.Checks)
			}
		})
	}
}

func TestPreflight_PermissionModeWarning(t *testing.T) {
	opts := PreflightOptions{
		LookPath: func(file string) (string, error) { return "/fake/agy", nil },
		ExecCmd: func(ctx context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("1.3.3\n"), nil, nil
			}
			if len(args) >= 2 && args[0] == "rev-parse" {
				return []byte("true\n"), nil, nil
			}
			return []byte(`{"status":"SUCCESS","response":"OK"}`), nil, nil
		},
		ReadFile:       func(name string) ([]byte, error) { return []byte("flags : aes\n"), nil },
		WriteProbeFile: func(dir string) error { return nil },
	}

	svc := NewPreflightService(&opts)
	cfg := application.DefaultConfig()
	cfg.Agy.PermissionMode = "dangerously-skip-permissions"

	report, err := svc.Check(context.Background(), &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundWarn bool
	for _, c := range report.Checks {
		if c.Name == "permission mode" && c.Status == application.CheckWarn {
			foundWarn = true
			if !strings.Contains(c.Detail, "dangerously-skip-permissions") {
				t.Errorf("unexpected detail: %s", c.Detail)
			}
		}
	}
	if !foundWarn {
		t.Error("expected WARN check for dangerously-skip-permissions")
	}
	// Warnings should not fail preflight
	if !report.AllPassed() {
		t.Error("report with only WARN should still report AllPassed() == true")
	}
}
