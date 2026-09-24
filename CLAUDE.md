# focusflow — repo conventions

## What this is

A Go CLI (`focusflow`) that manages personal tasks and bootstraps per-task Claude Code sessions. SQLite via `modernc.org/sqlite` (pure Go, no CGO).

## Build and test

```bash
# Build (produces ./focusflow in the repo dir; `make install` puts the one installed copy in ~/.local/bin)
make build
# or: go build -o focusflow .

# Install: copies the binary to ~/.local/bin (single copy), refreshes skill + hooks, restarts control-center
make install

# Run all tests (fast — no network, no real iTerm/Claude)
make test
# or: go test ./...

# Run a single test
go test -run TestE2EFullRoundtrip -v ./internal/app/
```

Tests use `$FOCUSFLOW_ROOT` pointed at a temp directory and override `$HOME` so nothing touches real `~/.focusflow/` or `~/.claude/`. External dependencies (osascript, claude CLI) are mocked via package-level function vars.

## Project structure

```
focusflow/
├── main.go                          # thin entry point — calls app.Run()
├── internal/
│   ├── app/                         # CLI commands and dispatch (one file per subcommand)
│   │   ├── app.go                   # Run(), printUsage()
│   │   ├── helpers.go               # flagSet()
│   │   ├── add.go archive.go edit.go done.go show.go list.go update.go
│   │   ├── do.go run.go             # session spawner; playbook runs
│   │   ├── hook.go                  # session-start / user-prompt-submit / stop hooks
│   │   ├── message.go inbox.go watch.go   # message bus
│   │   ├── stats.go transcript.go workdir.go playbook.go
│   │   ├── backup.go                # focusflow backup / restore (VACUUM INTO snapshot)
│   │   ├── doctor.go                # focusflow doctor [--fix]
│   │   ├── perms.go                 # 0700/0600 hardening of ~/.focusflow
│   │   ├── skill.go                 # skill + hooks install|uninstall|update
│   │   ├── version.go init.go liveness.go bootstrap.go resolve.go slug.go dateparse.go aux.go
│   │   ├── skill/SKILL.md           # embedded skill (//go:embed)
│   │   └── *_test.go
│   ├── store/                       # SQLite data layer
│   │   ├── db.go                    # schema, models, CRUD, OpenDB, versioned migrations
│   │   ├── messages.go bus_extras.go# bus queries, watches, stats, nudges
│   │   └── *_test.go
│   └── iterm/                       # terminal tab spawning (iTerm2, Terminal.app)
│       └── iterm.go
├── Makefile                         # build / install / test / check
├── README.md
├── CLAUDE.md
├── .github/workflows/
├── go.mod
└── go.sum
```

## Package responsibilities

- **`internal/app`** — all CLI command handlers, dispatch, shared helpers. One file per subcommand. Imports `store` and `iterm`.
- **`internal/store`** (`bus_extras.go` holds the watch/stats/nudge queries; `OpenDB` is the single opener — WAL, 30s busy timeout, foreign keys, versioned migrations via `PRAGMA user_version`) — schema DDL, model structs (`Project`, `Task`, `Workdir`), scan helpers, CRUD queries, migrations. All DB access via `database/sql` + `modernc.org/sqlite`.
- **`internal/iterm`** — osascript-based terminal spawning (iTerm2 default, Terminal.app via `FOCUSFLOW_TERM`). Exposes `iterm.Runner` var for test mocking.

## Conventions

- **No CGO.** Pure Go SQLite driver (`modernc.org/sqlite`).
- **Flag parsing:** `flag.FlagSet` with `ContinueOnError`, not `flag.Parse()`. Created via `flagSet()` helper in `internal/app/helpers.go`.
- **Exit codes:** 0 = success, 1 = runtime error, 2 = usage error.
- **Timestamps:** RFC3339 strings everywhere (never Unix timestamps).
- **Tests:** Table-driven where possible. Command tests live alongside source in `internal/app/`. `e2e_test.go` exercises the full command surface in sequence.
- **No mocks for DB.** Tests use real SQLite in a temp directory. Only osascript is mocked (via `iterm.Runner` function var).
- **Skill file is the source of truth** for how Claude sessions interact with focusflow. If the skill says something, the code must support it.
- **Skill embed path:** `internal/app/skill/SKILL.md` is embedded at compile time via `//go:embed` in `internal/app/skill.go`. After editing, rebuild for `focusflow skill update` to pick up changes.

## Data directory layout

```
~/.focusflow/
  focusflow.db
  kb/{user,org,products,processes,business}.md
  projects/<slug>/brief.md
  projects/<slug>/updates/*.md
  tasks/<slug>/brief.md
  tasks/<slug>/updates/*.md
  backups/focusflow-backup-*.tar.gz   (focusflow backup)
```
Everything under `~/.focusflow` is private (dirs 0700, files 0600); `ensureRootPrivate` re-applies this at startup.

## Things to watch out for

- `hookCommand` in `internal/app/skill.go` is the exact string matched in `~/.claude/settings.json`. Changing it orphans existing installations.
- `do.go` uses `openConcurrentDB` with `busy_timeout(30000)` and `_txlock=immediate` for safe concurrent access.
- Tests override `$HOME` — any code that calls `os.UserHomeDir()` will see the test's temp dir, not the real home.
- Hook tests must call `isolateHookEnv` (throwaway `$HOME`/`$FOCUSFLOW_ROOT`, cleared `CLAUDE_CODE_SESSION_ID`/`CODEX_THREAD_ID`) so nothing touches real state on a dev machine. `initTempRoot` also clears `FOCUSFLOW_TASK`/`FOCUSFLOW_PROJECT` so tests pass when run inside a live focusflow session.
