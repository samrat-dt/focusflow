package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"focusflow/internal/store"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cmdList dispatches `focusflow list tasks|projects|playbooks|runs`. Per spec §5.4.
func cmdList(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: list requires 'tasks', 'projects', 'playbooks', or 'runs'")
		return 2
	}
	switch args[0] {
	case "tasks":
		return listTasksCmd(args[1:])
	case "projects":
		return listProjectsCmd(args[1:])
	case "playbooks":
		return listPlaybooksCmd(args[1:])
	case "runs":
		return listRunsCmd(args[1:])
	case "tags":
		return listTagsCmd(args[1:])
	case "owners":
		return ownerList(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown list subcommand %q\n", args[0])
	return 2
}

func listTasksCmd(args []string) int {
	fs := flagSet("list tasks")
	status := fs.String("status", "", store.StatusUsage)
	project := fs.String("project", "", "project slug")
	priority := fs.String("priority", "", "high|medium|low")
	since := fs.String("since", "", "today|monday|7d|YYYY-MM-DD")
	includeArchived := fs.Bool("include-archived", false, "include archived tasks")
	includeDone := fs.Bool("include-done", false, "include done tasks (hidden by default)")
	kind := fs.String("kind", "regular", "filter by task kind: regular | playbook_run | all")
	format := fs.String("format", "table", "output format: table|json|tsv")
	var tagFilter stringSliceFlag
	fs.Var(&tagFilter, "tag", "only tasks carrying this tag (case-insensitive; repeatable — tasks must carry ALL given tags)")
	noColor := fs.Bool("no-color", false, "disable ANSI color even when stdout is a TTY")
	noTruncate := fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}

	if *status != "" && !store.IsValidStatus(*status) {
		fmt.Fprintf(os.Stderr, "error: --status must be %s, got %q\n", store.StatusUsage, *status)
		return 2
	}
	filter := store.TaskFilter{
		Status:          *status,
		Project:         *project,
		Priority:        *priority,
		IncludeArchived: *includeArchived,
		Tags:            []string(tagFilter),
	}
	// Default kind is "regular"; "all" disables the kind filter.
	if *kind != "all" {
		filter.Kind = *kind
	}
	// Hide done tasks by default. Skipped if --status is given (user
	// explicitly chose a status, including possibly "done") or if
	// --include-done is set.
	if *status == "" && !*includeDone {
		filter.ExcludeDone = true
	}
	if *since != "" {
		s, err := parseSince(*since, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --since: %v\n", err)
			return 2
		}
		filter.Since = s.Format(time.RFC3339)
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	tasks, err := store.ListTasks(db, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	// A `do --auto` run whose process has crashed or been killed still
	// reads auto_run_status='running' until something notices — refresh
	// it here so a stale 'running' never reaches the caller as a lie.
	for _, t := range tasks {
		if !t.AutoRunStatus.Valid || t.AutoRunStatus.String != "running" {
			continue
		}
		if err := refreshAutoRunStatus(db, t); err != nil {
			fmt.Fprintf(os.Stderr, "warning: refresh auto_run_status for %s: %v\n", t.Slug, err)
			continue
		}
		if err := db.QueryRow(`SELECT auto_run_status FROM tasks WHERE slug = ?`, t.Slug).Scan(&t.AutoRunStatus); err != nil {
			fmt.Fprintf(os.Stderr, "warning: re-read auto_run_status for %s: %v\n", t.Slug, err)
		}
	}

	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// json/tsv must stay valid, parseable output even when empty (an
	// empty JSON array, or just the TSV header) — the dashboard parses
	// this unconditionally, so "(no tasks)" prose would break it.
	if *format == "json" {
		return renderTasksJSON(tasks, db)
	}
	if *format == "tsv" {
		return renderTasksTSV(tasks, db, root)
	}
	if len(tasks) == 0 {
		fmt.Println("(no tasks)")
		return 0
	}

	now := time.Now()

	// Compute max slug+name width for alignment. We render
	// "<slug>  <name>" as the identity column; truncate later.
	type row struct {
		ident    string
		statusAb string
		pri      string
		project  string
		age      string
		due      string
		stale    string
		waiting  string
		archived bool
		done     bool
	}
	warnPrefix, boltPrefix := "⚠ ", "⚡ "
	if *noColor {
		warnPrefix, boltPrefix = "", ""
	}

	var rows []row
	maxIdent := 0
	for _, t := range tasks {
		ident := t.Slug
		if t.Name != "" && t.Name != t.Slug {
			ident = t.Slug
		}
		if n := len(ident); n > maxIdent {
			maxIdent = n
		}
		r := row{
			ident:    ident,
			statusAb: statusAbbrev(t.Status),
			pri:      priorityShort(t.Priority),
			archived: t.ArchivedAt.Valid,
			done:     t.Status == "done",
		}
		if t.ProjectSlug.Valid && t.ProjectSlug.String != "" {
			r.project = "(" + t.ProjectSlug.String + ")"
		}

		// Age: days in current status.
		if !t.ArchivedAt.Valid {
			if age := daysInStatus(t, now); age > 0 {
				r.age = fmt.Sprintf("%dd", age)
			}
		}

		// Due date indicator.
		if diff, ok := daysUntilDue(t, now); ok {
			switch {
			case diff < 0:
				r.due = fmt.Sprintf("%soverdue %dd", warnPrefix, -diff)
			case diff == 0:
				r.due = boltPrefix + "due today"
			case diff == 1:
				r.due = "due tomorrow"
			default:
				r.due = fmt.Sprintf("due %dd", diff)
			}
		}

		if t.Status == "in-progress" && !t.ArchivedAt.Valid {
			if days, ok := taskStaleness(t, root); ok {
				r.stale = fmt.Sprintf("%sstale (%dd)", warnPrefix, days)
			}
		}
		if t.WaitingOn.Valid && t.WaitingOn.String != "" {
			r.waiting = "[waiting: " + t.WaitingOn.String + "]"
		}
		rows = append(rows, r)
	}

	// Render each row. We align the ident column across all rows.
	identW := maxIdent
	if !*noTruncate && identW > 40 {
		identW = 40
	}
	if identW < 10 {
		identW = 10
	}
	for _, r := range rows {
		var sb strings.Builder
		sb.WriteString("  ")
		sb.WriteString("[")
		sb.WriteString(r.statusAb)
		sb.WriteString("] ")
		sb.WriteString(fmt.Sprintf("%-6s ", r.pri))
		ident := r.ident
		if !*noTruncate && len(ident) > identW {
			ident = ident[:identW]
		}
		sb.WriteString(fmt.Sprintf("%-*s ", identW, ident))
		if r.project != "" {
			sb.WriteString(fmt.Sprintf(" %-18s", r.project))
		} else {
			sb.WriteString(fmt.Sprintf(" %-18s", ""))
		}
		if r.age != "" {
			sb.WriteString(fmt.Sprintf("  %4s", r.age))
		} else {
			sb.WriteString("      ")
		}
		if r.due != "" {
			sb.WriteString("  ")
			sb.WriteString(r.due)
		}
		if r.stale != "" {
			sb.WriteString("  ")
			sb.WriteString(r.stale)
		}
		if r.waiting != "" {
			sb.WriteString("  ")
			sb.WriteString(r.waiting)
		}
		if r.archived {
			sb.WriteString("  (archived)")
		}
		fmt.Println(strings.TrimRight(sb.String(), " "))
	}
	return 0
}

// taskRowCommon computes the fields shared by JSON and TSV rendering
// that require extra DB/process lookups beyond what's on the Task
// struct itself: tags and liveness.
type taskRowCommon struct {
	tags []string
	live bool
}

// buildTaskRowCommon takes an already-fetched process list (from a
// single psLines() call shared across every task in a render) so that
// rendering many rows forks `ps` once, not once per task with a
// session_id.
func buildTaskRowCommon(db *sql.DB, t *store.Task, procLines []string) (taskRowCommon, error) {
	tags, err := store.ListTagsForTask(db, t.Slug)
	if err != nil {
		return taskRowCommon{}, err
	}
	live := t.SessionID.Valid && isSessionLiveIn(procLines, t.SessionID.String)
	return taskRowCommon{tags: tags, live: live}, nil
}

// autoRunDisplay renders the auto_run column:
// "" when never run, otherwise one of running|completed|dead.
func autoRunDisplay(t *store.Task) string {
	if !t.AutoRunStatus.Valid {
		return ""
	}
	return t.AutoRunStatus.String
}

// renderTasksJSON prints tasks as a JSON array with a fixed field set
// (the dashboard depends on it); there is deliberately no "kind"
// key, even though the Task struct has one — the output never includes it.
func renderTasksJSON(tasks []*store.Task, db *sql.DB) int {
	// Key order is fixed and optional keys are omitted when empty: live, auto_run,
	// tags and archived only appear when set.
	type jsonRow struct {
		Slug     string   `json:"slug"`
		Name     string   `json:"name"`
		Status   string   `json:"status"`
		Priority string   `json:"priority"`
		Project  string   `json:"project,omitempty"`
		Live     bool     `json:"live,omitempty"`
		AgeDays  int      `json:"age_days"`
		AutoRun  string   `json:"auto_run,omitempty"`
		Updated  string   `json:"updated"`
		Tags     []string `json:"tags,omitempty"`
		Archived bool     `json:"archived,omitempty"`
	}
	// Fork `ps` once for the whole render, not once per task — this list
	// can return hundreds of rows (the dashboard's own call passes
	// --include-done --include-archived --kind all).
	procLines, _ := psLines()
	now := time.Now()
	rows := make([]jsonRow, 0, len(tasks))
	for _, t := range tasks {
		common, err := buildTaskRowCommon(db, t, procLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		project := ""
		if t.ProjectSlug.Valid {
			project = t.ProjectSlug.String
		}
		tags := common.tags
		rows = append(rows, jsonRow{
			Slug: t.Slug, Name: t.Name, Status: t.Status, Priority: t.Priority,
			Project: project, Live: common.live, Updated: t.UpdatedAt, Tags: tags,
			Archived: t.ArchivedAt.Valid, AutoRun: autoRunDisplay(t),
			AgeDays: daysInStatus(t, now),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

// renderTasksTSV prints tasks as tab-separated rows matching live
// a fixed TSV header/column order (empty cells for false/unset values).
func renderTasksTSV(tasks []*store.Task, db *sql.DB, root string) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{
		"slug", "status", "priority", "project", "age_days", "due_in_days", "due_label",
		"stale", "stale_days", "waiting_on", "assignee", "live", "auto_run", "auto_run_pid",
		"archived", "tags",
	}, "\t"))
	procLines, _ := psLines()
	now := time.Now()
	for _, t := range tasks {
		common, err := buildTaskRowCommon(db, t, procLines)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		project := ""
		if t.ProjectSlug.Valid {
			project = t.ProjectSlug.String
		}
		dueInDays, dueLabel := "", ""
		if diff, ok := daysUntilDue(t, now); ok {
			dueInDays = fmt.Sprintf("%d", diff)
			switch {
			case diff < 0:
				dueLabel = "overdue"
			case diff == 0:
				dueLabel = "due today"
			default:
				dueLabel = "due"
			}
		}
		stale, staleDays := "", ""
		if t.Status == "in-progress" && !t.ArchivedAt.Valid {
			if days, ok := taskStaleness(t, root); ok {
				stale = "true"
				staleDays = fmt.Sprintf("%d", days)
			}
		}
		waitingOn := ""
		if t.WaitingOn.Valid {
			waitingOn = t.WaitingOn.String
		}
		assignee := ""
		if t.Assignee.Valid {
			assignee = t.Assignee.String
		}
		pid := ""
		if t.AutoRunPID.Valid {
			pid = fmt.Sprintf("%d", t.AutoRunPID.Int64)
		}
		fmt.Fprintln(w, strings.Join([]string{
			t.Slug, t.Status, t.Priority, project,
			fmt.Sprintf("%d", daysInStatus(t, now)), dueInDays, dueLabel,
			stale, staleDays, waitingOn, assignee,
			boolCell(common.live), autoRunDisplay(t), pid,
			boolCell(t.ArchivedAt.Valid), strings.Join(common.tags, ","),
		}, "\t"))
	}
	return 0
}

// listTagsCmd implements `focusflow list tags` — every distinct tag in
// use (across non-archived tasks) with its usage count.
func listTagsCmd(args []string) int {
	fs := flagSet("list tags")
	format := fs.String("format", "table", "output format: table|json|tsv")
	_ = fs.Bool("no-color", false, "disable ANSI color even when stdout is a TTY")
	_ = fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}
	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	tags, err := store.ListAllTags(db)
	// Busiest tags first (count desc, then name).
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Count > tags[j].Count })
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *format == "json" {
		return renderTagsJSON(tags)
	}
	if *format == "tsv" {
		return renderTagsTSV(tags)
	}
	if len(tags) == 0 {
		fmt.Println("(no tags)")
		return 0
	}
	for _, tc := range tags {
		fmt.Printf("  #%-20s %d task(s)\n", tc.Tag, tc.Count)
	}
	return 0
}

// renderTagsJSON prints tags as a JSON array of {tag, count}.
func renderTagsJSON(tags []store.TagCount) int {
	type jsonRow struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	rows := make([]jsonRow, 0, len(tags))
	for _, tc := range tags {
		rows = append(rows, jsonRow{Tag: tc.Tag, Count: tc.Count})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

// renderTagsTSV prints tags as tab-separated rows.
func renderTagsTSV(tags []store.TagCount) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{"tag", "count"}, "\t"))
	for _, tc := range tags {
		fmt.Fprintf(w, "%s\t%d\n", tc.Tag, tc.Count)
	}
	return 0
}

func listProjectsCmd(args []string) int {
	fs := flagSet("list projects")
	status := fs.String("status", "", "active|done")
	includeArchived := fs.Bool("include-archived", false, "include archived projects")
	format := fs.String("format", "table", "output format: table|json|tsv")
	_ = fs.Bool("no-color", false, "disable ANSI color even when stdout is a TTY")
	noTruncate := fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}
	filter := store.ProjectFilter{
		Status:          *status,
		IncludeArchived: *includeArchived,
	}
	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	projects, err := store.ListProjects(db, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *format == "json" {
		return renderProjectsJSON(projects, db)
	}
	if *format == "tsv" {
		return renderProjectsTSV(projects, db)
	}
	if len(projects) == 0 {
		fmt.Println("(no projects)")
		return 0
	}

	// Sort projects by priority (high, med, low) then slug. ListProjects
	// currently sorts by slug only, so reorder here.
	// A stable insertion sort is fine at the volumes we expect.
	sortedProjects := make([]*store.Project, len(projects))
	copy(sortedProjects, projects)
	priorityOrder := func(p string) int {
		switch p {
		case "high":
			return 0
		case "medium":
			return 1
		case "low":
			return 2
		}
		return 3
	}
	// Simple insertion sort for stability and small N.
	for i := 1; i < len(sortedProjects); i++ {
		for j := i; j > 0; j-- {
			a, b := sortedProjects[j-1], sortedProjects[j]
			if priorityOrder(b.Priority) < priorityOrder(a.Priority) {
				sortedProjects[j-1], sortedProjects[j] = b, a
			} else {
				break
			}
		}
	}

	maxSlug := 0
	for _, p := range sortedProjects {
		if n := len(p.Slug); n > maxSlug {
			maxSlug = n
		}
	}
	if !*noTruncate && maxSlug > 40 {
		maxSlug = 40
	}
	if maxSlug < 10 {
		maxSlug = 10
	}

	for _, p := range sortedProjects {
		counts, err := projectTaskCounts(db, p.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		statusW := "active"
		if p.Status != "" {
			statusW = p.Status
		}
		slug := p.Slug
		if !*noTruncate && len(slug) > maxSlug {
			slug = slug[:maxSlug]
		}

		label := fmt.Sprintf("%d tasks", counts.total)
		if counts.total == 1 {
			label = "1 task "
		}
		var segs []string
		if counts.next > 0 {
			segs = append(segs, fmt.Sprintf("%d NX", counts.next))
		}
		if counts.inProg > 0 {
			segs = append(segs, fmt.Sprintf("%d IP", counts.inProg))
		}
		if counts.paused > 0 {
			segs = append(segs, fmt.Sprintf("%d PA", counts.paused))
		}
		if counts.review > 0 {
			segs = append(segs, fmt.Sprintf("%d RV", counts.review))
		}
		if counts.backlog > 0 {
			segs = append(segs, fmt.Sprintf("%d BL", counts.backlog))
		}
		if counts.done > 0 {
			segs = append(segs, fmt.Sprintf("%d DN", counts.done))
		}
		breakdown := ""
		if len(segs) > 0 {
			breakdown = "(" + strings.Join(segs, ", ") + ")"
		}
		arch := ""
		if p.ArchivedAt.Valid {
			arch = "  (archived)"
		}
		fmt.Printf("  %-6s %-*s   %-7s %s %s%s\n",
			priorityShort(p.Priority), maxSlug, slug, statusW, label, breakdown, arch)
	}
	return 0
}

// renderProjectsJSON prints projects as a JSON array with per-status task
// counts; archived and other optional keys appear only when set.
func renderProjectsJSON(projects []*store.Project, db *sql.DB) int {
	type jsonRow struct {
		Slug       string `json:"slug"`
		Name       string `json:"name"`
		Priority   string `json:"priority"`
		Status     string `json:"status"`
		Total      int    `json:"total"`
		InProgress int    `json:"in_progress"`
		Backlog    int    `json:"backlog"`
		Next       int    `json:"next"`
		Paused     int    `json:"paused"`
		Review     int    `json:"review"`
		Done       int    `json:"done"`
		Archived   bool   `json:"archived,omitempty"`
		Updated    string `json:"updated"`
	}
	rows := make([]jsonRow, 0, len(projects))
	for _, p := range projects {
		counts, err := projectTaskCounts(db, p.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		rows = append(rows, jsonRow{
			Slug: p.Slug, Name: p.Name, Priority: p.Priority, Status: p.Status,
			Total: counts.total, InProgress: counts.inProg, Backlog: counts.backlog, Next: counts.next, Paused: counts.paused, Review: counts.review, Done: counts.done,
			Archived: p.ArchivedAt.Valid, Updated: p.UpdatedAt,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

// renderProjectsTSV prints projects with a fixed header.
func renderProjectsTSV(projects []*store.Project, db *sql.DB) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{"slug", "priority", "status", "total", "in_progress", "backlog", "next", "paused", "review", "done", "archived"}, "\t"))
	for _, p := range projects {
		counts, err := projectTaskCounts(db, p.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintln(w, strings.Join([]string{
			p.Slug, p.Priority, p.Status,
			fmt.Sprintf("%d", counts.total), fmt.Sprintf("%d", counts.inProg),
			fmt.Sprintf("%d", counts.backlog), fmt.Sprintf("%d", counts.next), fmt.Sprintf("%d", counts.paused),
			fmt.Sprintf("%d", counts.review), fmt.Sprintf("%d", counts.done),
			boolCell(p.ArchivedAt.Valid),
		}, "\t"))
	}
	return 0
}

func listPlaybooksCmd(args []string) int {
	fs := flagSet("list playbooks")
	project := fs.String("project", "", "filter by project slug")
	includeArchived := fs.Bool("include-archived", false, "include archived")
	format := fs.String("format", "table", "output format: table|json|tsv")
	_ = fs.Bool("no-color", false, "disable ANSI color even when stdout is a TTY")
	_ = fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	pbs, err := store.ListPlaybooks(db, store.PlaybookFilter{
		Project:         *project,
		IncludeArchived: *includeArchived,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *format == "json" {
		return renderPlaybooksJSON(pbs)
	}
	if *format == "tsv" {
		return renderPlaybooksTSV(pbs)
	}
	if len(pbs) == 0 {
		fmt.Println("(no playbooks)")
		return 0
	}
	for _, pb := range pbs {
		proj := ""
		if pb.ProjectSlug.Valid {
			proj = "(" + pb.ProjectSlug.String + ")"
		}
		archived := ""
		if pb.ArchivedAt.Valid {
			archived = "  (archived)"
		}
		fmt.Printf("  %-40s %s%s\n", pb.Slug, proj, archived)
	}
	return 0
}

// renderPlaybooksJSON prints playbooks as a JSON array.
func renderPlaybooksJSON(pbs []*store.Playbook) int {
	type jsonRow struct {
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Project  string `json:"project"`
		WorkDir  string `json:"work_dir"`
		Archived bool   `json:"archived"`
	}
	rows := make([]jsonRow, 0, len(pbs))
	for _, pb := range pbs {
		project := ""
		if pb.ProjectSlug.Valid {
			project = pb.ProjectSlug.String
		}
		rows = append(rows, jsonRow{
			Slug: pb.Slug, Name: pb.Name, Project: project,
			WorkDir: pb.WorkDir, Archived: pb.ArchivedAt.Valid,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

// renderPlaybooksTSV prints playbooks as tab-separated rows, using the
// same field set as renderPlaybooksJSON.
func renderPlaybooksTSV(pbs []*store.Playbook) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{"slug", "name", "project", "work_dir", "archived"}, "\t"))
	for _, pb := range pbs {
		project := ""
		if pb.ProjectSlug.Valid {
			project = pb.ProjectSlug.String
		}
		fmt.Fprintln(w, strings.Join([]string{
			pb.Slug, pb.Name, project, pb.WorkDir, fmt.Sprintf("%t", pb.ArchivedAt.Valid),
		}, "\t"))
	}
	return 0
}

func listRunsCmd(args []string) int {
	fs := flagSet("list runs")
	status := fs.String("status", "", store.StatusUsage)
	includeArchived := fs.Bool("include-archived", false, "include archived")
	format := fs.String("format", "table", "output format: table|json|tsv")
	_ = fs.Bool("no-color", false, "disable ANSI color even when stdout is a TTY")
	_ = fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}
	var playbookSlug string
	if fs.NArg() > 0 {
		playbookSlug = fs.Arg(0)
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	tasks, err := store.ListTasks(db, store.TaskFilter{
		Kind:            "playbook_run",
		PlaybookSlug:    playbookSlug,
		Status:          *status,
		IncludeArchived: *includeArchived,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *format == "json" {
		return renderRunsJSON(tasks)
	}
	if *format == "tsv" {
		return renderRunsTSV(tasks)
	}
	if len(tasks) == 0 {
		fmt.Println("(no runs)")
		return 0
	}
	for _, tk := range tasks {
		archived := ""
		if tk.ArchivedAt.Valid {
			archived = "  (archived)"
		}
		pbCol := ""
		if tk.PlaybookSlug.Valid {
			pbCol = "(" + tk.PlaybookSlug.String + ")"
		}
		fmt.Printf("  [%s] %-50s %s%s\n", statusAbbrev(tk.Status), tk.Slug, pbCol, archived)
	}
	return 0
}

// renderRunsJSON prints playbook runs as a JSON array.
func renderRunsJSON(tasks []*store.Task) int {
	type jsonRow struct {
		Slug     string `json:"slug"`
		Status   string `json:"status"`
		Playbook string `json:"playbook"`
		Archived bool   `json:"archived"`
	}
	rows := make([]jsonRow, 0, len(tasks))
	for _, tk := range tasks {
		playbook := ""
		if tk.PlaybookSlug.Valid {
			playbook = tk.PlaybookSlug.String
		}
		rows = append(rows, jsonRow{Slug: tk.Slug, Status: tk.Status, Playbook: playbook, Archived: tk.ArchivedAt.Valid})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

// renderRunsTSV prints playbook runs as tab-separated rows, using the
// same field set as renderRunsJSON.
func renderRunsTSV(tasks []*store.Task) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{"slug", "status", "playbook", "archived"}, "\t"))
	for _, tk := range tasks {
		playbook := ""
		if tk.PlaybookSlug.Valid {
			playbook = tk.PlaybookSlug.String
		}
		fmt.Fprintln(w, strings.Join([]string{tk.Slug, tk.Status, playbook, fmt.Sprintf("%t", tk.ArchivedAt.Valid)}, "\t"))
	}
	return 0
}

// ---------- helpers ----------

type taskCounts struct {
	total, inProg, backlog, done, next, paused, review int
}

func projectTaskCounts(db *sql.DB, projectSlug string) (taskCounts, error) {
	var c taskCounts
	rows, err := db.Query(
		`SELECT status, COUNT(*) FROM tasks
		 WHERE project_slug = ? AND archived_at IS NULL AND kind != 'project_agent'
		 GROUP BY status`, projectSlug)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return c, err
		}
		c.total += n
		switch s {
		case "in-progress":
			c.inProg += n
		case "backlog":
			c.backlog += n
		case "done":
			c.done += n
		case "next":
			c.next += n
		case "paused":
			c.paused += n
		case "review":
			c.review += n
		}
	}
	return c, rows.Err()
}

func statusAbbrev(status string) string {
	switch status {
	case "backlog":
		return "BL"
	case "next":
		return "NX"
	case "in-progress":
		return "IP"
	case "paused":
		return "PA"
	case "review":
		return "RV"
	case "done":
		return "DN"
	}
	return "??"
}

func priorityShort(p string) string {
	switch p {
	case "high":
		return "high"
	case "medium":
		return "med"
	case "low":
		return "low"
	}
	return p
}

// parseSince converts "today" / "monday" / "7d" / "YYYY-MM-DD" / "Nd"
// into an absolute time lower bound, interpreted in local time. `now` is
// passed in for testability.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	case "monday":
		// Start of the current week (Monday 00:00).
		wd := int(now.Weekday()) // Sunday = 0
		// Convert so Monday = 0, Sunday = 6.
		offset := (wd + 6) % 7
		y, mo, d := now.Date()
		start := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
		return start.AddDate(0, 0, -offset), nil
	}
	// Pattern "<N>d".
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(numStr, "%d", &n); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	// YYYY-MM-DD.
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized --since value %q (want today|monday|Nd|YYYY-MM-DD)", s)
}

// ensureUpdatesDir is a small utility used in tests to pre-create an
// updates directory. Kept here so tests can share it without exposing
// internals elsewhere.
func ensureUpdatesDir(root, kind, slug string) (string, error) {
	dir := filepath.Join(root, kind, slug, "updates")
	if err := os.MkdirAll(dir, privDir); err != nil {
		return "", err
	}
	return dir, nil
}

// boolCell renders a TSV boolean: "true" when set,
// empty when not.
func boolCell(b bool) string {
	if b {
		return "true"
	}
	return ""
}
