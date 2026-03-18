---
name: stop
description: Stop the ClaudeClaw daemon. Use when users say "stop claudeclaw", "stop daemon", "shut down", "kill daemon", "turn off claudeclaw", "claudeclaw stop".
---

# Stop ClaudeClaw Daemon

Stop the running ClaudeClaw daemon process.

## Steps

1. Read the PID file at `.claude/claudeclaw/daemon.pid`
2. If no PID file exists, inform the user that no daemon is running
3. Send SIGTERM to the process:
   ```bash
   kill $(cat .claude/claudeclaw/daemon.pid)
   ```
4. Clean up runtime files:
   ```bash
   rm -f .claude/claudeclaw/daemon.pid .claude/claudeclaw/state.json
   ```
5. Remove the statusline configuration:
   - Delete `.claude/statusline.cjs` if it exists
   - Remove the `statusLine` key from `.claude/settings.json` if present
6. Confirm to the user that the daemon has been stopped
