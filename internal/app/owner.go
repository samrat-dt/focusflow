package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"focusflow/internal/store"
)

// ---------- durations and times ----------

var everyRe = regexp.MustCompile(`^(\d+)([smhdw])$`)

// parseEvery parses a cadence like "30m", "2h", "1d", "1w" (or any Go
// duration such as "1h30m"). It must be between 1 minute and 90 days.
func parseEvery(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if m := everyRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		d = time.Duration(n) * unit
	} else if pd, err := time.ParseDuration(s); err == nil {
		d = pd
	} else {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 30m, 2h, 1d, 1w)", s)
	}
	if d < time.Minute || d > 90*24*time.Hour {
		return 0, fmt.Errorf("duration %q must be between 1m and 90d", s)
	}
	return d, nil
}

// parseWhen parses an absolute wake time: RFC3339, "YYYY-MM-DD[ HH:MM]",
// or "today|tomorrow [HH:MM]" (default 09:00) in local time.
func parseWhen(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc := now.Location()
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t.Add(9 * time.Hour), nil
	}
	fields := strings.Fields(strings.ToLower(s))
	if len(fields) >= 1 && (fields[0] == "today" || fields[0] == "tomorrow") && len(fields) <= 2 {
		day := now
		if fields[0] == "tomorrow" {
			day = now.AddDate(0, 0, 1)
		}
		hh, mm := 9, 0
		if len(fields) == 2 {
			if _, err := fmt.Sscanf(fields[1], "%d:%d", &hh, &mm); err != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
				return time.Time{}, fmt.Errorf("invalid time of day %q (use HH:MM)", fields[1])
			}
		}
		return time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q (use RFC3339, 'YYYY-MM-DD HH:MM', or 'tomorrow 09:00')", s)
}

// ---------- dispatch ----------

func cmdOwner(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: owner requires a subcommand (list, show, start, pause, tick, tick-due, next, retire, scheduler)")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "list":
		return ownerList(rest)
	case "show":
		return ownerShow(rest)
	case "start":
		return ownerSetActive(rest, true)
	case "pause":
		return ownerSetActive(rest, false)
	case "retire":
		return ownerRetire(rest)
	case "next":
		return ownerNext(rest)
	case "tick":
		return ownerTick(rest)
	case "tick-due":
		return ownerTickDue(rest)
	case "scheduler":
		return ownerScheduler(rest)
	}
	fmt.Fprintf(os.Stderr, "error: unknown owner subcommand %q\n", args[0])
	return 2
}

func openOwnerDB() (*sql.DB, error) {
	p, err := dataDBPath()
	if err != nil {
		return nil, err
	}
	return store.OpenDB(p)
}

func loadOwner(db *sql.DB, args []string, verb string) (*store.Owner, int) {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintf(os.Stderr, "error: owner %s requires an owner slug\n", verb)
		return nil, 2
	}
	o, err := store.GetOwner(db, args[0])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fmt.Fprintf(os.Stderr, "error: owner %q not found\n", args[0])
			return nil, 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	return o, 0
}

// ---------- add owner ----------

const ownerCharterStub = `# %s

## What
<one sentence: what this owner is responsible for>

## Why
<why it should run on its own, and what "good" looks like>

## Where
work_dir: %s

## Each tick
- <what to check or do every time it wakes>

## Escalate when
- <signals that need a human — the owner asks via a task tagged ` + "`question`" + `>

## Out of scope
- <what it must never do on its own>
`

// addOwner implements `focusflow add owner "<name>" --work-dir <path>
// --every <dur> [--slug <s>] [--project <slug>] [--mkdir]`. New owners
// start paused; `focusflow owner start <slug>` begins ticking.
func addOwner(args []string) int {
	if len(args) == 0 || args[0] == "" || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: add owner requires a name")
		return 2
	}
	name := args[0]
	fs := flagSet("add owner")
	workDir := fs.String("work-dir", "", "absolute path of the owner's work directory (required)")
	every := fs.String("every", "", "fallback tick cadence, e.g. 30m, 2h, 1d (required)")
	slugFlag := fs.String("slug", "", "short slug (default derived from the name)")
	project := fs.String("project", "", "project slug to attach")
	mkdir := fs.Bool("mkdir", false, "create --work-dir if it does not exist")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *workDir == "" || *every == "" {
		fmt.Fprintln(os.Stderr, "error: add owner requires --work-dir and --every")
		return 2
	}
	if _, err := parseEvery(*every); err != nil {
		fmt.Fprintf(os.Stderr, "error: --every: %v\n", err)
		return 2
	}
	abs, err := resolveWorkDir(*workDir, *mkdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	var projectSlug sql.NullString
	if *project != "" {
		p, err := store.GetProject(db, *project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: project %q not found\n", *project)
			return 1
		}
		projectSlug = sql.NullString{String: p.Slug, Valid: true}
	}
	slug := *slugFlag
	if slug == "" {
		base, err := Slugify(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		if slug, err = uniqueSlug(db, "owners", base); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	if err := store.InsertOwner(db, &store.Owner{Slug: slug, Name: name, WorkDir: abs, ProjectSlug: projectSlug, Every: *every}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	dir := filepath.Join(root, "owners", slug)
	if err := os.MkdirAll(filepath.Join(dir, "updates"), privDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	brief := filepath.Join(dir, "brief.md")
	if _, err := os.Stat(brief); os.IsNotExist(err) {
		if err := os.WriteFile(brief, []byte(fmt.Sprintf(ownerCharterStub, name, abs)), privFile); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	fmt.Printf("Created owner %q (paused) at %s\n", slug, dir)
	fmt.Printf("Next: edit the charter (%s), then `focusflow owner start %s`\n", brief, slug)
	return 0
}

// ---------- list / show ----------

func relTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "-"
	}
	d := t.Sub(now)
	if d >= 0 {
		return "in " + shortDur(d)
	}
	return shortDur(-d) + " ago"
}

func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func ownerList(args []string) int {
	fs := flagSet("owner list")
	format := fs.String("format", "table", "table|json")
	all := fs.Bool("include-archived", false, "include retired owners")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	owners, err := store.ListOwners(db, *all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *format == "json" {
		type row struct {
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			Every    string `json:"every"`
			NextWake string `json:"next_wake_at,omitempty"`
			LastTick string `json:"last_tick_at,omitempty"`
		}
		rows := []row{}
		for _, o := range owners {
			rows = append(rows, row{o.Slug, o.Name, o.Status, o.Every, o.NextWakeAt.String, o.LastTickAt.String})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
		return 0
	}
	if len(owners) == 0 {
		fmt.Println(`No owners. Create one with: focusflow add owner "<name>" --work-dir <path> --every <dur>`)
		return 0
	}
	now := time.Now()
	for _, o := range owners {
		next := "-"
		if o.Status == "active" {
			next = "now"
			if o.NextWakeAt.Valid {
				next = relTime(o.NextWakeAt.String, now)
			}
		}
		last := "never"
		if o.LastTickAt.Valid {
			last = relTime(o.LastTickAt.String, now)
		}
		fmt.Printf("  %-8s %-24s every %-5s next: %-9s last: %s\n", o.Status, o.Slug, o.Every, next, last)
	}
	return 0
}

func ownerShow(args []string) int {
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	o, rc := loadOwner(db, args, "show")
	if o == nil {
		return rc
	}
	root, _ := dataRoot()
	now := time.Now()
	fmt.Printf("slug:        %s\nname:        %s\nstatus:      %s\nevery:       %s\nwork_dir:    %s\n", o.Slug, o.Name, o.Status, o.Every, o.WorkDir)
	if o.ProjectSlug.Valid {
		fmt.Printf("project:     %s\n", o.ProjectSlug.String)
	}
	if o.NextWakeAt.Valid {
		fmt.Printf("next tick:   %s (%s)\n", o.NextWakeAt.String, relTime(o.NextWakeAt.String, now))
	}
	if o.LastTickAt.Valid {
		fmt.Printf("last tick:   %s (%s) [%s]\n", o.LastTickAt.String, relTime(o.LastTickAt.String, now), o.LastTickStatus.String)
	}
	fmt.Printf("charter:     %s\n", filepath.Join(root, "owners", o.Slug, "brief.md"))
	fmt.Printf("journal:     %s\n", filepath.Join(root, "owners", o.Slug, "updates"))
	rows, err := db.Query(`SELECT t.slug, t.status FROM tasks t JOIN task_tags g ON g.task_slug = t.slug
		WHERE g.tag = ? AND t.archived_at IS NULL AND t.status != 'done' ORDER BY t.created_at DESC`, "owner:"+o.Slug)
	if err == nil {
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var s, st string
			if rows.Scan(&s, &st) == nil {
				lines = append(lines, fmt.Sprintf("  - %s [%s]", s, st))
			}
		}
		if len(lines) > 0 {
			fmt.Printf("in flight:\n%s\n", strings.Join(lines, "\n"))
		} else {
			fmt.Println("in flight:   (nothing)")
		}
	}
	return 0
}

// ---------- state changes ----------

func ownerSetActive(args []string, active bool) int {
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	verb := "pause"
	if active {
		verb = "start"
	}
	o, rc := loadOwner(db, args, verb)
	if o == nil {
		return rc
	}
	if active {
		if err := store.SetOwnerStatus(db, o.Slug, "active", time.Now().Format(time.RFC3339)); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		// A retired owner is reactivated: clear archived_at.
		db.Exec(`UPDATE owners SET archived_at = NULL WHERE slug = ?`, o.Slug)
		fmt.Printf("%s is active — first tick is due now, then every %s (fallback; the owner self-paces via `owner next`).\n", o.Slug, o.Every)
		fmt.Println("Ticks fire from the scheduler: `focusflow owner scheduler install`, or wake it now with `focusflow owner tick " + o.Slug + "`.")
		return 0
	}
	if err := store.SetOwnerStatus(db, o.Slug, "paused", ""); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s paused (state kept).\n", o.Slug)
	return 0
}

func ownerRetire(args []string) int {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: owner retire requires an owner slug")
		return 2
	}
	fs := flagSet("owner retire")
	del := fs.Bool("delete", false, "also delete the row and its directory")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	o, rc := loadOwner(db, args[:1], "retire")
	if o == nil {
		return rc
	}
	root, _ := dataRoot()
	if *del {
		if err := store.DeleteOwner(db, o.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		os.RemoveAll(filepath.Join(root, "owners", o.Slug))
		fmt.Printf("%s deleted (row and directory).\n", o.Slug)
		return 0
	}
	if err := store.SetOwnerStatus(db, o.Slug, "retired", ""); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s retired — it will no longer tick. Files preserved under owners/%s/ (use --delete to remove entirely; `owner start` reactivates).\n", o.Slug, o.Slug)
	return 0
}

// ownerNext implements `owner next <slug> --in <dur> | --at <when>` — how
// a tick self-paces its own next wake-up.
func ownerNext(args []string) int {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: owner next requires an owner slug")
		return 2
	}
	fs := flagSet("owner next")
	in := fs.String("in", "", "wake after this duration (30m, 2h, 1d)")
	at := fs.String("at", "", "wake at this time (RFC3339, 'YYYY-MM-DD HH:MM', 'tomorrow 09:00')")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if (*in == "") == (*at == "") {
		fmt.Fprintln(os.Stderr, "error: give exactly one of --in or --at")
		return 2
	}
	now := time.Now()
	var when time.Time
	if *in != "" {
		d, err := parseEvery(*in)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --in: %v\n", err)
			return 2
		}
		when = now.Add(d)
	} else {
		t, err := parseWhen(*at, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --at: %v\n", err)
			return 2
		}
		if !t.After(now) {
			fmt.Fprintln(os.Stderr, "error: --at must be in the future")
			return 2
		}
		when = t
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	o, rc := loadOwner(db, args[:1], "next")
	if o == nil {
		return rc
	}
	if err := store.SetOwnerNextWake(db, o.Slug, when.Format(time.RFC3339)); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s next tick: %s (%s)\n", o.Slug, when.Format(time.RFC3339), relTime(when.Format(time.RFC3339), now))
	return 0
}

// ---------- ticking ----------

func buildTickBrief(o *store.Owner, root, tickSlug, charter string, now time.Time) string {
	journal := filepath.Join(root, "owners", o.Slug, "updates")
	return fmt.Sprintf(`# %s — tick %s

## What
Run ONE tick of the owner "%s" (%s). You are this owner's agent for this wake-up.

## Charter
%s

## Each tick
1. Read the charter above and the journal, newest first: %s
2. See what you already own: focusflow list tasks --tag owner:%s --include-done
3. Decide what needs doing and act. Delegate real work as tasks: focusflow add task "<what>" --tag owner:%s , then focusflow do <task> --auto . If a human must decide, add a task tagged question and owner:%s instead of guessing.
4. Write a journal entry %s/%s-tick.md : what you saw, what you did, what is next.
5. Self-pace: focusflow owner next %s --in <duration> (fallback cadence: %s).
6. Finish with: focusflow done %s

---
*Owner ticks run unattended when scheduled; never wait for input you cannot get.*
`, o.Name, now.Format("2006-01-02 15:04"), o.Name, o.Slug, strings.TrimSpace(charter), journal, o.Slug, o.Slug, o.Slug,
		journal, now.Format("2006-01-02"), o.Slug, o.Every, tickSlug)
}

// quietly runs fn with stdout discarded (used to reuse commands that
// print progress lines).
func quietly(fn func() int) int {
	old := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fn()
	}
	os.Stdout = devnull
	defer func() { os.Stdout = old; devnull.Close() }()
	return fn()
}

// tickOwner spawns one tick task for o and starts it (headless when auto).
func tickOwner(db *sql.DB, o *store.Owner, auto bool) error {
	every, err := parseEvery(o.Every)
	if err != nil {
		return err
	}
	root, err := dataRoot()
	if err != nil {
		return err
	}
	now := time.Now()
	charter, _ := os.ReadFile(filepath.Join(root, "owners", o.Slug, "brief.md"))
	tickSlug := fmt.Sprintf("%s-tick-%s", o.Slug, now.Format("20060102-150405"))
	args := []string{"task", fmt.Sprintf("%s — tick %s", o.Name, now.Format("2006-01-02 15:04")),
		"--slug", tickSlug, "--work-dir", o.WorkDir, "--tag", "owner:" + o.Slug}
	if o.ProjectSlug.Valid {
		args = append(args, "--project", o.ProjectSlug.String)
	}
	if rc := quietly(func() int { return cmdAdd(args) }); rc != 0 {
		return fmt.Errorf("could not create the tick task (rc=%d)", rc)
	}
	brief := filepath.Join(root, "tasks", tickSlug, "brief.md")
	if err := os.WriteFile(brief, []byte(buildTickBrief(o, root, tickSlug, string(charter), now)), privFile); err != nil {
		return err
	}
	next := now.Add(every).Format(time.RFC3339)
	if err := store.RecordOwnerTick(db, o.Slug, "running", next); err != nil {
		return err
	}
	doArgs := []string{tickSlug}
	if auto {
		doArgs = append(doArgs, "--auto")
	}
	if rc := cmdDo(doArgs); rc != 0 {
		db.Exec(`UPDATE owners SET last_tick_status = 'failed' WHERE slug = ?`, o.Slug)
		return fmt.Errorf("could not start the tick session (rc=%d)", rc)
	}
	return nil
}

func ownerTick(args []string) int {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: owner tick requires an owner slug")
		return 2
	}
	fs := flagSet("owner tick")
	auto := fs.Bool("auto", false, "run headlessly instead of opening a tab")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	o, rc := loadOwner(db, args[:1], "tick")
	if o == nil {
		return rc
	}
	if o.Status == "retired" {
		fmt.Fprintf(os.Stderr, "error: owner %q is retired (use `owner start` to reactivate)\n", o.Slug)
		return 1
	}
	if err := tickOwner(db, o, *auto); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("ticked %s\n", o.Slug)
	return 0
}

// tickStillRunning reports whether the owner's latest tick task is an
// unfinished headless run, so the scheduler never stacks ticks.
func tickStillRunning(db *sql.DB, o *store.Owner) bool {
	var slug string
	err := db.QueryRow(`SELECT t.slug FROM tasks t JOIN task_tags g ON g.task_slug = t.slug
		WHERE g.tag = ? AND t.slug LIKE ? AND t.status != 'done' AND t.auto_run_status = 'running'
		ORDER BY t.created_at DESC LIMIT 1`, "owner:"+o.Slug, o.Slug+"-tick-%").Scan(&slug)
	if err != nil {
		return false
	}
	task, err := store.GetTask(db, slug)
	if err != nil {
		return false
	}
	_ = refreshAutoRunStatus(db, task) // marks dead if the pid is gone
	task, err = store.GetTask(db, slug)
	return err == nil && task.AutoRunStatus.Valid && task.AutoRunStatus.String == "running"
}

// ownerTickDue is what the scheduler runs each minute: every active owner
// whose next wake time has passed gets a headless tick.
func ownerTickDue(args []string) int {
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	owners, err := store.ListOwners(db, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	now := time.Now()
	dispatched, failed := 0, 0
	for _, o := range owners {
		if o.Status != "active" {
			continue
		}
		if o.NextWakeAt.Valid {
			if t, err := time.Parse(time.RFC3339, o.NextWakeAt.String); err == nil && t.After(now) {
				continue
			}
		}
		if tickStillRunning(db, o) {
			continue
		}
		if err := tickOwner(db, o, true); err != nil {
			fmt.Fprintf(os.Stderr, "owner %s: %v\n", o.Slug, err)
			failed++
			continue
		}
		dispatched++
	}
	fmt.Printf("dispatched %d owner tick(s)\n", dispatched)
	if failed > 0 {
		return 1
	}
	return 0
}

// ---------- scheduler (opt-in launchd job) ----------

const schedulerLabel = "local.focusflow.owner-scheduler"

// launchctl is indirected so tests never touch the real launchd.
var launchctl = func(args ...string) ([]byte, error) { return exec.Command("launchctl", args...).CombinedOutput() }

func schedulerPlist(bin, root string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>owner</string><string>tick-due</string></array>
  <key>StartInterval</key><integer>60</integer>
  <key>RunAtLoad</key><true/>
  <key>EnvironmentVariables</key>
  <dict><key>FOCUSFLOW_ROOT</key><string>%s</string></dict>
  <key>StandardOutPath</key><string>%s/owner-scheduler.log</string>
  <key>StandardErrorPath</key><string>%s/owner-scheduler.err.log</string>
</dict>
</plist>
`, schedulerLabel, bin, root, root, root)
}

func schedulerPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", schedulerLabel+".plist"), nil
}

func ownerScheduler(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	plist, err := schedulerPlistPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	uid := strconv.Itoa(os.Getuid())
	switch sub {
	case "install":
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		bin, _ = filepath.EvalSymlinks(bin)
		root, err := dataRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(plist, []byte(schedulerPlist(bin, root)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		launchctl("bootout", "gui/"+uid+"/"+schedulerLabel) // replace any running copy
		if out, err := launchctl("bootstrap", "gui/"+uid, plist); err != nil {
			fmt.Fprintf(os.Stderr, "error: launchctl bootstrap: %v: %s\n", err, strings.TrimSpace(string(out)))
			return 1
		}
		fmt.Printf("installed %s — owners tick automatically (every minute it checks who is due)\n", plist)
		return 0
	case "uninstall":
		launchctl("bootout", "gui/"+uid+"/"+schedulerLabel)
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("scheduler removed; owners keep their state but no longer tick on their own")
		return 0
	case "status":
		if _, err := os.Stat(plist); err != nil {
			fmt.Println("scheduler: not installed")
			return 0
		}
		if _, err := launchctl("print", "gui/"+uid+"/"+schedulerLabel); err != nil {
			fmt.Println("scheduler: installed but not loaded")
			return 0
		}
		fmt.Println("scheduler: installed and loaded")
		return 0
	}
	fmt.Fprintln(os.Stderr, "error: usage: focusflow owner scheduler install|uninstall|status")
	return 2
}
