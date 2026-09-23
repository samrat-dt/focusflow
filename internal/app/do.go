package app

import (
	"database/sql"
	"errors"
	"fmt"
	"focusflow/internal/flowdb"
	"focusflow/internal/iterm"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// execCommand wraps exec.Command so tests can substitute a fake process
// without depending on the real `claude` binary existing in the test
// environment. Matches the iterm.Runner mocking pattern.
var execCommand = exec.Command

// openConcurrentDB opens focusflow.db with a generous busy_timeout so that two
// concurrent `focusflow do` processes (or two goroutines in the tests) will
// serialize at the SQLite file level rather than failing fast with
// SQLITE_BUSY. The pragma is applied at connection-open time via the DSN
// so every conn in the pool inherits it. Schema creation still runs via
// OpenDB to keep DDL in one place.
func openConcurrentDB(path string) (*sql.DB, error) {
	// Ensure schema exists via the shared OpenDB path.
	pre, err := flowdb.OpenDB(path)
	if err != nil {
		return nil, err
	}
	pre.Close()

	q := url.Values{}
	// 30s is enough to cover realistic bootstraps; tests finish in ms.
	q.Set("_pragma", "busy_timeout(30000)")
	// BEGIN IMMEDIATE acquires a RESERVED lock up-front, so two concurrent
	// `focusflow do` transactions serialize at tx.Begin() (waiting on the busy
	// timeout) instead of racing to the first write and failing.
	q.Set("_txlock", "immediate")
	dsn := "file:" + path + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	return db, nil
}

// cmdDo flips a task to in-progress, bootstraps a Claude session if
// needed (race-free via atomic UPDATE ... WHERE session_id IS ?), and
// spawns an iTerm tab to resume it. See spec §6 for the full protocol.
func cmdDo(args []string) int {
	if len(args) == 0 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: do requires a task ref")
		return 2
	}
	query := args[0]
	fs := flagSet("do")
	fresh := fs.Bool("fresh", false, "discard existing session and re-bootstrap")
	dangerSkip := fs.Bool("dangerously-skip-permissions", false, "pass --dangerously-skip-permissions through to claude")
	here := fs.Bool("here", false, "bind the current harness session to this task instead of spawning a tab")
	force := fs.Bool("force", false, "override the live-session guard")
	harness := fs.String("harness", "", "claude|codex (first-session choice only)")
	with := fs.String("with", "", "one-off instruction to include in the session's first/next message")
	withFile := fs.String("with-file", "", "path to a file whose contents are used as --with")
	auto := fs.Bool("auto", false, "run headlessly in the background; implies --dangerously-skip-permissions")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *with != "" && *withFile != "" {
		fmt.Fprintln(os.Stderr, "error: --with and --with-file are mutually exclusive")
		return 2
	}
	if *harness != "" && *harness != "claude" && *harness != "codex" {
		fmt.Fprintf(os.Stderr, "error: --harness must be claude or codex, got %q\n", *harness)
		return 2
	}
	if *auto && *here {
		fmt.Fprintln(os.Stderr, "error: --auto and --here are mutually exclusive")
		return 2
	}
	if *auto {
		*dangerSkip = true
	}
	var withText string
	if *with != "" {
		withText = *with
	} else if *withFile != "" {
		b, err := os.ReadFile(*withFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --with-file: %v\n", err)
			return 1
		}
		withText = string(b)
	}

	var extraClaudeArgs []string
	if *dangerSkip {
		extraClaudeArgs = append(extraClaudeArgs, "--dangerously-skip-permissions")
	}

	dbPath, err := flowDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openConcurrentDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	task, rc := findTask(db, query)
	if rc != 0 {
		return rc
	}

	if *here {
		return doHere(db, task, *force)
	}

	// Step 2: atomic status flip inside a transaction. Captures preSessionID
	// and other fields for later steps. Per spec §6 this commit is the
	// durability boundary — even if bootstrap or iTerm spawn fails below,
	// the task is already in 'in-progress'.
	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: begin tx: %v\n", err)
		return 1
	}
	// If we don't commit by the end, rollback.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Re-read inside the tx so we see the freshest status.
	var curStatus string
	if err := tx.QueryRow(`SELECT status FROM tasks WHERE slug = ?`, task.Slug).Scan(&curStatus); err != nil {
		fmt.Fprintf(os.Stderr, "error: re-read task: %v\n", err)
		return 1
	}
	if curStatus == "done" {
		fmt.Fprintf(os.Stderr,
			"error: task %q is done; edit its status back to backlog or in-progress to reopen it\n",
			task.Slug)
		return 1
	}

	// Decide bootstrap vs resume based on the row we re-read inside the tx.
	// Fresh bootstrap means: either the task has no session_id, or --fresh
	// was passed. In both cases we allocate a new UUID here and claim it
	// in the DB via the status-flip UPDATE below — so the jsonl file claude
	// writes is identified deterministically by us, not scraped afterwards.
	var curSessionID sql.NullString
	if err := tx.QueryRow(`SELECT session_id FROM tasks WHERE slug=?`, task.Slug).Scan(&curSessionID); err != nil {
		fmt.Fprintf(os.Stderr, "error: re-read session_id: %v\n", err)
		return 1
	}
	needsBootstrap := !curSessionID.Valid || *fresh
	var sessionID string
	if needsBootstrap {
		id, err := newUUID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: allocate session id: %v\n", err)
			return 1
		}
		sessionID = id
	} else {
		sessionID = curSessionID.String
	}

	now := flowdb.NowISO()
	if needsBootstrap {
		resolvedHarness := *harness
		if resolvedHarness == "" {
			resolvedHarness = "claude"
		}
		if _, err := tx.Exec(
			`UPDATE tasks SET status='in-progress',
			 status_changed_at = CASE WHEN status != 'in-progress' THEN ? ELSE status_changed_at END,
			 session_id=?, session_started=?, harness=?, updated_at=?
			 WHERE slug=? AND status IN ('backlog','in-progress')`,
			now, sessionID, now, resolvedHarness, now, task.Slug,
		); err != nil {
			fmt.Fprintf(os.Stderr, "error: flip status: %v\n", err)
			return 1
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE tasks SET status='in-progress',
			 status_changed_at = CASE WHEN status != 'in-progress' THEN ? ELSE status_changed_at END,
			 updated_at=?
			 WHERE slug=? AND status IN ('backlog','in-progress')`,
			now, now, task.Slug,
		); err != nil {
			fmt.Fprintf(os.Stderr, "error: flip status: %v\n", err)
			return 1
		}
	}
	// Re-select to capture the canonical view.
	row := tx.QueryRow(`SELECT `+flowdb.TaskCols+` FROM tasks WHERE slug = ?`, task.Slug)
	fresh2, err := flowdb.ScanTask(row)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: re-select task: %v\n", err)
		return 1
	}
	task = fresh2
	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "error: commit: %v\n", err)
		return 1
	}
	committed = true

	if *auto {
		return spawnHeadless(db, task, sessionID, needsBootstrap, extraClaudeArgs, withText)
	}

	if *fresh && curSessionID.Valid {
		fmt.Printf("--fresh: discarding old session %s\n", curSessionID.String)
	}

	// Look up project (may be nil).
	var project *flowdb.Project
	if task.ProjectSlug.Valid {
		p, err := flowdb.GetProject(db, task.ProjectSlug.String)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			fmt.Fprintf(os.Stderr, "error: get project: %v\n", err)
			return 1
		}
		project = p
	}

	cwd := task.WorkDir
	if cwd == "" {
		fmt.Fprintf(os.Stderr, "error: task %q has no work_dir\n", task.Slug)
		return 1
	}

	// Spawn the iTerm tab.
	//
	// We shell out to `claude` directly (no wrapper). The skill on disk at
	// ~/.claude/skills/focusflow/SKILL.md is whatever was last installed via
	// `focusflow skill install` / `focusflow skill update`. To refresh it after
	// upgrading focusflow, the user runs `focusflow skill update` manually.
	var command string
	if needsBootstrap {
		// Fresh bootstrap path: we pre-allocated the session UUID above
		// and committed it to the DB. Passing --session-id to claude
		// makes it write its jsonl at the deterministic path
		// ~/.claude/projects/<encoded-cwd>/<sessionID>.jsonl, so there is
		// nothing to discover afterwards.
		playbookSlug := ""
		isFirstRun := false
		if task.PlaybookSlug.Valid {
			playbookSlug = task.PlaybookSlug.String
			// First run = this is the only non-archived run-task for the
			// playbook. The current run row was just inserted by
			// cmdRunPlaybook, so a count of 1 means no prior runs exist.
			var runCount int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM tasks WHERE playbook_slug = ? AND kind = 'playbook_run' AND archived_at IS NULL`,
				playbookSlug,
			).Scan(&runCount); err != nil {
				fmt.Fprintf(os.Stderr, "warning: count playbook runs: %v\n", err)
			}
			isFirstRun = runCount <= 1
		}
		prompt := buildBootstrapPromptForKindV2(task.Slug, task.Kind, playbookSlug, isFirstRun)
		if withText != "" {
			prompt = prompt + "\n\n" + withText
		}
		command = fmt.Sprintf("claude --session-id %s %s", sessionID, iterm.ShellQuote(prompt))
	} else {
		// Resume path: the UUID we already have in the DB is what claude
		// used to write its existing jsonl.
		command = "claude --resume " + sessionID
		if withText != "" {
			command += " " + iterm.ShellQuote(withText)
		}
	}
	if *dangerSkip {
		command += " --dangerously-skip-permissions"
	}
	envVars := map[string]string{"FOCUSFLOW_TASK": task.Slug}
	if project != nil {
		envVars["FOCUSFLOW_PROJECT"] = project.Slug
	}
	if err := iterm.SpawnTab(buildTabTitle(project, task), cwd, command, envVars); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Post-spawn bookkeeping, outside the main tx.
	now2 := flowdb.NowISO()
	if !needsBootstrap {
		if _, err := db.Exec(
			`UPDATE tasks SET session_last_resumed = ? WHERE slug = ?`,
			now2, task.Slug,
		); err != nil {
			fmt.Fprintf(os.Stderr, "error: record resume: %v\n", err)
			return 1
		}
	}
	if _, err := db.Exec(
		`UPDATE workdirs SET last_used_at = ? WHERE path = ?`,
		now2, task.WorkDir,
	); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bump workdir last_used_at: %v\n", err)
	}

	if needsBootstrap {
		fmt.Printf("Spawned %s (session %s)\n", task.Slug, sessionID)
	} else {
		fmt.Printf("Resumed %s (session %s)\n", task.Slug, sessionID)
	}
	return 0
}

// buildBootstrapPromptForKind dispatches to the right prompt variant
// based on task kind. For kind='playbook_run' the playbook variant is
// used; otherwise the regular task variant. Empty kind (legacy rows
// that somehow didn't migrate) falls through to the regular variant.
//
// The bootstrap prompt is intentionally shell-safe — no single/double
// quotes, backticks, or dollar signs — because it gets shell-quoted
// as a single positional argument to `claude`.
//
// The session's UUID is pre-allocated by `focusflow do` and passed via
// `claude --session-id <uuid>`, so there is no self-registration step
// here. The session loads context in order: task brief + task updates,
// then (if any) project brief + project updates, then CLAUDE.md files
// in the work_dir. The focusflow skill enforces this sequence too; the
// bootstrap prompt is a backup in case the skill isn't auto-activated.
// Kept for callers (and tests) that don't track first-run state. New
// callers should use buildBootstrapPromptForKindV2 to opt into the
// first-run variant when relevant.
func buildBootstrapPromptForKind(slug, kind, playbookSlug string) string {
	return buildBootstrapPromptForKindV2(slug, kind, playbookSlug, false)
}

// buildBootstrapPromptForKindV2 is the kind-aware dispatcher with first-
// run awareness for playbook runs. When isFirstRun=true on a playbook
// run, a richer "capture-aggressive" prompt is emitted that nudges the
// session to harvest scripts, edge cases, and decision rules back into
// the live playbook brief / sidecar files.
func buildBootstrapPromptForKindV2(slug, kind, playbookSlug string, isFirstRun bool) string {
	if kind == "playbook_run" {
		return buildPlaybookRunBootstrapPrompt(slug, playbookSlug, isFirstRun)
	}
	return buildTaskBootstrapPrompt(slug)
}

// autonomousModeAddendum tells a `do --auto` session it is running
// unattended and must close the loop itself — without this, a run that
// finishes normally but never calls `focusflow done` leaves
// auto_run_status stuck at 'running' until refreshAutoRunStatus
// eventually notices the process exited and marks it 'dead', which is
// indistinguishable from a crash to anything reading the status later.
func autonomousModeAddendum(slug string) string {
	return fmt.Sprintf(
		"IMPORTANT — you are running headlessly and unattended (no human is watching this session). "+
			"When you have finished the work described above, run `focusflow done %s` yourself to close out the task. "+
			"Do not wait for a human to do this — no one will.",
		slug,
	)
}

// buildTaskBootstrapPrompt is the prompt for regular tasks.
func buildTaskBootstrapPrompt(slug string) string {
	return fmt.Sprintf(
		"You are the execution session for focusflow task %s. Do ALL of the following in order before touching code:\n"+
			"1. Invoke the focusflow skill via the Skill tool. This loads the operating manual that governs how this session works: workflows, bootstrap contract, KB discipline, and scope-creep detection.\n"+
			"2. Run: focusflow show task. Read the file at the brief: path AND every file listed under updates:. Files listed under other: are sidecar references — load on demand when relevant, not eagerly.\n"+
			"3. If a project is listed on the task, run: focusflow show project <that-project-slug>. Read its brief AND every file under updates:. Files under other: are on-demand references.\n"+
			"4. Read CLAUDE.md in your work_dir and any nested CLAUDE.md files under subdirectories you will modify. These override any assumption from the brief.\n"+
			"5. Only then begin work. If any brief section is blank or unclear, ASK — do not infer.",
		slug,
	)
}

// buildPlaybookRunBootstrapPrompt is the prompt for playbook-run tasks.
// Adds an explicit `focusflow show playbook <slug>` context-load step and
// frames the run's brief as an authoritative snapshot — the session
// must execute against that snapshot, not re-read the live playbook
// brief (which may drift between runs).
func buildPlaybookRunBootstrapPrompt(runSlug, playbookSlug string, isFirstRun bool) string {
	base := fmt.Sprintf(
		"You are running playbook `%s` as run `%s`. Do ALL of the following in order before executing anything:\n"+
			"1. Invoke the focusflow skill via the Skill tool. This loads the operating manual that governs how this session works.\n"+
			"2. Run: focusflow show playbook %s. This shows the playbook's definition and recent runs — context only, not your instructions. Note any files listed under other: — they're sidecar references you can Read on demand if relevant; do not eagerly load them.\n"+
			"3. Run: focusflow show task. Read the file at the brief: path AND every file listed under updates:. Files under other: are references for THIS run; load on demand when relevant. The brief is your authoritative instructions for this run — it was snapshotted from the playbook at the moment this run started. Execute against this, not the live playbook brief.\n"+
			"4. If a project is listed on the task, run: focusflow show project <that-project-slug>. Read its brief and every file under updates:. Files under other: are on-demand references.\n"+
			"5. Read CLAUDE.md in your work_dir.\n"+
			"6. Only then begin executing your brief.\n"+
			"\n"+
			"While executing: if the user adjusts the playbook's procedure during this run (e.g. 'let's always do X', 'change the approach for...', 'this step should also...'), pause and ask via AskUserQuestion whether to persist the change to the playbook's live brief.md so future runs benefit. Options: 'Persist to playbook' (Edit playbooks/%s/brief.md), 'Just this run' (no change to live playbook), 'Both — persist + log a note in playbooks/%s/updates/'. The run's own brief.md is a frozen snapshot — never edit it to change future behavior; that's what the live playbook brief is for. See focusflow skill §4.13 for the full pattern.",
		playbookSlug, runSlug, playbookSlug, playbookSlug, playbookSlug,
	)

	if !isFirstRun {
		return base
	}

	firstRunAddendum := fmt.Sprintf(
		"\n"+
			"\n"+
			"⚡ THIS IS THE FIRST RUN OF THIS PLAYBOOK ⚡\n"+
			"\n"+
			"The brief was written aspirationally; this run is where the actual procedure crystallizes. Be MORE proactive than usual about capturing back to the live playbook. Specifically:\n"+
			"\n"+
			"- When you write a script, command, or settle on a concrete decision rule that wasn't in the brief: don't wait for the user to ask. Pause and AskUserQuestion whether to capture it. Three capture targets:\n"+
			"    • 'Add to playbook brief' — append/edit the relevant section of playbooks/%s/brief.md so future runs see it inline\n"+
			"    • 'Save as sidecar file' — write to playbooks/%s/<topic>.md (e.g. decision-tree.md, sample-script.md, edge-cases.md). These get surfaced under `other:` in focusflow show playbook for future runs to load on demand\n"+
			"    • 'Just this run' — apply locally, don't change the playbook (rare; usually means it's run-specific)\n"+
			"- When you discover an edge case or signal worth watching: AskUserQuestion whether to add it to the 'Signals to watch for' section of the live brief.\n"+
			"- Before focusflow done at the end of the run, AskUserQuestion: 'Capture anything from this run back to the playbook before closing?' Options: 'Yes — walk me through what to capture' / 'No, close out as-is'. The 'walk me through' path: list candidate captures (scripts produced, decisions made, edge cases hit, commands you ended up using) and offer per-item via AskUserQuestion.\n"+
			"\n"+
			"After this run, the playbook should be substantially more concrete than the aspirational brief it started with. That's the point. Treat capture-back as a primary deliverable of the first run, not an afterthought.",
		playbookSlug, playbookSlug,
	)

	return base + firstRunAddendum
}

// buildBootstrapPrompt is a backwards-compat shim for old callers that
// pass only a slug. Now points at the regular-task variant. Tests still
// call this to verify the regular variant.
func buildBootstrapPrompt(slug string) string {
	return buildTaskBootstrapPrompt(slug)
}

// buildTabTitle returns a short iTerm tab title. Project-scoped tasks get
// "<project-slug>/<task-slug>"; floating tasks get just "<task-slug>".
// Titles longer than 30 runes are truncated with a trailing ellipsis.
func buildTabTitle(project *flowdb.Project, task *flowdb.Task) string {
	raw := task.Slug
	if project != nil {
		raw = project.Slug + "/" + task.Slug
	}
	const maxLen = 30
	runes := []rune(raw)
	if len(runes) > maxLen {
		return string(runes[:maxLen-1]) + "…"
	}
	return raw
}

// findTask resolves a user-supplied ref to exactly one non-archived task.
// Exact alias match first, then exact slug match.
func findTask(db *sql.DB, query string) (*flowdb.Task, int) {
	t, err := ResolveTask(db, query, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	return t, 0
}

// doHere binds the current process's harness session id to task without
// spawning a tab. The env var checked mirrors flow's own detection order
// (see currentHarnessSessionID in mirror.go / §10 of the flow skill):
// Claude Code first, then Codex.
func doHere(db *sql.DB, task *flowdb.Task, force bool) int {
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	harness := "claude"
	if sessionID == "" {
		sessionID = os.Getenv("CODEX_THREAD_ID")
		harness = "codex"
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --here requires a harness session id in the environment (CLAUDE_CODE_SESSION_ID or CODEX_THREAD_ID)")
		return 1
	}

	now := flowdb.NowISO()
	query := `UPDATE tasks SET status='in-progress',
		 status_changed_at = CASE WHEN status != 'in-progress' THEN ? ELSE status_changed_at END,
		 session_id=?, session_started=?, harness=?, updated_at=?
		 WHERE slug=? AND status IN ('backlog','in-progress')`
	args := []any{now, sessionID, now, harness, now, task.Slug}
	if !force {
		query += ` AND (session_id IS NULL OR session_id = ?)`
		args = append(args, sessionID)
	}
	res, err := db.Exec(query, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			fmt.Fprintln(os.Stderr, "error: this session is already bound to another task; a session can only be bound to one task")
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: bind session: %v\n", err)
		return 1
	}
	n, err := res.RowsAffected()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if n == 0 {
		fmt.Fprintf(os.Stderr, "error: task %q could not be bound (already done, or already bound to a different session — pass --force to override)\n", task.Slug)
		return 1
	}
	fmt.Printf("Bound %s to this session (%s)\n", task.Slug, sessionID)
	return 0
}

// spawnHeadless launches claude as a detached background process for
// `do --auto`. Unlike the interactive tab path, this never blocks: it
// starts the process, records its pid, and returns immediately. The
// process is expected to call `focusflow done` on itself when finished
// (per the flow skill's autonomous-mode contract) — spawnHeadless does
// not wait for that; it only tracks whether the pid is still alive via
// refreshAutoRunStatus, called lazily from list/show.
func spawnHeadless(db *sql.DB, task *flowdb.Task, sessionID string, needsBootstrap bool, extraArgs []string, withText string) int {
	root, err := flowRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	logDir := filepath.Join(root, "tasks", task.Slug, "auto-runs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: mkdir %s: %v\n", logDir, err)
		return 1
	}
	logPath := filepath.Join(logDir, time.Now().UTC().Format("2006-01-02-15-04-05")+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create log %s: %v\n", logPath, err)
		return 1
	}

	// -p/--print is required for a headless run: without it claude starts
	// its interactive TUI, which fails immediately with no TTY attached
	// (stdout/stderr here go to a log file, not a terminal).
	var args []string
	if needsBootstrap {
		playbookSlug := ""
		if task.PlaybookSlug.Valid {
			playbookSlug = task.PlaybookSlug.String
		}
		prompt := buildBootstrapPromptForKindV2(task.Slug, task.Kind, playbookSlug, false)
		prompt = prompt + "\n\n" + autonomousModeAddendum(task.Slug)
		if withText != "" {
			prompt = prompt + "\n\n" + withText
		}
		args = []string{"--session-id", sessionID, "-p", prompt}
	} else {
		resumeText := autonomousModeAddendum(task.Slug)
		if withText != "" {
			resumeText = resumeText + "\n\n" + withText
		}
		args = []string{"--resume", sessionID, "-p", resumeText}
	}
	args = append(args, extraArgs...)

	cmd := execCommand("claude", args...)
	cmd.Dir = task.WorkDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "FOCUSFLOW_TASK="+task.Slug)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		fmt.Fprintf(os.Stderr, "error: start headless claude: %v\n", err)
		return 1
	}
	pid := cmd.Process.Pid
	go func() {
		cmd.Wait()
		logFile.Close()
	}()

	now := flowdb.NowISO()
	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=?, auto_run_started=?, auto_run_log=?, updated_at=? WHERE slug=?`,
		pid, now, logPath, now, task.Slug,
	); err != nil {
		fmt.Fprintf(os.Stderr, "error: record auto_run: %v\n", err)
		return 1
	}
	fmt.Printf("Started headless run for %s (pid %d), logging to %s\n", task.Slug, pid, logPath)
	return 0
}

// refreshAutoRunStatus checks whether a task's recorded auto_run_pid is
// still alive and, if the row claims 'running' but the pid is gone,
// flips it to 'dead' (a crash/kill outside focusflow's control — not
// the same as 'completed', which only `focusflow done` sets). No-op for
// tasks with no in-flight auto run.
func refreshAutoRunStatus(db *sql.DB, task *flowdb.Task) error {
	if !task.AutoRunStatus.Valid || task.AutoRunStatus.String != "running" || !task.AutoRunPID.Valid {
		return nil
	}
	proc, err := os.FindProcess(int(task.AutoRunPID.Int64))
	if err != nil {
		return markAutoRunDead(db, task.Slug)
	}
	// On Unix, FindProcess always succeeds; signal 0 is the standard
	// liveness probe — it errors if the process doesn't exist.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return markAutoRunDead(db, task.Slug)
	}
	return nil
}

func markAutoRunDead(db *sql.DB, slug string) error {
	now := flowdb.NowISO()
	_, err := db.Exec(
		`UPDATE tasks SET auto_run_status='dead', auto_run_finished=?, updated_at=? WHERE slug=? AND auto_run_status='running'`,
		now, now, slug,
	)
	return err
}
