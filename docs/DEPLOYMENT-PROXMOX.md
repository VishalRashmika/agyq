# Deployment on Proxmox & Linux Servers — Operator Runbook

## 1. Where to run it

| Option | Verdict |
|--------|---------|
| **Debian/Ubuntu VM, CPU type `host`** | **Recommended.** Full systemd, easiest to reason about, `agy` native binary works if AES-NI is exposed. |
| Unprivileged LXC container | Works, lighter on RAM/power; the container shares the host CPU so AES-NI is available. Slightly fiddlier for `agy`'s sandbox/keyring behaviour. Choose only if you want minimum overhead. |
| Do **not** use CPU type `kvm64`/`qemu64` | `agy` crashes with *illegal instruction* (no AES-NI). |

Sizing: 2 vCPU, 2–4 GB RAM, 20 GB disk is plenty (agy + language server + your build tools: Go/Node/Python as your prompts need). The *prompts' build tooling must be installed in the guest* (compilers, package managers), because agy runs commands there.

## 2. Surviving a power cut with nobody awake

All three must be true, otherwise "resume after power cut" cannot happen:

1. **BIOS/UEFI:** ThinkCentre → *Power* → *After Power Loss* = **Power On** (wording varies: "Last State"/"Power On"). Without this the box stays off until someone presses the button.
2. **Proxmox:** guest → *Options* → *Start at boot* = **Yes** (optionally a start delay of 30 s so the network is ready).
3. **Guest:** `systemctl enable agyq` (unit in `deploy/agyq.service`).

Extra insurance (strongly advised): a small UPS rides through a 5-minute cut entirely — then there is no interruption at all and agyq's recovery path is only a safety net. Also use a journaling FS (ext4 default is fine) and keep state on the guest's disk, not tmpfs.

Time: ensure `systemd-timesyncd`/chrony is enabled. After a cut, the RTC may be off until NTP syncs; agyq waits for clock synchronization before scheduling.

## 3. One-time install (on the guest, as a normal user e.g. `agyq`)

```bash
# 1. Dedicated user + dirs
sudo adduser --disabled-password --gecos "" agyq
sudo mkdir -p /var/lib/agyq /etc/agyq && sudo chown agyq: /var/lib/agyq

# 2. Install agy as that user
sudo -iu agyq bash -c 'curl -fsSL https://antigravity.google/cli/install.sh | bash'

# 3. Log in ONCE interactively over SSH (prints a URL; open on your laptop, paste the code back)
sudo -iu agyq agy          # complete login, then /exit
#    Then confirm `sudo -iu agyq agy -p "Reply PONG"` works without re-login.
#    If the login does NOT persist across reboot: see §6 "Login Persistence".

# 4. Target repo (must be a git repo)
sudo -iu agyq bash -c 'mkdir -p ~/work/myproject && cd ~/work/myproject && git init && agyq init'

# 5. Install agyq (static binary)
sudo install -m 0755 agyq /usr/local/bin/agyq

# 6. Automatic service install
#    agyq service install auto-detects user, home, resolved PATH, and working directory:
cd ~agyq/work/myproject
sudo agyq service install --user agyq

# 7. Check, then enable
sudo -iu agyq agyq doctor
sudo systemctl enable --now agyq
```

## 4. Permissions for headless runs

Headless `agy` requires permission to execute shell commands and modify files. `agyq` defaults to:
- **`dangerously-skip-permissions` (Default):** Passes `--dangerously-skip-permissions` to `agy`, auto-approving terminal commands and file edits so the agent runs completely unattended without waiting for human confirmation. This is automatically set by `agyq init` and `DefaultConfig()`.
- **`settings-allowlist` (Optional for shared machines):** If preferred on non-isolated hosts, configure permissions explicitly in `~agyq/.gemini/antigravity-cli/settings.json` under `permissions.allow` and set `"permission_mode": "settings-allowlist"` in `agyq.json`.

> [!NOTE]
> Regardless of the mode, `agyq` includes a **False-Success Guard** that detects any tool denials (`denied_actions` or headless permission auto-denials) and immediately halts the prompt as a fatal failure rather than recording a false success.

The agent runs commands as user `agyq` on the guest — keep secrets out of that account, snapshot the VM before big queues.

## 5. Daily operation

```bash
# 1. Edit queue prompts (# #1 …, # #2 …)
vim ~agyq/work/myproject/queue.md

# 2. Validate + preview (zero-config, runs in repo directory)
agyq plan

# 3. Start or control queue
sudo systemctl start agyq        # or: sudo agyq service start
agyq status                      # from any SSH session
journalctl -u agyq -f            # live log; state in .agyq/ (events.jsonl, transcripts/)
agyq pause / agyq resume         # gracefully pause after active prompt completes
agyq skip N / agyq retry N       # skip prompt or clear retry counter

# 4. Starting a fresh batch
#    After reviewing and pushing completed code, reset for the next run:
agyq reset                       # archives previous state and transcripts to .agyq/archive/<timestamp>/
```

Morning review: `agyq status`, then `git -C ~agyq/work/myproject log --oneline` (checkpoint commits `agyq: checkpoint before #N` separate each prompt's work; tags `agyq/cp-N`).

You may append prompts to `queue.md` while it runs; edits to already-started prompts are flagged, not re-run.

## 6. Login Persistence

`agy` reads credentials from the OS keyring on desktops. On headless Linux without a Secret Service:

| Outcome | Action |
|---------|--------|
| Login survives reboot | Ready for unattended operation. |
| Login lost after reboot / `agy` re-prompts | Run a minimal D-Bus + `gnome-keyring-daemon` in the `agyq` user session with `loginctl enable-linger agyq`, or configure persistent file-based credential storage. |
| API key mode | Uses Gemini API key via environment variable `GEMINI_API_KEY`. |

## 7. Plan quota facts you should know

- Limits are per account: a rolling ~5-hour session window plus a weekly cap (exact values are Google's and may change). `agyq` can only wait; if you use `agy` interactively on your PC at the same time, you share the same quota and may trigger limits earlier.
- Heavy runs at "high" effort consume quota faster; the tool will happily pause at the limit and continue after reset.
