# agyq Troubleshooting & Diagnostics Guide

This guide helps you diagnose and resolve common issues encountered while running `agyq`.

---

## 1. Quick Diagnostics (`agyq doctor`)

When anything behaves unexpectedly, run:

```bash
agyq doctor
```

This verifies:
1. **CPU Features:** Confirms AES-NI is exposed to the VM/host (required by `agy`'s runtime; VM CPU type must be `host`, not `kvm64`).
2. **Git Root:** Ensures the directory is a clean Git repository or has a valid recovery checkpoint.
3. **Binary Resolution:** Verifies `agy` is installed, executable, and on the PATH.
4. **Authentication Probe:** Sends a live test prompt to ensure Google credentials are valid.
5. **Disk & State:** Confirms `.agyq/` is writable and has free disk space.
6. **Queue Syntax:** Validates `queue.md` for duplicate IDs, missing titles, or syntax errors.

---

## 2. Common Scenarios & Resolutions

### Scenario A: Prompts Complete in 15 Seconds with No Changes
- **Symptom:** `agyq status` marks the prompt failed with `tool execution was denied: [command]`, or previously succeeded without doing any work.
- **Root Cause:** Headless `agy` auto-denies tool calls (running shell commands or modifying files) if permission is required and no human is present to approve it.
- **Fix:**
  In `agyq.json`, set:
  ```json
  "agy": {
    "permission_mode": "dangerously-skip-permissions"
  }
  ```
  *(Only use in dedicated VMs or containers without sensitive secrets.)*

---

### Scenario B: "Queue paused: usage limit reached"
- **Symptom:** `agyq status` shows:
  ```
  [warn] limits.paused #2: Quota or rate limit reached. Pausing until 18:30:00 UTC
  ```
- **Root Cause:** The Google AI Pro session window or weekly quota has been exceeded.
- **Behavior:** `agyq` automatically halts execution, sleeps until the window resets, and resumes prompt `#2` without losing context.
- **Action Needed:** None. The runner handles this unattended. If you wish to stop early, run `agyq pause` or cancel the service.

---

### Scenario C: System Crashed / Power Lost During Run
- **Symptom:** Server rebooted unexpectedly while prompt `#3` was executing.
- **Recovery:**
  1. Simply restart `agyq`:
     ```bash
     agyq run   # or sudo agyq service start
     ```
  2. `agyq` detects the interrupted prompt from `state.json`.
  3. It prepends an automated recovery prompt instructing the agent:
     > *"NOTE: A previous attempt at this task was interrupted. Inspect git status, git diff, and commits tagged agyq/cp-3, determine what was already completed, and finish only what remains."*
  4. The agent completes the remainder of the task without duplicating work.

---

### Scenario D: Systemd Service Won't Start
- **Symptom:** `systemctl start agyq` fails with `exit-code` or `status=203/EXEC`.
- **Diagnosis:**
  ```bash
  journalctl -u agyq -e --no-pager
  ```
- **Common causes:**
  1. **PATH mismatch:** Systemd runs in a minimal environment. Re-run `sudo agyq service install` so it can regenerate the unit with the correct user environment and PATH.
  2. **Permissions:** The service user cannot write to `/var/lib/agyq` or the project `.agyq/` folder. Ensure the service user owns the repo directory:
     ```bash
     sudo chown -R agyq:agyq ~/work/myproject
     ```

---

### Scenario E: Queue Validation Error (`Q-E1`, `Q-E2`)
- **Symptom:** `agyq plan` or `agyq run` exits with:
  ```
  queue.md: line 15: duplicate prompt number #2
  ```
  or
  ```
  queue.md: line 20: prompt #4 is out of order (previous was #5)
  ```
- **Fix:** Ensure prompt headers start with `# #N` (e.g. `# #1`, `# #2`), are strictly ascending, and have unique numbers.

---

## 3. Inspecting Transcripts and Logs

All raw output from the agent is saved for forensic review:

- **Prompt transcripts:** Located in `.agyq/transcripts/`:
  - `0001-a1.log` (Prompt #1, attempt 1)
  - `0001-a2.log` (Prompt #1, attempt 2)
- **Structured event log:** `.agyq/events.jsonl` contains machine-readable timeline events:
  ```bash
  cat .agyq/events.jsonl | tail -n 10
  ```
- **Live daemon log:**
  ```bash
  journalctl -u agyq -f
  ```
