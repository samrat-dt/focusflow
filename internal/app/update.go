package app

import (
	"database/sql"
	"errors"
	"fmt"
	"focusflow/internal/store"
	"os"
	"strings"
	"time"
)

// cmdUpdate dispatches `focusflow update <kind>`. v1 supports `task` only;
// the command exists as a manual escape hatch when the work_dir drifts
// from what the DB has (e.g. the user moved a repo on disk), and as the
// single mutation surface for status/priority/assignee/due/waiting/tags
// (one flag-based update model).
func cmdUpdate(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: update requires 'task' and a ref")
		return 2
	}
	switch args[0] {
	case "task":
		return cmdUpdateTask(args[1:])
	case "project":
		return cmdUpdateProject(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown update target %q (expected 'task' or 'project')\n", args[0])
	return 2
}

// cmdUpdateProject implements `focusflow update project <ref> [--priority
// high|medium|low]`. At least one field-changing flag must be given.
func cmdUpdateProject(args []string) int {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: update project requires a project ref")
		return 2
	}
	ref := args[0]
	fs := flagSet("update project")
	priority := fs.String("priority", "", "high|medium|low")
	name := fs.String("name", "", "new display name (the slug never changes)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *priority != "" && !isValidPriority(*priority) {
		fmt.Fprintf(os.Stderr, "error: --priority must be high|medium|low, got %q\n", *priority)
		return 2
	}
	if *name != "" && strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "error: --name must not be blank")
		return 2
	}
	if *priority == "" && *name == "" {
		fmt.Fprintln(os.Stderr, "error: give at least one field to update")
		return 2
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

	project, err := ResolveProject(db, ref, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fmt.Fprintf(os.Stderr, "error: project %q not found\n", ref)
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	now := store.NowISO()
	if *priority != "" {
		if _, err := db.Exec(`UPDATE projects SET priority=?, updated_at=? WHERE slug=?`, *priority, now, project.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update priority: %v\n", err)
			return 1
		}
		fmt.Printf("priority → %s\n", *priority)
	}
	if *name != "" {
		if _, err := db.Exec(`UPDATE projects SET name=?, updated_at=? WHERE slug=?`, strings.TrimSpace(*name), now, project.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update name: %v\n", err)
			return 1
		}
		fmt.Printf("name → %s\n", strings.TrimSpace(*name))
	}
	return 0
}

// cmdUpdateTask implements `focusflow update task <ref> [--status ...]
// [--priority ...] [--assignee ...] [--clear-assignee] [--due-date ...]
// [--clear-due] [--waiting ...] [--clear-waiting] [--tag ...]
// [--remove-tag ...] [--clear-tags] [--work-dir <path>] [--mkdir]
// [--name <new>] [--project <slug> | --clear-project]`. At
// least one field-changing flag must be given. Session binding is owned
// solely by `focusflow do`/`focusflow do --here` — there is no
// --session-id flag here (a session id belongs to at most one task).
func cmdUpdateTask(args []string) int {
	if len(args) < 1 || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: update task requires a task ref")
		return 2
	}
	ref := args[0]
	fs := flagSet("update task")
	workDir := fs.String("work-dir", "", "new absolute work directory")
	mkdir := fs.Bool("mkdir", false, "create --work-dir if it does not exist")
	status := fs.String("status", "", store.StatusUsage)
	priority := fs.String("priority", "", "high|medium|low")
	assignee := fs.String("assignee", "", "person or agent to assign")
	clearAssignee := fs.Bool("clear-assignee", false, "remove the assignee")
	dueDate := fs.String("due-date", "", "due date (YYYY-MM-DD, today, tomorrow, monday, 3d)")
	clearDue := fs.Bool("clear-due", false, "remove the due date")
	waiting := fs.String("waiting", "", "who or what this task is waiting on")
	clearWaiting := fs.Bool("clear-waiting", false, "clear waiting_on")
	var addTags, removeTags stringSliceFlag
	fs.Var(&addTags, "tag", "tag to add (repeatable)")
	fs.Var(&removeTags, "remove-tag", "tag to remove (repeatable)")
	clearTags := fs.Bool("clear-tags", false, "remove all tags")
	name := fs.String("name", "", "new display name (the slug never changes)")
	project := fs.String("project", "", "move the task into this project (slug)")
	clearProject := fs.Bool("clear-project", false, "detach the task from its project (make it floating)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *status != "" {
		if !store.IsValidStatus(*status) {
			fmt.Fprintf(os.Stderr, "error: --status must be %s, got %q\n", store.StatusUsage, *status)
			return 2
		}
	}
	if *priority != "" && !isValidPriority(*priority) {
		fmt.Fprintf(os.Stderr, "error: --priority must be high|medium|low, got %q\n", *priority)
		return 2
	}
	if *assignee != "" && *clearAssignee {
		fmt.Fprintln(os.Stderr, "error: --assignee and --clear-assignee are mutually exclusive")
		return 2
	}
	if *dueDate != "" && *clearDue {
		fmt.Fprintln(os.Stderr, "error: --due-date and --clear-due are mutually exclusive")
		return 2
	}
	if *waiting != "" && *clearWaiting {
		fmt.Fprintln(os.Stderr, "error: --waiting and --clear-waiting are mutually exclusive")
		return 2
	}
	if *project != "" && *clearProject {
		fmt.Fprintln(os.Stderr, "error: --project and --clear-project are mutually exclusive")
		return 2
	}
	if *name != "" && strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "error: --name must not be blank")
		return 2
	}

	var absWorkDir string
	if *workDir != "" {
		abs, err := resolveWorkDir(*workDir, *mkdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		absWorkDir = abs
	}

	var parsedDue string
	if *dueDate != "" {
		d, err := parseDueDate(*dueDate, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --due-date: %v\n", err)
			return 2
		}
		parsedDue = d.Format("2006-01-02")
	}

	if *workDir == "" && *status == "" && *priority == "" && *assignee == "" && !*clearAssignee &&
		*dueDate == "" && !*clearDue && *waiting == "" && !*clearWaiting &&
		len(addTags) == 0 && len(removeTags) == 0 && !*clearTags &&
		*name == "" && *project == "" && !*clearProject {
		fmt.Fprintln(os.Stderr, "error: give at least one field to update")
		return 2
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

	// --waiting/--clear-waiting is tasks-only; give a clear error on a
	// project ref rather than silently no-op'ing.
	if (*waiting != "" || *clearWaiting) && strings.HasPrefix(ref, "project/") {
		fmt.Fprintf(os.Stderr, "error: waiting is valid on tasks only, got project ref %q\n", ref)
		return 1
	}

	task, err := ResolveTask(db, strings.TrimPrefix(ref, "task/"), true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if *waiting != "" || *clearWaiting {
				if p, pErr := ResolveProject(db, ref, true); pErr == nil {
					fmt.Fprintf(os.Stderr, "error: waiting is valid on tasks only, %q is a project\n", p.Slug)
					return 1
				}
			}
			fmt.Fprintf(os.Stderr, "error: task %q not found\n", ref)
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var targetProject *store.Project
	if *project != "" {
		p, err := ResolveProject(db, *project, false)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintf(os.Stderr, "error: project %q not found\n", *project)
				return 1
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		targetProject = p
	}

	now := store.NowISO()
	if *name != "" {
		if _, err := db.Exec(`UPDATE tasks SET name=?, updated_at=? WHERE slug=?`, strings.TrimSpace(*name), now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update name: %v\n", err)
			return 1
		}
		fmt.Printf("name → %s\n", strings.TrimSpace(*name))
	}
	if targetProject != nil {
		if _, err := db.Exec(`UPDATE tasks SET project_slug=?, updated_at=? WHERE slug=?`, targetProject.Slug, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update project: %v\n", err)
			return 1
		}
		fmt.Printf("project → %s\n", targetProject.Slug)
	}
	if *clearProject {
		if _, err := db.Exec(`UPDATE tasks SET project_slug=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear project: %v\n", err)
			return 1
		}
		fmt.Println("project cleared (floating)")
	}
	if absWorkDir != "" {
		if _, err := db.Exec(`UPDATE tasks SET work_dir=?, updated_at=? WHERE slug=?`, absWorkDir, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update work_dir: %v\n", err)
			return 1
		}
		fmt.Printf("work_dir → %s\n", absWorkDir)
	}
	if *status != "" {
		if _, err := db.Exec(
			`UPDATE tasks SET status=?,
			 status_changed_at = CASE WHEN status != ? THEN ? ELSE status_changed_at END,
			 updated_at=? WHERE slug=?`,
			*status, *status, now, now, task.Slug,
		); err != nil {
			fmt.Fprintf(os.Stderr, "error: update status: %v\n", err)
			return 1
		}
		fmt.Printf("status → %s\n", *status)
	}
	if *priority != "" {
		if _, err := db.Exec(`UPDATE tasks SET priority=?, updated_at=? WHERE slug=?`, *priority, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update priority: %v\n", err)
			return 1
		}
		fmt.Printf("priority → %s\n", *priority)
	}
	if *assignee != "" {
		if _, err := db.Exec(`UPDATE tasks SET assignee=?, updated_at=? WHERE slug=?`, *assignee, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update assignee: %v\n", err)
			return 1
		}
		fmt.Printf("assignee → %s\n", *assignee)
	}
	if *clearAssignee {
		if _, err := db.Exec(`UPDATE tasks SET assignee=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear assignee: %v\n", err)
			return 1
		}
		fmt.Println("assignee cleared")
	}
	if parsedDue != "" {
		if _, err := db.Exec(`UPDATE tasks SET due_date=?, updated_at=? WHERE slug=?`, parsedDue, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update due_date: %v\n", err)
			return 1
		}
		fmt.Printf("due_date → %s\n", parsedDue)
	}
	if *clearDue {
		if _, err := db.Exec(`UPDATE tasks SET due_date=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear due_date: %v\n", err)
			return 1
		}
		fmt.Println("due_date cleared")
	}
	if *waiting != "" {
		if _, err := db.Exec(`UPDATE tasks SET waiting_on=?, updated_at=? WHERE slug=?`, *waiting, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update waiting_on: %v\n", err)
			return 1
		}
		fmt.Printf("waiting_on → %s\n", *waiting)
	}
	if *clearWaiting {
		if _, err := db.Exec(`UPDATE tasks SET waiting_on=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear waiting_on: %v\n", err)
			return 1
		}
		fmt.Println("waiting_on cleared")
	}
	for _, tag := range addTags {
		if err := store.AddTag(db, task.Slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	for _, tag := range removeTags {
		if err := store.RemoveTag(db, task.Slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	if *clearTags {
		if err := store.ClearTags(db, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	return 0
}
