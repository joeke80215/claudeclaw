# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ClaudeClaw is a TypeScript/Bun daemon that turns Claude Code into a personal AI assistant. It runs scheduled tasks (cron jobs, heartbeats), integrates with Telegram and Discord for messaging, provides a web dashboard, and supports voice transcription via Whisper API.

## Commands

```bash
# Run the daemon
bun run src/index.ts start

# Dev mode with web UI and hot reload
bun run dev:web

# Run Telegram/Discord handlers
bun run src/index.ts telegram
bun run src/index.ts discord

# Check daemon status
bun run src/index.ts status

# Send a one-shot prompt to active daemon
bun run src/index.ts send <prompt>

# Install dependencies
bun install
```

There is no test suite or linter configured.

## Architecture

**Runtime**: Bun with ESM modules. TypeScript targeting `esnext` with bundler module resolution.

**Entry point**: `src/index.ts` — CLI dispatcher that routes to command handlers.

**Core modules** (`src/`):
- `runner.ts` — Main daemon loop. Checks cron schedules every 60s, manages Claude Code sessions (`--resume`), handles rate limiting with GLM fallback, logs executions.
- `config.ts` — Settings management (model, timezone, heartbeat, telegram, discord, security levels).
- `jobs.ts` — Cron job parser. Jobs are markdown files with YAML frontmatter (`schedule`, `recurring`, `notify` fields).
- `cron.ts` — Cron expression matching with timezone-aware scheduling.
- `sessions.ts` — Claude Code session management and caching.
- `skills.ts` — Skill discovery and routing across project/global/plugin scopes.
- `whisper.ts` — Speech-to-text transcription via Whisper API.

**Commands** (`src/commands/`):
- `start.ts` — Daemon initialization with interactive setup wizard.
- `stop.ts` — Daemon shutdown via PID file.
- `telegram.ts` — Polling-based Telegram bot with voice/image/text support.
- `discord.ts` — Gateway-based Discord bot with slash commands and DM support.
- `send.ts` — One-shot prompt execution against active daemon.

**Web UI** (`src/ui/`):
- `server.ts` — Bun.serve HTTP server (default `127.0.0.1:4632`).
- `services/` — REST API endpoints for state, settings, jobs, logs.
- `page/` — Single-page dashboard (HTML/CSS/JS generated in TypeScript).

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
