# focusflow

You don't hire a new engineer every day. You hire one, and you work together.

Claude is the most capable coding partner you've ever had — but every
session starts from zero. It doesn't know what you're building, what you
tried yesterday, or why you care. You re-explain yourself constantly.
The more sessions you run, the worse it gets.

focusflow is the working relationship between you and Claude.

It's not a task tracker. It's not a session manager. It's the layer that
turns isolated Claude conversations into continuous collaboration — where
context compounds instead of evaporating.

## The problem

Think about how you use Claude today:

- You start a session, explain your project, get deep into a problem.
  Next morning: fresh session, start over.
- You have five sessions open. Which one had the auth discussion?
  Which one has your half-finished migration?
- You ask Claude to help prioritize — but it doesn't know what your
  week looks like.
- A colleague who's worked with you for a month knows your org, your
  role, your products, your team's quirks, your deployment process.
  Claude knows none of this. Every session, it's a stranger.

The bottleneck isn't Claude's capability. It's context.

## What focusflow does

focusflow sits between you and Claude. You tell focusflow what you're working on
and why. Claude gets that context automatically — every session, every
time.

**You capture your work once.**
Projects, tasks, priorities, acceptance criteria — structured through a
quick interview, not a form. Focusflow asks what, why, where, and done-when,
then writes it down.

**Claude shows up informed.**
When you start a session on a task, Claude gets the brief, the progress
notes, the repo conventions, and your knowledge base — before you say a
word.

**Context compounds.**
Progress notes accumulate. Your knowledge base grows. What Claude knows
about you on day 50 is radically different from day 1. You stop
repeating yourself. Claude starts anticipating.

**Sessions persist.**
Pick up where you left off. `focusflow do auth` resumes the same Claude
conversation — same context, same thread, same momentum.

## How it works

- **Projects and tasks** live in a local SQLite database. Each task gets
  a markdown brief capturing what, why, where, and done-when.
- **`focusflow do <task>`** spawns a Claude session in a dedicated iTerm tab
  with full context injected — brief, progress notes, repo conventions,
  knowledge base. Resume the same session tomorrow with the same command.
- **Knowledge base** — five markdown files tracking durable facts about
  you, your org, your products, your processes, and your business. Claude
  reads these and learns them over time. You never repeat yourself.
- **Progress notes** — append-only logs under each task. Context survives
  across sessions so Claude knows what happened last time.
- **A Claude skill** interprets natural language into focusflow commands. Say
  "what should I work on" or "add a task" — the skill handles the rest.

## Prerequisites

- macOS (iTerm2 for session spawning)
- [Claude Code](https://claude.ai/claude-code) CLI installed

## Install

Download the latest binary for your Mac, mark it executable, clear the
quarantine flag, and put it on your PATH:

```bash
# Apple Silicon (M1/M2/M3/M4):
ARCH=arm64
# Intel:
# ARCH=amd64

curl -fsSL -o /usr/local/bin/focusflow \
  "https://github.com/Facets-cloud/focusflow/releases/latest/download/focusflow-darwin-${ARCH}"
chmod +x /usr/local/bin/focusflow
xattr -d com.apple.quarantine /usr/local/bin/focusflow 2>/dev/null || true

focusflow init
```

`focusflow init` creates `~/.focusflow/`, the database, the knowledge base, and
installs the focusflow skill into `~/.claude/skills/focusflow/SKILL.md` plus a
SessionStart hook in `~/.claude/settings.json`.

Then run **`claude`** and say **"let's get to work"**.

> The `xattr` step removes Gatekeeper's quarantine attribute so macOS
> doesn't refuse to run the unsigned binary. If you prefer, the first
> launch will fail and you can right-click → Open in Finder to allow
> it instead.

## Upgrade

Re-download the binary the same way you installed it:

```bash
curl -fsSL -o /usr/local/bin/focusflow \
  "https://github.com/Facets-cloud/focusflow/releases/latest/download/focusflow-darwin-${ARCH}"
chmod +x /usr/local/bin/focusflow
xattr -d com.apple.quarantine /usr/local/bin/focusflow 2>/dev/null || true
```

The next time you run any focusflow command, the binary detects the version
bump and refreshes the skill + SessionStart hook automatically. Check
the running version with:

```bash
focusflow --version
```

## Build from source

If you want to hack on focusflow, clone and build with the included
Makefile:

```bash
git clone git@github.com:Facets-cloud/focusflow.git
cd focusflow
make install     # builds, adds repo dir to PATH, installs skill + hook
source ~/.zshrc
focusflow init
```

Local dev builds are tagged `dev` and skip the auto-upgrade check, so
you can iterate on the skill without your changes being clobbered.

## Usage

You don't need to memorize commands. Just talk to Claude:

- **"what should I work on"** — shows your task list
- **"add a task"** — interviews you and saves a structured brief
- **"resume auth"** — opens a dedicated Claude session for that task
- **"save a note"** — logs progress under the current task
- **"mark done"** — closes out the task

For direct CLI use:

```bash
focusflow add project "My App" --work-dir ~/code/my-app
focusflow add task "Add auth" --project my-app --slug auth
focusflow do auth
focusflow list tasks --status in-progress
focusflow done auth
```

## How it works under the hood

`focusflow do <task>` pre-allocates a session UUID, writes it to the task row,
and spawns a new iTerm tab running `claude --session-id <uuid>` with
`FOCUSFLOW_TASK` / `FOCUSFLOW_PROJECT` environment variables inlined. The jsonl
file lands at the deterministic path
`~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, so future `focusflow do`
calls spawn `claude --resume <uuid>` to continue the same conversation.
A SessionStart hook re-injects the task brief, updates, and CLAUDE.md
context on every resume.

Briefs live at `~/.focusflow/tasks/<slug>/brief.md`. Progress notes accumulate
under `~/.focusflow/tasks/<slug>/updates/`. The focusflow skill (installed to
`~/.claude/skills/focusflow/SKILL.md`) interprets natural language into focusflow
commands and enforces interview-driven intake.

## Data directory

All runtime state lives under `~/.focusflow/` (or `$FOCUSFLOW_ROOT` if set):

```
~/.focusflow/
  focusflow.db          # SQLite database
  kb/              # knowledge base (5 markdown files)
  projects/        # per-project briefs and updates
  tasks/           # per-task briefs and updates
```

## Environment variables

| Variable | Purpose |
|---|---|
| `FOCUSFLOW_ROOT` | Override the default `~/.focusflow` data directory |
| `FOCUSFLOW_TASK` | Set by `focusflow do` — current task slug |
| `FOCUSFLOW_PROJECT` | Set by `focusflow do` — current project slug |
| `FOCUSFLOW_STALE_DAYS` | Staleness threshold in days (default: 3) |
| `FOCUSFLOW_TERM` | Terminal used for new sessions: `iTerm2` (default) or `Terminal.app` |

## Operations

```bash
focusflow doctor [--fix]              # health check: hooks, skill, binary drift, DB integrity, dead work_dirs, backups
focusflow backup [--out <dir>] [--keep N]   # consistent snapshot of ~/.focusflow (default: ~/.focusflow/backups, keep 10)
focusflow restore <archive> --force   # validate, then replace data (previous data is kept under backups/pre-restore-*)
focusflow version                     # build version + commit
focusflow stats [--since 30d] [--project <slug>] [--card]
focusflow next|review|pause <ref>       # stages: backlog → next → in-progress ⇄ paused → review → done
focusflow project message <slug> "…" [--fanout]   # one permanent coordinator session per project
focusflow watch <task|project|assignee>   # get broadcasts fanned out to your task's inbox
focusflow inbox stats
```

`make install` copies the binary to `~/.local/bin` (the one installed copy), refreshes the
skill and the three Claude Code hooks (SessionStart, UserPromptSubmit, Stop), and restarts a
tend launchd job if one is installed. The database runs in WAL mode with a 30s busy
timeout, and its schema is versioned (`PRAGMA user_version`); a database newer than the binary is
refused rather than opened. Everything under `~/.focusflow` is private to your user (0700/0600).
