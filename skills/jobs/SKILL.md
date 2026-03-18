---
name: jobs
description: Manage ClaudeClaw cron jobs. Use when users say "jobs", "list jobs", "create job", "cron job", "schedule task", "scheduled tasks", "jobs list", "jobs create", "jobs delete", "claudeclaw jobs".
---

# ClaudeClaw Jobs

Manage scheduled cron jobs. Use `$ARGUMENTS` to determine the subcommand.

## Subcommands

### `list` (default if no arguments)
List all cron jobs:
```bash
ls .claude/claudeclaw/jobs/*.md 2>/dev/null
```
For each job file, read and display its YAML frontmatter (schedule, recurring, notify) and description.

### `create`
Create a new cron job. Ask the user for:
- **name**: job name (kebab-case, used as filename)
- **schedule**: 5-field cron expression (e.g., `0 9 * * *` for daily at 9am)
- **recurring**: true/false (default: true)
- **notify**: where to send results — `telegram`, `discord`, or `none`
- **prompt**: what the job should do

Create the job file at `.claude/claudeclaw/jobs/<name>.md` with this format:
```markdown
---
schedule: "0 9 * * *"
recurring: true
notify: telegram
---

The prompt/instructions for what this job should do.
```

### `delete`
Delete a cron job. List available jobs, ask which to delete, then:
```bash
rm .claude/claudeclaw/jobs/<name>.md
```

### `edit`
Edit an existing job. Read the current file, ask the user what to change, and write the updated content.

## Cron Syntax Reference
```
┌───────────── minute (0-59)
│ ┌───────────── hour (0-23)
│ │ ┌───────────── day of month (1-31)
│ │ │ ┌───────────── month (1-12)
│ │ │ │ ┌───────────── day of week (0-6, Sun=0)
│ │ │ │ │
* * * * *
```
