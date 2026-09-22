// Package app implements the focusflow CLI — personal task and Claude session
// manager backed by SQLite.
package app

import (
	"fmt"
	"os"
)

// Version holds the binary version string, set by main.go from a
// `-ldflags -X main.version=<tag>` build. Defaults to "dev" if main
// never assigns it (e.g. tests linking the package directly).
var Version = "dev"

// Run is the entry point for the CLI. Returns an exit code.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 0
	}
	cmd, rest := args[0], args[1:]

	// Auto-upgrade the skill + SessionStart hook if the binary version
	// has changed since the last install. Skipped for `init`, `skill`,
	// and `--version` — those manage the skill themselves or need to
	// run before any install state exists. See maybeAutoUpgradeSkill.
	switch cmd {
	case "init", "skill", "--version", "-v", "version", "-h", "--help", "help":
		// no auto-upgrade
	default:
		maybeAutoUpgradeSkill()
	}

	switch cmd {
	case "--version", "-v", "version":
		fmt.Println(Version)
		return 0
	case "init":
		return cmdInit(rest)
	case "add":
		return cmdAdd(rest)
	case "do":
		return cmdDo(rest)
	case "run":
		return cmdRun(rest)
	case "done":
		return cmdDone(rest)
	case "due":
		return cmdDue(rest)
	case "show":
		return cmdShow(rest)
	case "list":
		return cmdList(rest)
	case "edit":
		return cmdEdit(rest)
	case "update":
		return cmdUpdate(rest)
	case "archive":
		return cmdArchive(rest)
	case "unarchive":
		return cmdUnarchive(rest)
	case "priority":
		return cmdPriority(rest)
	case "waiting":
		return cmdWaiting(rest)
	case "workdir":
		return cmdWorkdir(rest)
	case "skill":
		return cmdSkill(rest)
	case "transcript":
		return cmdTranscript(rest)
	case "hook":
		return cmdHook(rest)
	case "-h", "--help", "help":
		printUsage()
		return 0
	}
	fmt.Fprintf(os.Stderr, "error: unknown subcommand %q\n", cmd)
	printUsage()
	return 2
}

func printUsage() {
	fmt.Println(`focusflow — personal task and Claude session manager

Setup:
  focusflow init
  focusflow skill install [--force]
  focusflow skill uninstall
  focusflow skill update

Create:
  focusflow add project "<name>" --work-dir <path> [--slug <s>] [--priority h|m|l] [--mkdir]
  focusflow add task    "<name>" [--slug <s>] [--project <slug>] [--work-dir <path>] [--mkdir] [--priority h|m|l] [--due <date>]

Sessions:
  focusflow do                <ref> [--fresh] [--dangerously-skip-permissions]
  focusflow done              <ref>
  focusflow hook session-start                      (SessionStart hook handler — wire via ~/.claude/settings.json)

Read:
  focusflow show task       [<ref>]
  focusflow show project    [<ref>]
  focusflow transcript      [<ref>] [--compact]           (readable transcript from session jsonl)
  focusflow list tasks    [--status ...] [--project ...] [--priority ...] [--since ...] [--include-archived]
  focusflow list projects [--status ...] [--include-archived]

Edit / mutate:
  focusflow edit        <ref>
  focusflow update task <ref> [--session-id <uuid>] [--work-dir <path>] [--mkdir]
  focusflow due         <ref> <date> | --clear                    (set or clear due date; date: YYYY-MM-DD, today, tomorrow, monday, 3d)
  focusflow priority  <ref> high|medium|low
  focusflow waiting   <ref> "<who or what>" | --clear
  focusflow archive   <ref>
  focusflow unarchive <ref>

Workdirs:
  focusflow workdir list
  focusflow workdir add <path> [--name <nickname>]
  focusflow workdir remove <path>
  focusflow workdir scan [<root>] [--add]

Playbooks:
  focusflow add playbook   "<name>" --work-dir <path> [--slug <s>] [--project <slug>] [--mkdir]
  focusflow run playbook   <slug> [--dangerously-skip-permissions]
  focusflow show playbook  <ref>
  focusflow list playbooks [--project <slug>] [--include-archived]`)
}
