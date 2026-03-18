---
name: start
description: Start the ClaudeClaw daemon. Use when users say "start claudeclaw", "launch daemon", "boot up", "run claudeclaw", "turn on claudeclaw", "claudeclaw start".
---

# Start ClaudeClaw Daemon

Start the ClaudeClaw daemon process with optional web dashboard.

## Steps

1. Check if a daemon is already running by reading `.claude/claudeclaw/daemon.pid` and verifying the process is alive. If running, inform the user and exit.

2. Ensure the runtime directory exists:
   ```bash
   mkdir -p .claude/claudeclaw/logs .claude/claudeclaw/jobs .claude/claudeclaw/sessions
   ```

3. Start the daemon. Use `$ARGUMENTS` to check for flags like `--web`:
   ```bash
   nohup ./claudeclaw-bin start $ARGUMENTS > .claude/claudeclaw/logs/daemon.log 2>&1 &
   ```

4. Verify the daemon started by checking that `.claude/claudeclaw/daemon.pid` was created.

5. Report the daemon PID and status to the user.
