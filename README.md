<p align="center">
  <img src="images/claudeclaw-banner.svg" alt="ClaudeClaw Banner" />
</p>
<p align="center">
  <img src="images/claudeclaw-wordmark.png" alt="ClaudeClaw Wordmark" />
</p>

<p align="center">
  <img src="https://awesome.re/badge.svg" alt="Awesome" />
  <a href="https://github.com/moazbuilds/ClaudeClaw/stargazers">
    <img src="https://img.shields.io/github/stars/moazbuilds/ClaudeClaw?style=flat-square" alt="GitHub Stars" />
  </a>
  <a href="https://github.com/moazbuilds/ClaudeClaw/commits/master">
    <img src="https://img.shields.io/github/last-commit/moazbuilds/ClaudeClaw?style=flat-square" alt="Last Commit" />
  </a>
  <a href="https://github.com/moazbuilds/ClaudeClaw/issues">
    <img src="https://img.shields.io/github/issues/moazbuilds/ClaudeClaw?style=flat-square" alt="Open Issues" />
  </a>
  <a href="https://x.com/moazbuilds">
    <img src="https://img.shields.io/badge/X-%40moazbuilds-000000?style=flat-square&logo=x" alt="X @moazbuilds" />
  </a>
</p>

<p align="center"><b>A lightweight, open-source OpenClaw version built into your Claude Code.</b></p>

ClaudeClaw turns your Claude Code into a personal assistant that never sleeps. It runs as a background daemon, executing tasks on a schedule, responding to messages on Telegram and Discord, transcribing voice commands, and integrating with any service you need.

> Note: Please don't use ClaudeClaw for hacking any bank system or doing any illegal activities. Thank you.

## Why ClaudeClaw?

| Category | ClaudeClaw | OpenClaw |
| --- | --- | --- |
| Anthropic Will Come After You | No | Yes |
| API Overhead | Directly uses your Claude Code subscription | Nightmare |
| Setup & Installation | ~5 minutes | Nightmare |
| Deployment | Install Claude Code on any device or VPS and run | Nightmare |
| Isolation Model | Folder-based and isolated as needed | Global by default (security nightmare) |
| Reliability | Simple reliable system for agents | Bugs nightmare |
| Feature Scope | Lightweight features you actually use | 600k+ LOC nightmare |
| Security | Average Claude Code usage | Nightmare |
| Cost Efficiency | Efficient usage | Nightmare |
| Memory | Uses Claude internal memory system + `CLAUDE.md` | Nightmare |

## Getting Started in 5 Minutes

```bash
claude plugin marketplace add moazbuilds/claudeclaw
claude plugin install claudeclaw
```
Then open a Claude Code session and run:
```
/claudeclaw:start
```
The setup wizard walks you through model, heartbeat, Telegram, Discord, and security, then your daemon is live with a web dashboard.

## What Would Be Built Next?

> **Mega Post:** Help shape the next ClaudeClaw features.
> Vote, suggest ideas, and discuss priorities in **[this post](https://github.com/moazbuilds/claudeclaw/issues/14)**.

<p align="center">
  <a href="https://github.com/moazbuilds/claudeclaw/issues/14">
    <img src="https://img.shields.io/badge/Roadmap-Mega%20Post-blue?style=for-the-badge&logo=github" alt="Roadmap Mega Post" />
  </a>
</p>

## Features

### Automation
- **Heartbeat:** Periodic check-ins with configurable intervals, quiet hours, and editable prompts.
- **Cron Jobs:** Timezone-aware schedules for repeating or one-time tasks with reliable execution.

### Communication
- **Telegram:** Text, image, and voice support.
- **Discord:** DMs, server mentions/replies, slash commands, voice messages, and image attachments.
- **Time Awareness:** Message time prefixes help the agent understand delays and daily patterns.

### Reliability and Control
- **GLM Fallback:** Automatically continue with GLM models if your primary limit is reached.
- **Web Dashboard:** Manage jobs, monitor runs, and inspect logs in real time.
- **Security Levels:** Four access levels from read-only to full system access.
- **Model Selection:** Switch models based on your workload.

## Using the Go Version

A Go rewrite lives in `go-rewrite/`. It produces a single binary with no runtime dependencies — no Bun or Node required.

### Build

```bash
cd go-rewrite
go build -o claudeclaw ./cmd/claudeclaw
```

### Run Standalone

The Go binary mirrors the TypeScript CLI exactly:

```bash
./claudeclaw start              # Start daemon (interactive setup wizard)
./claudeclaw start --web        # Start with web dashboard
./claudeclaw status             # Check daemon status
./claudeclaw send "your prompt" # Send a one-shot prompt
./claudeclaw telegram           # Start Telegram bot
./claudeclaw discord            # Start Discord bot
./claudeclaw --stop             # Stop daemon
./claudeclaw --clear            # Backup and reset session
```

### Use Inside Claude Code (like `/claudeclaw:start`)

To use the Go binary as the backend for Claude Code slash commands, replace the Bun invocation in the launch step with the Go binary path:

1. **Build the binary** and place it somewhere accessible (e.g. the project root or `go-rewrite/`):
   ```bash
   cd go-rewrite && go build -o claudeclaw ./cmd/claudeclaw
   ```

2. **Start the daemon** from your project directory:
   ```bash
   mkdir -p .claude/claudeclaw/logs
   nohup ./go-rewrite/claudeclaw start --web > .claude/claudeclaw/logs/daemon.log 2>&1 &
   ```

3. **Resume the shared session** in Claude Code:
   ```bash
   claude --resume $(cat .claude/claudeclaw/session.json | grep -o '"sessionId":"[^"]*"' | cut -d'"' -f4)
   ```

All slash commands (`/claudeclaw:status`, `/claudeclaw:stop`, etc.) work the same way — they read from the same `.claude/claudeclaw/` state directory. The Go binary and TypeScript version share identical config and state formats, so you can switch between them freely.

### Why Go?

| | TypeScript (Bun) | Go |
| --- | --- | --- |
| Dependencies | Requires Bun + Node | Single ~11 MB binary |
| Deployment | Install Bun first | Copy binary and run |
| Performance | V8 runtime overhead | Native execution |
| Cross-compile | N/A | `GOOS=linux GOARCH=arm64 go build` |

## FAQ

<details open>
  <summary><strong>Can ClaudeClaw do &lt;something&gt;?</strong></summary>
  <p>
    If Claude Code can do it, ClaudeClaw can do it too. ClaudeClaw adds cron jobs,
    heartbeats, and Telegram/Discord bridges on top. You can also give your ClaudeClaw new
    skills and teach it custom workflows.
  </p>
</details>

<details open>
  <summary><strong>Is this project breaking Anthropic ToS?</strong></summary>
  <p>
    No. ClaudeClaw is local usage inside the Claude Code ecosystem. It wraps Claude Code
    directly and does not require third-party OAuth outside that flow.
    If you build your own scripts to do the same thing, it would be the same.
  </p>
</details>

<details open>
  <summary><strong>Will Anthropic sue you for building ClaudeClaw?</strong></summary>
  <p>
    I hope not.
  </p>
</details>

<details open>
  <summary><strong>Are you ready to change this project name?</strong></summary>
  <p>
    If it bothers Anthropic, I might rename it to OpenClawd. Not sure yet.
  </p>
</details>

## Screenshots

### Claude Code Folder-Based Status Bar
![Claude Code folder-based status bar](images/bar.png)

### Cool UI to Manage and Check Your ClaudeClaw
![Cool UI to manage and check your ClaudeClaw](images/dashboard.png)

## Contributors

Thanks for helping make ClaudeClaw better.

<a href="https://github.com/moazbuilds/claudeclaw/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=moazbuilds/claudeclaw" />
</a>
