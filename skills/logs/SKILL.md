---
name: logs
description: View ClaudeClaw execution logs. Use when users say "logs", "show logs", "view logs", "check logs", "daemon logs", "execution history", "claudeclaw logs".
---

# ClaudeClaw Logs

View ClaudeClaw daemon execution logs. Use `$ARGUMENTS` for filtering options.

## Steps

1. List available log files:
   ```bash
   ls -lt .claude/claudeclaw/logs/ 2>/dev/null
   ```

2. If `$ARGUMENTS` specifies a log type or date, filter accordingly.

3. By default, show the most recent log entries:
   ```bash
   tail -100 .claude/claudeclaw/logs/daemon.log
   ```

4. Present logs in a readable format, highlighting:
   - Errors and warnings
   - Heartbeat executions
   - Cron job executions
   - Rate limiting events
   - Session information

5. If the user asks for a specific job's logs, grep for that job name in the log files.
