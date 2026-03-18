# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ClaudeClaw is a Go daemon that turns Claude Code into a personal AI assistant. It runs scheduled tasks (cron jobs, heartbeats), integrates with Telegram and Discord for messaging, provides a web dashboard, and supports voice transcription via Whisper API.

## Commands

```bash
# Build the binary
go build -o claudeclaw-bin ./cmd/claudeclaw

# Run the daemon
./claudeclaw-bin start

# Start with web dashboard
./claudeclaw-bin start --web

# Run Telegram/Discord handlers
./claudeclaw-bin telegram
./claudeclaw-bin discord

# Check daemon status
./claudeclaw-bin status

# Send a one-shot prompt to active daemon
./claudeclaw-bin send <prompt>

# Stop the daemon
./claudeclaw-bin --stop
```

There is no test suite or linter configured.

## Architecture

**Runtime**: Go 1.24+. Single static binary with no runtime dependencies.

**Entry point**: `cmd/claudeclaw/main.go` — CLI dispatcher that routes to command handlers.

**Core packages** (`internal/`):
- `runner/` — Main daemon loop. Checks cron schedules every 60s, manages Claude Code sessions (`--resume`), handles rate limiting with GLM fallback, logs executions.
- `config/` — Settings management (model, timezone, heartbeat, telegram, discord, security levels).
- `jobs/` — Cron job parser. Jobs are markdown files with YAML frontmatter (`schedule`, `recurring`, `notify` fields).
- `cron/` — Cron expression matching with timezone-aware scheduling.
- `sessions/` — Claude Code session management and caching.
- `skills/` — Skill discovery and routing across project/global/plugin scopes.
- `whisper/` — Speech-to-text transcription via Whisper API.

**Commands** (`internal/commands/`):
- `start.go` — Daemon initialization with interactive setup wizard.
- `stop.go` — Daemon shutdown via PID file.
- `telegram.go` — Polling-based Telegram bot with voice/image/text support.
- `discord.go` — Gateway-based Discord bot with slash commands and DM support.
- `send.go` — One-shot prompt execution against active daemon.

**Web UI** (`internal/web/`):
- `server.go` — HTTP server (default `127.0.0.1:4632`).
- REST API endpoints for state, settings, jobs, logs.
- Single-page dashboard (HTML/CSS/JS).

**Prompts** (`prompts/`):
- `BOOTSTRAP.md` — Initial setup and project onboarding prompt.
- `SOUL.md` — Personality and behavior guidelines.
- `IDENTITY.md` — User-customizable identity template (name, creature, vibe).
- `heartbeat/HEARTBEAT.md` — Template for periodic check-in prompts.

**Runtime state** is stored in `.claude/claudeclaw/` (gitignored):
- `settings.json` — Configuration.
- `state.json` — Heartbeat/job schedule state.
- `daemon.pid` — Process ID for daemon management.
- `jobs/` — User-created cron job markdown files.
- `logs/` — Execution logs.
- `sessions/` — Claude Code session caches.

**Plugin metadata** lives in `.claude-plugin/` (`plugin.json`, `marketplace.json`).

## Key Patterns

- The daemon executes prompts serially via a queue to prevent concurrent Claude Code session conflicts.
- Rate limiting detection ("you've hit your limit") triggers automatic fallback to a configured GLM model.
- Security has four levels: `locked` (no tools), `strict` (allowlist), `moderate` (default, blocklist), `unrestricted` (all tools).
- Cron jobs use standard 5-field cron syntax, stored as `.md` files with YAML frontmatter.
- ClaudeClaw manages its own block in the project's `CLAUDE.md` (delimited by HTML comments) — avoid manually editing that section.
