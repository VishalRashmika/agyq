# agyq CLI Reference Manual

Complete reference manual for the `agyq` command-line interface.

---

## Command Overview

```
agyq [command] [flags]
```

| Command | Summary |
|---------|---------|
| [`init`](#agyq-init) | Scaffold starter files and verify preflight in current repository |
| [`plan`](#agyq-plan) | Parse and validate `queue.md`, display execution order |
| [`run`](#agyq-run) | Start sequential queue execution |
| [`status`](#agyq-status) | Show current state, active prompt, attempt counts, and event stream |
| [`doctor`](#agyq-doctor) | Run comprehensive 9-step preflight diagnostic |
| [`pause`](#agyq-pause) | Request queue runner to pause gracefully after current prompt |
| [`resume`](#agyq-resume) | Resume a paused queue runner |
| [`skip <N>`](#agyq-skip) | Mark prompt `#N` as skipped |
| [`retry <N>`](#agyq-retry) | Reset failed prompt `#N` to pending and clear attempt counter |
| [`reset`](#agyq-reset) | Archive completed batch and clear state for next run |
| [`service`](#agyq-service) | Manage systemd service units (`install`, `status`, `start`, `stop`) |
| [`version`](#agyq-version) | Print binary version and git commit |
| [`help`](#agyq-help) | Display help and usage information |

---

## Global Flags

The following flags can be passed to commands that read configuration (`plan`, `run`, `status`, `doctor`, `pause`, `resume`, `skip`, `retry`, `reset`):

- `--config <path>`, `-c <path>`: Explicit path to the configuration file (defaults to `./agyq.json` or zero-config repository discovery).

---

## Command Reference

### `agyq init`
Initializes a new `agyq` project in the current git repository.

```bash
agyq init [--user <name>] [--no-doctor]
```

- **Flags:**
  - `--user <name>`: Custom system username for service definitions.
  - `--no-doctor`: Skip preflight doctor checks during initialization.
- **Actions:**
  - Creates starter `queue.md` with prompt examples if not present.
  - Creates default `agyq.json` with `"permission_mode": "dangerously-skip-permissions"` if not present.
  - Creates `AGENTS.md` with autonomous agent guidelines (ensuring code modifications rather than advice) if not present.
  - Appends `.agyq/` to `.gitignore` if not present.
  - Runs all 9 preflight doctor checks.
- **Exit Codes:** `0` (Success), `2` (Not a git repo or invalid flag), `3` (Doctor check failed).

---

### `agyq plan`
Parses and validates the queue file without launching the `agy` CLI.

```bash
agyq plan [--config <path>] [--json]
```

- **Flags:**
  - `--json`: Output execution plan as machine-readable JSON.
- **Output:** Table of prompts, assigned sequence numbers, current statuses, and titles.
- **Exit Codes:** `0` (Plan valid), `1` (Parsing or validation error, e.g. duplicate prompt IDs).

---

### `agyq run`
Runs pending prompts sequentially until the queue completes, an unrecoverable failure occurs, or a pause is requested.

```bash
agyq run [--config <path>] [--pty]
```

- **Flags:**
  - `--pty`: Allocate pseudo-terminal when invoking `agy` (useful if interactive coloring is required).
- **Behavior:**
  - Checks git repository status.
  - Creates git checkpoint `agyq/cp-N` before each prompt.
  - Invokes `agy` headless print mode with configured model, effort, and permission flags.
  - Intercepts rate limits and quota resets, sleeping until quota is restored.
  - Retries transient errors up to `max_attempts`.
- **Exit Codes:** `0` (Queue complete or clean stop), `1` (Fatal run failure).

---

### `agyq status`
Displays the real-time execution state of the queue.

```bash
agyq status [--config <path>] [--json]
```

- **Flags:**
  - `--json`: Output raw `RunState` and recent events in JSON format.
- **Output:** Table showing prompt IDs, statuses (`pending`, `running`, `succeeded`, `failed`, `interrupted`, `skipped`), attempt counts, last change timestamp, and recent event stream.

---

### `agyq doctor`
Runs the 9-point preflight diagnostic suite to ensure unattended runs will succeed.

```bash
agyq doctor [--config <path>] [--no-live]
```

- **Checks performed:**
  1. Operating system and CPU instruction set (AES-NI check).
  2. Git binary availability and valid work repository.
  3. Working directory clean or valid checkpoint presence.
  4. Antigravity CLI binary resolution and executable permissions.
  5. Antigravity version compatibility.
  6. Live headless authentication probe (`agy -p "Reply with exactly: OK"`).
  7. State directory write permissions and disk space.
  8. Queue file syntax and prompt parse validation.
  9. Permission mode validation (warns on dangerous modes).
- **Flags:**
  - `--no-live`: Skip the live model API probe (check #6).

---

### `agyq pause`
Signals the running queue daemon to pause after the currently executing prompt completes.

```bash
agyq pause [--config <path>]
```

- **Mechanism:** Creates `.agyq/pause.signal` control file.

---

### `agyq resume`
Clears the pause signal and allows a paused queue runner to proceed.

```bash
agyq resume [--config <path>]
```

- **Mechanism:** Removes `.agyq/pause.signal` and notifies runner.

---

### `agyq skip <N>`
Marks prompt number `N` as `skipped`.

```bash
agyq skip <N> [--config <path>]
```

- **Restrictions:** Prompt must be currently `pending` or `interrupted`. A currently `running` prompt cannot be skipped.

---

### `agyq retry <N>`
Resets a `failed` prompt back to `pending` and clears its attempt counter so the runner can try again.

```bash
agyq retry <N> [--config <path>]
```

- **Restrictions:** Prompt must have status `failed`.

---

### `agyq reset`
Archives the current queue state, events, and transcripts, resetting state to clean for the next batch.

```bash
agyq reset [--force] [--no-archive]
```

- **Flags:**
  - `--force`: Force reset even if a prompt is currently marked `running`.
  - `--no-archive`: Discard state files instead of archiving to `.agyq/archive/<timestamp>/`.
- **Archived files:**
  - `state.json`
  - `state.json.bak`
  - `events.jsonl`
  - `transcripts/*.log`
  - Control FIFO signals

---

### `agyq service`
Helper to configure and control systemd service units for unattended daemon runs.

```bash
agyq service <subcommand> [options]
```

#### `agyq service install`
Generates and writes a systemd service unit to `/etc/systemd/system/<unit-name>.service`.

```bash
sudo agyq service install [--user <name>] [--unit-name <name>] [--config <path>] [--dry-run]
```

- **Flags:**
  - `--dry-run`: Output generated unit configuration to stdout without writing file.
  - `--user <name>`: System user account (defaults to current user).
  - `--unit-name <name>`: Service name (defaults to `agyq`).
  - `--config <path>`: Explicit config path (defaults to discovered config).

#### `agyq service status [unit-name]`
Executes `systemctl status <unit-name>`.

#### `agyq service start [unit-name]`
Executes `systemctl start <unit-name>`.

#### `agyq service stop [unit-name]`
Executes `systemctl stop <unit-name>`.

---

### `agyq version`
Prints the compiled version, Git commit hash, and target architecture.

```bash
agyq version
```

---

## Exit Codes

| Code | Meaning |
|---|---|
| `0` | Success / Clean normal exit |
| `1` | Runtime error / Prompt fatal failure |
| `2` | Command usage error / Invalid flag or syntax |
| `3` | Preflight check failure (`doctor` or `init`) |
| `4` | Interrupted by signal (SIGINT / SIGTERM) |
