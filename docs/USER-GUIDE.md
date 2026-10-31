# agyq User Guide: Operating Autonomous Queues

This guide covers everything you need to know to author, execute, monitor, and maintain autonomous prompt queues using `agyq` and the **Antigravity CLI (`agy`)**.

---

## 1. Core Concepts & Operating Model

`agyq` is built for **unattended, crash-safe execution**. Instead of baby-sitting an AI agent or pasting prompts manually:

1. **One prompt at a time:** Each task runs in sequence, isolated from conversational drift.
2. **Fresh conversation per prompt:** Every prompt starts with a clean context window. State and architectural memory live in your repository files (code, tests, markdown docs), not in transient chat history.
3. **Automatic Git checkpoints:** Before executing each prompt `#N`, `agyq` creates a checkpoint commit tagged `agyq/cp-N`. If power fails or an attempt crashes, the next run knows exactly where work was interrupted and resumes cleanly.
4. **Crash and limit resilience:** Network hiccups, power outages, and API quota exhaustion are handled automatically with backoff, retry caps, and pause states.

---

## 2. Setting Up Your Target Repository

To get the highest quality results from an autonomous agent running overnight, structure your repository for autonomy:

### Repository Guidelines (`AGENTS.md` / `GEMINI.md`)
Create an `AGENTS.md` or `GEMINI.md` file in the root of your target repository. The Antigravity CLI auto-loads this file as project-level instructions for every prompt:

```markdown
# AGENTS.md — Instructions for Autonomous Coding Agent

1. Always run tests before finishing a prompt (`go test ./...` or `npm test`).
2. Follow existing project conventions and maintain documentation.
3. Keep commits clean and do not revert completed work from prior prompts.
4. If a task is blocked, document the reason clearly in project notes.
```

### Tracking Multi-Prompt Progress
For multi-step projects, maintain a progress or tracking markdown file (such as `ROADMAP.md` or `STATE.md`) in the repo. Have your prompts read and update this file so each autonomous session knows the overall project status.

---

## 3. Writing Prompts in `queue.md`

`queue.md` is your control file. It is a standard Markdown file containing level-1 headings with prompt numbers.

### Heading Syntax
A prompt heading must begin with `# #<number>`:
- `# #1 Set up database schema`
- `# #2: Implement user registration`
- `# #3 - Add password hashing`

Prompt numbers must be positive integers (`1..999999`) and strictly ascending.

### Anatomy of an Effective Prompt
Because each prompt starts a fresh agent session, prompts must be **self-contained** with explicit acceptance criteria:

```markdown
# #1 Implement JWT Authentication Service

Create an authentication service package at `internal/auth/`.

### Requirements:
1. Implement `GenerateToken(userID string, duration time.Duration) (string, error)`
2. Implement `ValidateToken(tokenString string) (*Claims, error)`
3. Use HMAC-SHA256 signing with secret loaded from `AUTH_SECRET` environment variable.
4. Unit tests covering valid tokens, expired tokens, and invalid signatures.

### Done When:
- `go test -v ./internal/auth/...` passes with >90% coverage.
- `go vet ./...` reports zero issues.
```

### Best Practices:
- **Include "Done When":** Explicit completion criteria (passing test commands, lint checks) drastically increase the success rate.
- **Do not rely on chat memory:** Never write "now refactor the code from earlier". Instead, write "refactor `internal/auth/token.go` to support refresh tokens".
- **Dynamic queue expansion:** You can append `# #4`, `# #5`, etc. to `queue.md` while `agyq` is actively running. It will pick up new items automatically on the next iteration.

---

## 4. Running the Queue

### Step 1: Initializing a Fresh Project (`agyq init`)
Inside your Git repository (or after running `git init`), initialize the project:
```bash
agyq init
```

This single command sets up everything required:
- **`queue.md`**: Starter prompt queue containing formatting guidelines and `# #1 Title` examples.
- **`agyq.json`**: Pre-configured with `"permission_mode": "dangerously-skip-permissions"`, ensuring that headless tool executions (file creation, edits, shell commands) run automatically without human prompts.
- **`AGENTS.md`**: Autonomous guidelines auto-loaded by `agy`, strictly instructing the agent to always modify files directly and test its work rather than outputting text-only explanations.
- **`.gitignore`**: Automatically adds `.agyq/` so runtime databases and logs are excluded from Git.
- **Preflight Diagnostics**: Verifies the `agy` binary, login credentials, and repository readiness.

*(Tip: If you want to start completely fresh in a project that previously had an agyq run, run `agyq reset` first).*

### Step 2: Previewing the Plan
Before executing, check that your queue is valid and inspect what will run:
```bash
agyq plan
```
Output:
```
#   STATUS   TITLE
#1  pending  Implement JWT Authentication Service
#2  pending  Add Rate Limiting Middleware

Plan: 2 prompts (2 pending)
```

### Step 3: Executing the Queue

Choose between running in the foreground or as a background service:

#### Option A: Running in the Foreground
Run directly in your terminal, inside a `tmux` session, or over SSH:
```bash
agyq run
```

#### Option B: Running as a Background Systemd Service
On a headless home server, Proxmox VM, or LXC container:
```bash
# Install the systemd unit (auto-detects user, working dir, and PATH)
sudo agyq service install

# Start the service
sudo agyq service start

# Monitor service status and logs
sudo agyq service status
journalctl -u agyq -f
```

---

## 5. Monitoring & Live Controls

### Status Overview
Check progress at any time from another terminal:
```bash
agyq status
```
Output:
```
#   STATUS     ATTEMPTS  LAST CHANGE              TITLE
#1  succeeded  1         2026-10-12 14:05:10 UTC  Implement JWT Authentication Service
#2  running    1         2026-10-12 14:15:32 UTC  Add Rate Limiting Middleware

Boot count: 1

Recent events:
  14:05:10 [info] prompt.succeeded #1: Prompt #1 succeeded (attempt 1)
  14:05:11 [info] checkpoint.created #2: Created git checkpoint agyq/cp-2 before prompt #2
  14:05:11 [info] prompt.started #2: Started prompt #2 (attempt 1)
```

### Inspecting Execution Transcripts
To review the exact raw input, model thinking, and tool output for any prompt:
```bash
cat .agyq/transcripts/0001-a1.log
```

### Queue Controls
- **Graceful Pause:**
  ```bash
  agyq pause
  ```
  Signals `agyq` to finish the currently active prompt and then pause before starting the next one.
- **Resume:**
  ```bash
  agyq resume
  ```
- **Skip a Prompt:**
  ```bash
  agyq skip 2
  ```
  Marks prompt #2 as skipped (cannot skip a currently running prompt).
- **Retry a Failed Prompt:**
  ```bash
  agyq retry 2
  ```
  Resets a failed prompt back to `pending` and clears its attempt count.

---

## 6. Reviewing Commits & Starting a New Batch (`agyq reset`)

### Reviewing Completed Work
When all prompts in the queue finish, `agyq` automatically creates a final checkpoint commit (`agyq: queue completed` tagged `agyq/completed`).

1. **Check Status:**
   ```bash
   agyq status
   ```
2. **Review Git Commits:**
   Inspect the checkpoints and commits made by the agent:
   ```bash
   # View commit history
   git log --oneline -n 15

   # Compare working tree against where the queue started:
   git diff agyq/cp-1 agyq/completed
   ```
3. **Run Your Project Tests:**
   Run full local test suites or builds (`npm test`, `go test ./...`, `pytest`).

---

### Starting a New Batch of Prompts (`agyq reset`)

Once you have verified and pushed the completed code, you will often want to start a new batch of prompts in `queue.md` numbered from `# #1` again.

> [!IMPORTANT]
> If your new `queue.md` starts prompt numbering over from `# #1`, you **must** run `agyq reset`. Otherwise, `agyq` reads its existing `state.json` and considers prompts `#1..#N` already completed!

#### Step-by-Step Reset Workflow:

1. **Stop the background service (if running):**
   ```bash
   sudo agyq service stop
   # (or: sudo systemctl stop agyq)
   ```

2. **Archive the previous run and reset state:**
   ```bash
   agyq reset
   ```
   **What happens during reset:**
   - Verifies that no prompt is actively executing (refuses to reset if a prompt is running, unless `--force` is passed).
   - Safely moves `state.json`, `state.json.bak`, `events.jsonl`, transcripts, and rotated logs into `.agyq/archive/<timestamp>/`.
   - Cleans up control files and creates a fresh state database ready for prompt `#1`.

3. **Update `queue.md` with your new prompts:**
   ```markdown
   # #1 Next Feature Title
   Task description and acceptance criteria...

   # #2 Another Task
   Task description...
   ```

4. **Verify the new plan:**
   ```bash
   agyq plan
   ```
   Verify that all prompts in the new `queue.md` show `pending`.

5. **Start the new run:**
   ```bash
   # In foreground:
   agyq run

   # OR via systemd daemon:
   sudo agyq service start
   ```

#### Appending vs. Resetting
- **Reset (`agyq reset`):** Use when you write a brand new `queue.md` starting numbering from `# #1`.
- **Append (No reset needed):** If your previous queue had prompts `#1..#12`, and you simply add `# #13`, `# #14`, etc. to the existing `queue.md`, you **do not** need to reset. `agyq` will keep `#1..#12` as completed and immediately pick up `#13` onward.
