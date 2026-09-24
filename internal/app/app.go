// Package app implements the focusflow CLI — personal task and Claude session
// manager backed by SQLite.
package app

import (
	"fmt"
	"focusflow/internal/store"
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
		ensureRootPrivate()
		maybeAutoUpgradeSkill()
	}

	switch cmd {
	case "--version", "-v", "version":
		return cmdVersion(rest)
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
	case "workdir":
		return cmdWorkdir(rest)
	case "skill":
		return cmdSkill(rest)
	case "transcript":
		return cmdTranscript(rest)
	case "hook":
		return cmdHook(rest)
	case "message", "msg":
		return cmdMessage(rest)
	case "remember":
		return cmdRemember(rest)
	case "memories":
		return cmdMemories(rest)
	case "forget":
		return cmdForget(rest)
	case "memory":
		return cmdMemory(rest)
	case "broadcast", "post":
		return cmdBroadcast(rest)
	case "dashboard":
		return cmdDashboard(rest)
	case "project":
		return cmdProject(rest)
	case "pause":
		return cmdPause(rest)
	case "next":
		return cmdSetStage(store.StatusNext, rest)
	case "review":
		return cmdSetStage(store.StatusReview, rest)
	case "owner":
		return cmdOwner(rest)
	case "sync":
		return cmdSync(rest)
	case "search":
		return cmdSearch(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "backup":
		return cmdBackup(rest)
	case "restore":
		return cmdRestore(rest)
	case "stats":
		return cmdStats(rest)
	case "watch":
		return cmdWatch(rest)
	case "inbox":
		return cmdInbox(rest)
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
  focusflow add project  "<name>" --work-dir <path> [--slug <s>] [--priority h|m|l] [--mkdir]
  focusflow add task     "<name>" [--slug <s>] [--project <slug>] [--work-dir <path>] [--mkdir]
                            [--priority h|m|l] [--due <date>] [--assignee <name>] [--tag <t> ...]
  focusflow add playbook "<name>" --work-dir <path> [--slug <s>] [--project <slug>] [--mkdir]

Sessions:
  focusflow do    <ref> [--fresh] [--dangerously-skip-permissions] [--harness claude|codex]
                        [--with "<instruction>" | --with-file <path>]
  focusflow do    <ref> --here [--force]     (bind THIS harness session — no new tab)
  focusflow do    <ref> --auto               (run headlessly in the background)
  focusflow dashboard [open|start|status|url]   (the dashboard: board, projects, archive, inbox)
  focusflow project agent|pause|unschedule <slug>  |  project message <slug> "<text>" [--fanout] [--urgent]  |  project schedule <slug> --every <dur>
  focusflow pause <ref|project/<slug>>   (status → paused; stops a headless run / asks an open terminal to wrap up)
  focusflow next|review <ref>            (stage moves; review = ready for approval, done approves)
  focusflow done  <ref>
  focusflow run playbook  <slug> [--dangerously-skip-permissions] [--here] [--auto]
                                  [--with "<instruction>" | --with-file <path>]
  focusflow hook session-start       (SessionStart hook handler — wire via ~/.claude/settings.json)
  focusflow hook user-prompt-submit  (UserPromptSubmit hook — per-turn task anchor)
  focusflow hook stop                (Stop hook — nudges on an unread reply to an outstanding message)

Read:
  focusflow show task       [<ref>]
  focusflow show project    [<ref>]
  focusflow show playbook   <ref>
  focusflow transcript      [<ref>] [--compact]
  focusflow list tasks     [--status ...] [--project ...] [--priority ...] [--since ...] [--kind ...]
                            [--tag <t> ...] [--include-archived] [--include-done]
                            [--format table|json|tsv] [--no-color] [--no-truncate]
  focusflow list projects  [--status ...] [--include-archived] [--format table|json|tsv]
  focusflow list playbooks [--project <slug>] [--include-archived] [--format table|json|tsv]
  focusflow list tags      [--format table|json|tsv]
  focusflow list runs      [<playbook-slug>] [--status ...] [--include-archived] [--format table|json|tsv]

Edit / mutate:
  focusflow edit          <ref>
  focusflow update task   <ref> [--work-dir <path>] [--mkdir]
                          [--status ...] [--priority ...] [--assignee <name>] [--clear-assignee]
                          [--due-date <date>] [--clear-due] [--waiting "<who or what>"] [--clear-waiting]
                          [--tag <t> ...] [--remove-tag <t> ...] [--clear-tags]
  focusflow update project <ref> [--priority h|m|l]
  focusflow archive   <ref>
  focusflow unarchive <ref>

Message bus:
  focusflow message <assignee>[/<task-slug>] "<body>" [--urgent] [--reply-to <id>]
                                     (message a human — "user" is the local human — or the
                                      session bound to a task; never your own address; alias: msg)
  focusflow broadcast "<one-liner>" [--urgent]   (alias: post)
  focusflow inbox [--as <assignee>] [--all] [--json]
  focusflow inbox pop [--wait] [--timeout <s>] [--as <assignee>] [--json] [--keep-unread]
  focusflow inbox read <id> [--json]
  focusflow inbox stats [--as <assignee>]   (wait-time metrics)
  focusflow add owner "<name>" --work-dir <path> --every <dur> [--slug <s>] [--project <slug>] [--mkdir]
  focusflow owner list|show <slug>|start <slug>|pause <slug>|retire <slug> [--delete]
  focusflow owner next <slug> --in <dur> | --at <when>   |   owner tick <slug> [--auto]   |   owner tick-due
  focusflow owner scheduler install|uninstall|status   (opt-in launchd job that runs tick-due)
  focusflow sync [init --remote <url> | status | push]   (git-backed sync of briefs/updates/KB; opt-in)
  focusflow search <query> [--in names,briefs,updates,notes,kb] [--project <slug>] [--regex] [--json]
  focusflow doctor [--fix] [--json]           (health check)
  focusflow backup [--out <dir>] [--keep N]   (consistent snapshot of ~/.focusflow)
  focusflow backup schedule install|uninstall|status   (daily automatic backup via launchd)
  focusflow restore <archive.tar.gz> --force
  focusflow version
  focusflow stats [--since all|<N>d] [--project <slug>] [--card] [--out <path>]
  focusflow watch <task|project|assignee> [--as <assignee>] | --list | --rm <target>

Migration:

Workdirs:
  focusflow workdir list
  focusflow workdir add <path> [--name <nickname>]
  focusflow workdir remove <path>
  focusflow workdir scan [<root>] [--add]`)
}
