---
name: status
description: Check ClaudeClaw daemon status. Use when users say "status", "is claudeclaw running", "check daemon", "daemon status", "show status", "claudeclaw status".
---

# ClaudeClaw Status

Show the current status of the ClaudeClaw daemon.

## Steps

1. Check if the daemon is running:
   ```bash
   cat .claude/claudeclaw/daemon.pid 2>/dev/null && kill -0 $(cat .claude/claudeclaw/daemon.pid) 2>/dev/null && echo "running" || echo "not running"
   ```

2. If running, read and display settings:
   ```bash
   cat .claude/claudeclaw/settings.json
   ```

3. Show heartbeat status and next scheduled times:
   ```bash
   cat .claude/claudeclaw/state.json
   ```

4. List active cron jobs:
   ```bash
   ls .claude/claudeclaw/jobs/*.md 2>/dev/null
   ```

5. If `$ARGUMENTS` contains `--all`, also scan for daemons in other projects by checking `~/.claude/projects/*/` for PID files.

6. Present a concise summary to the user with: daemon state (running/stopped), PID, heartbeat config, job count, and next scheduled events.
