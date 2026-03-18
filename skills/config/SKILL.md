---
name: config
description: Configure ClaudeClaw settings. Use when users say "config", "settings", "configure claudeclaw", "set heartbeat", "set telegram", "change model", "config show", "config heartbeat", "config telegram", "config model", "config discord", "config security", "claudeclaw config".
---

# ClaudeClaw Configuration

Manage ClaudeClaw daemon settings. Use `$ARGUMENTS` to determine the subcommand.

## Subcommands

### `show` (default if no arguments)
Read and display the current settings:
```bash
cat .claude/claudeclaw/settings.json
```
Present settings in a readable format covering: model, timezone, heartbeat, telegram, discord, security level, web UI, and STT config.

### `heartbeat`
Configure the heartbeat schedule. Ask the user for:
- **enabled**: true/false
- **interval**: minutes between heartbeats (default: 15)
- **excludeWindows**: quiet hours (e.g., `[{"start": "23:00", "end": "07:00"}]`)
- **forwardToTelegram**: whether to send heartbeat results to Telegram

Update `.claude/claudeclaw/settings.json` with the new heartbeat config.

### `telegram`
Configure Telegram integration. Ask the user for:
- **token**: Telegram bot token from @BotFather
- **allowedUserIds**: array of allowed Telegram user IDs

Update `.claude/claudeclaw/settings.json` with the new telegram config.

### `discord`
Configure Discord integration. Ask the user for:
- **token**: Discord bot token
- **allowedUserIds**: array of allowed Discord user IDs
- **listenChannels**: array of channel IDs to listen in

Update `.claude/claudeclaw/settings.json` with the new discord config.

### `model`
Switch the active model. Ask the user which model to use, then update the `model` field in `.claude/claudeclaw/settings.json`.

### `security`
Configure security level. Levels: `locked`, `strict`, `moderate` (default), `unrestricted`. Update the `security.level` field in `.claude/claudeclaw/settings.json`.

### `web`
Configure the web dashboard. Fields: `enabled` (bool), `host` (default `127.0.0.1`), `port` (default `4632`). Update `.claude/claudeclaw/settings.json`.

## Important
- Always read the current settings first before modifying
- Only update the fields the user wants to change
- Write the updated JSON back with proper formatting (2-space indent)
