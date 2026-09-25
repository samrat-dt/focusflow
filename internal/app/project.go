package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"focusflow/internal/store"
)

// KindProjectAgent marks the hidden, permanent task that holds a project's
// one session. It is excluded from every default listing and from project
// task counts; the dashboard and the CLI address it through the project.
const KindProjectAgent = "project_agent"

func projectAgentSlug(projectSlug string) string { return projectSlug + "-agent" }

func coordinatorBrief(p *store.Project, projectBrief string) string {
	return fmt.Sprintf(`# %s — project coordinator

## What
You are the permanent coordinator session for the project "%s" (%s). Every message you get is a PROJECT-LEVEL ask, from the user via the dashboard. You keep one session forever; never ask for a fresh one.

## Each message
1. See what exists: focusflow list tasks --project %s --include-done
2. Decide where the ask belongs:
   - it fits an existing subtask: focusflow message user/<task> "<what that task should do>"
   - it is new work: focusflow add task "<name>" --project %s , then, if it should start now, run it unattended: focusflow do <task> --auto --with "<what to do>" (otherwise leave it in the backlog — the user starts it)
3. Tell the user what you decided: focusflow message user "<one or two sentences>" --reply-to <id of the message you are answering>
4. Delegate. Do not implement large changes yourself in this session.
5. You are permanent: never run focusflow done on yourself.

## Project
work_dir: %s

%s
`, p.Name, p.Name, p.Slug, p.Slug, p.Slug, p.WorkDir, strings.TrimSpace(projectBrief))
}

// ensureProjectAgent creates (idempotently) the project's coordinator task
// and links it from the project row. Returns its slug.
func ensureProjectAgent(db *sql.DB, root string, p *store.Project) (string, bool, error) {
	if p.AgentTaskSlug.Valid && p.AgentTaskSlug.String != "" {
		if _, err := store.GetTask(db, p.AgentTaskSlug.String); err == nil {
			return p.AgentTaskSlug.String, false, nil
		}
	}
	slug := projectAgentSlug(p.Slug)
	now := store.NowISO()
	if _, err := store.GetTask(db, slug); err != nil {
		if _, err := db.Exec(`INSERT INTO tasks (slug, name, project_slug, status, kind, priority, work_dir, status_changed_at, created_at, updated_at)
			VALUES (?, ?, ?, 'backlog', ?, 'medium', ?, ?, ?, ?)`,
			slug, p.Name+" — coordinator", p.Slug, KindProjectAgent, p.WorkDir, now, now, now); err != nil {
			return "", false, fmt.Errorf("create project agent: %w", err)
		}
	}
	dir := filepath.Join(root, "tasks", slug)
	if err := os.MkdirAll(filepath.Join(dir, "updates"), privDir); err != nil {
		return "", false, err
	}
	projectBrief, _ := os.ReadFile(filepath.Join(root, "projects", p.Slug, "brief.md"))
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(coordinatorBrief(p, string(projectBrief))), privFile); err != nil {
		return "", false, err
	}
	if _, err := db.Exec(`UPDATE projects SET agent_task_slug = ?, updated_at = ? WHERE slug = ?`, slug, now, p.Slug); err != nil {
		return "", false, err
	}
	return slug, true, nil
}

func cmdProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: project requires a subcommand (agent, message, pause, schedule, unschedule, runs)")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "agent", "message", "pause", "schedule", "unschedule", "runs":
	default:
		fmt.Fprintf(os.Stderr, "error: unknown project subcommand %q\n", sub)
		return 2
	}
	if len(rest) < 1 || looksLikeFlag(rest[0]) {
		fmt.Fprintf(os.Stderr, "error: project %s requires a project slug\n", sub)
		return 2
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	p, err := ResolveProject(db, strings.TrimPrefix(rest[0], "project/"), false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: project %q not found\n", rest[0])
		return 1
	}
	switch sub {
	case "agent":
		slug, created, err := ensureProjectAgent(db, root, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if created {
			fmt.Fprintf(os.Stderr, "created coordinator task %s\n", slug)
		}
		fmt.Println(slug)
		return 0
	case "pause":
		return pauseProject(db, p.Slug)
	case "message":
		return projectMessage(db, root, p, rest[1:])
	case "schedule":
		return projectSchedule(db, root, p, rest[1:])
	case "unschedule":
		return ownerSetActive([]string{projectAgentSlug(p.Slug) + "-owner"}, false)
	case "runs":
		return projectRuns(db, p, rest[1:])
	}
	return 2
}

// projectRuns implements `project runs <slug> [--on | --off] [--limit <minutes> | --no-limit] [--json]`:
// whether background runs may start in this project, and how long one may run before it is stopped.
// With no flags it prints the current policy.
func projectRuns(db *sql.DB, p *store.Project, args []string) int {
	fs := flagSet("project runs")
	on := fs.Bool("on", false, "allow background runs")
	off := fs.Bool("off", false, "do not start background runs in this project")
	limit := fs.Int("limit", 0, "stop a background run after this many minutes")
	noLimit := fs.Bool("no-limit", false, "remove the time limit")
	asJSON := fs.Bool("json", false, "print the policy as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *on && *off {
		fmt.Fprintln(os.Stderr, "error: --on and --off are opposites")
		return 2
	}
	if *limit < 0 || *limit > 24*60 || (*limit > 0 && *noLimit) {
		fmt.Fprintln(os.Stderr, "error: --limit must be 1 to 1440 minutes (or use --no-limit)")
		return 2
	}
	var onPtr *bool
	if *on || *off {
		v := *on
		onPtr = &v
	}
	var limPtr *int
	if *limit > 0 || *noLimit {
		v := *limit
		limPtr = &v
	}
	if onPtr != nil || limPtr != nil {
		if err := store.SetRunPolicy(db, p.Slug, onPtr, limPtr); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	pol, err := store.GetRunPolicy(db, p.Slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *asJSON {
		return printJSON(map[string]any{"project": p.Slug, "on": pol.On, "limit_minutes": pol.LimitMinutes})
	}
	state := "on"
	if !pol.On {
		state = "off"
	}
	lim := "no time limit"
	if pol.LimitMinutes > 0 {
		lim = fmt.Sprintf("stopped after %d minutes", pol.LimitMinutes)
	}
	fmt.Printf("Background runs in %s: %s (%s)\n", p.Slug, state, lim)
	return 0
}

// projectMessage implements `project message <slug> "<text>" [--fanout] [--urgent]`:
// the text goes to the project's coordinator (created on first use); with
// --fanout it is also queued to every open subtask so each session sees it.
func projectMessage(db *sql.DB, root string, p *store.Project, args []string) int {
	fs := flagSet("project message")
	fanout := fs.Bool("fanout", false, "also queue the message to every open subtask")
	urgent := fs.Bool("urgent", false, "mark the messages urgent")
	subtasksOnly := fs.Bool("subtasks-only", false, "fan out to the open subtasks WITHOUT messaging the coordinator (implies --fanout)")
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		fmt.Fprintln(os.Stderr, "error: usage: focusflow project message <slug> \"<text>\" [--fanout] [--urgent]")
		return 2
	}
	text := positional[0]
	agent, _, err := ensureProjectAgent(db, root, p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	send := func(toTask, body string) (string, error) {
		id, err := newMessageID()
		if err != nil {
			return "", err
		}
		m := &store.BusMessage{ID: id, FromAssignee: "user", ToAssignee: "user",
			ToTaskSlug: sql.NullString{String: toTask, Valid: true}, Body: body, Urgent: *urgent, CreatedAt: time.Now().Format(time.RFC3339)}
		return id, store.InsertBusMessage(db, m)
	}
	if *subtasksOnly {
		*fanout = true
	} else {
		id, err := send(agent, text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("Message sent to %s [%s]\n", agent, id)
	}
	if *fanout {
		rows, err := db.Query(`SELECT slug FROM tasks WHERE project_slug = ? AND archived_at IS NULL AND status != 'done' AND kind = 'regular' ORDER BY slug`, p.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		var slugs []string
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				slugs = append(slugs, s)
			}
		}
		rows.Close()
		for _, s := range slugs {
			if _, err := send(s, fmt.Sprintf("[from project %s] %s", p.Slug, text)); err != nil {
				fmt.Fprintf(os.Stderr, "error: %s: %v\n", s, err)
				return 1
			}
		}
		fmt.Printf("Fanned out to %d open subtask(s)\n", len(slugs))
	}
	return 0
}

// projectSchedule creates and starts an owner that reviews the project on a
// cadence — a scheduled coordinator (see `focusflow owner`).
func projectSchedule(db *sql.DB, root string, p *store.Project, args []string) int {
	fs := flagSet("project schedule")
	every := fs.String("every", "1d", "cadence, e.g. 12h, 1d, 1w")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := parseEvery(*every); err != nil {
		fmt.Fprintf(os.Stderr, "error: --every: %v\n", err)
		return 2
	}
	slug := projectAgentSlug(p.Slug) + "-owner"
	if _, err := store.GetOwner(db, slug); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already has a scheduled coordinator (%s); use `focusflow owner` to change it\n", p.Slug, slug)
		return 1
	}
	if rc := quietly(func() int {
		return addOwner([]string{p.Name + " coordinator", "--slug", slug, "--work-dir", p.WorkDir, "--every", *every, "--project", p.Slug})
	}); rc != 0 {
		return rc
	}
	charter := fmt.Sprintf(`# %s coordinator (scheduled)

## What
Review the project "%s" (%s) on a schedule and keep it moving.

## Each tick
- focusflow list tasks --project %s --include-done — see the shape of the work.
- Nudge anything stalled or in review too long: focusflow message user/<task> "<nudge>".
- If the backlog has an obvious next step, move it: focusflow next <task>.
- Summarize what changed and what needs the user in the journal, and message the user only if a decision is needed.

## Escalate when
- Something is blocked on a decision only the user can make (park it as a task tagged question).

## Out of scope
- Starting large work unattended; that is the user's call.
`, p.Name, p.Name, p.Slug, p.Slug)
	os.WriteFile(filepath.Join(root, "owners", slug, "brief.md"), []byte(charter), privFile)
	if rc := quietly(func() int { return ownerSetActive([]string{slug}, true) }); rc != 0 {
		return rc
	}
	fmt.Printf("scheduled coordinator %s every %s (owner scheduler must be installed: focusflow owner scheduler install)\n", slug, *every)
	return 0
}
