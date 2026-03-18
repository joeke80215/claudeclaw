---
name: help
description: Show ClaudeClaw help and available commands. Use when users say "help", "claudeclaw help", "what can you do", "show commands", "available commands", "how to use claudeclaw".
---

# ClaudeClaw Help

Show available ClaudeClaw commands and usage information.

## Available Slash Commands

| Command | Description |
|---------|-------------|
| `/claudeclaw:start` | Start the ClaudeClaw daemon |
| `/claudeclaw:stop` | Stop the running daemon |
| `/claudeclaw:status` | Check daemon status |
| `/claudeclaw:config show` | Show current settings |
| `/claudeclaw:config heartbeat` | Configure heartbeat schedule |
| `/claudeclaw:config telegram` | Configure Telegram integration |
| `/claudeclaw:config discord` | Configure Discord integration |
| `/claudeclaw:config model` | Switch AI model |
| `/claudeclaw:config security` | Set security level |
| `/claudeclaw:config web` | Configure web dashboard |
| `/claudeclaw:jobs list` | List scheduled cron jobs |
| `/claudeclaw:jobs create` | Create a new cron job |
| `/claudeclaw:jobs delete` | Delete a cron job |
| `/claudeclaw:logs` | View execution logs |
| `/claudeclaw:help` | Show this help |

## CLI Commands

```bash
bun run src/index.ts start [--web]    # Start daemon
bun run src/index.ts status [--all]   # Check status
bun run src/index.ts send <prompt>    # Send one-shot prompt
bun run src/index.ts telegram         # Start Telegram bot
bun run src/index.ts discord          # Start Discord bot
```

## Key Concepts

- **Daemon**: Background process that runs heartbeats and cron jobs on schedule
- **Heartbeat**: Periodic check-in prompt sent to Claude Code
- **Cron Jobs**: Scheduled tasks defined as markdown files with YAML frontmatter
- **Security Levels**: `locked` > `strict` > `moderate` (default) > `unrestricted`
- **Web Dashboard**: Browser UI at `http://127.0.0.1:4632` for monitoring

## More Info

- Settings: `.claude/claudeclaw/settings.json`
- Jobs: `.claude/claudeclaw/jobs/*.md`
- Logs: `.claude/claudeclaw/logs/`
