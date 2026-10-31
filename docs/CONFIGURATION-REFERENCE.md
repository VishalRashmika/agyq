# agyq Configuration Reference (`agyq.json`)

This document defines all configuration options supported by `agyq.json`.

---

## Example Configuration

```json
{
  "queue_file": "queue.md",
  "work_dir": ".",
  "state_dir": ".agyq",
  "prompt_timeout": "90m",
  "recovery": "resume",
  "on_failure": "stop",
  "agy": {
    "binary": "agy",
    "model": "gemini-3.8-flash-high",
    "effort": "high",
    "permission_mode": "dangerously-skip-permissions",
    "success_statuses": ["SUCCESS"],
    "use_pty": false
  },
  "retry": {
    "max_attempts": 3,
    "backoff": ["1m", "5m", "15m"]
  },
  "limits": {
    "patterns": [
      "quota",
      "rate limit",
      "usage limit",
      "resource[_ ]exhausted",
      "429",
      "limit reached",
      "exceeded your",
      "try again (in|after|at)"
    ],
    "default_wait": "1h",
    "max_wait": "24h"
  }
}
```

---

## Top-Level Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `queue_file` | string | `"queue.md"` | Path to the Markdown queue file containing prompts. Relative to `work_dir`. |
| `work_dir` | string | `"."` | Target Git repository root where the agent executes commands and creates commits. |
| `state_dir` | string | `".agyq"` | Directory storing `state.json`, `events.jsonl`, `transcripts/`, and control FIFO files. |
| `prompt_timeout` | string | `"90m"` | Maximum wall-clock execution time allowed for a single prompt before it is cancelled and marked timed out. Valid formats: `30m`, `1h30m`, `120s`. |
| `recovery` | string | `"resume"` | Crash recovery strategy: `"resume"` (re-attempts interrupted prompt with recovery context note) or `"fresh"` (starts fresh prompt). |
| `on_failure` | string | `"stop"` | Action when a prompt exhausts retries: `"stop"` (halts queue execution) or `"continue"` (skips to next prompt). |

---

## The `agy` Block (Antigravity CLI Invocation)

Controls how `agy` is called under the hood.

| Field | Type | Default | Description |
|---|---|---|---|
| `binary` | string | `"agy"` | Path or executable name for the Antigravity CLI binary. Auto-discovered if omitted. |
| `model` | string | `"gemini-3.8-flash-high"` | Gemini model used by the agent (`gemini-3.8-flash-high`, `gemini-3.8-pro`, `gemini-2.5-pro`). |
| `effort` | string | `"high"` | Thinking effort level (`"low"`, `"medium"`, `"high"`). |
| `permission_mode` | string | `"dangerously-skip-permissions"` | Headless tool approval mode: `"dangerously-skip-permissions"` or `"settings-allowlist"`. |
| `success_statuses` | string[] | `["SUCCESS"]` | List of JSON envelope status strings considered successful outcomes. |
| `use_pty` | boolean | `false` | When `true`, invokes `agy` attached to a pseudo-terminal device. |

### Permission Modes Explained

- **`dangerously-skip-permissions`**:
  Recommended for dedicated, sandboxed virtual machines (e.g. Proxmox LXC/VM). Passes `--dangerously-skip-permissions` to `agy`, auto-approving terminal commands and file writes so the agent can execute autonomously without human prompts.
- **`settings-allowlist`**:
  Recommended for shared machines. `agyq` will not pass the skip flag; `agy` reads pre-approved tool patterns from `~/.gemini/antigravity-cli/settings.json`.
- **False-Success Guard:**
  Regardless of permission mode, if `agy` auto-denies a tool and reports `denied_actions` in the JSON envelope, `agyq` immediately flags the prompt as failed (`permissions_denied`) rather than falsely completing.

---

## The `retry` Block (Transient Failure Handling)

| Field | Type | Default | Description |
|---|---|---|---|
| `max_attempts` | integer | `3` | Maximum execution attempts per prompt for recoverable or interrupted failures before marking failed. |
| `backoff` | string[] | `["1m", "5m", "15m"]` | List of sleep durations between retry attempts. |

---

## The `limits` Block (Quota & Rate Limit Resilience)

| Field | Type | Default | Description |
|---|---|---|---|
| `patterns` | string[] | *(built-in list)* | Regex patterns matched against `agy` output to detect quota, rate limit, or resource exhaustion errors. |
| `default_wait` | string | `"1h"` | Fallback duration to pause when quota is exhausted and no reset timestamp is provided. |
| `max_wait` | string | `"24h"` | Maximum cumulative wait time allowed across limit pause cycles. |

---

## Discovery & Precedence

1. **Explicit flag:** `--config <path>` always takes top precedence.
2. **Current working directory:** If `./agyq.json` exists, it is loaded.
3. **Repository root:** If `agyq.json` exists in the discovered Git root, it is loaded.
4. **Zero-Config fallback:** If no config file is found, `agyq` constructs safe defaults using the discovered Git root and resolved `agy` binary path.
