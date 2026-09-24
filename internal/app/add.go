package app

import (
	"database/sql"
	"errors"
	"fmt"
	"focusflow/internal/store"
	"os"
	"path/filepath"
	"time"
)

const projectBriefStub = `# %s

What this project is, why it matters, success criteria. Edit this freely
or let the focusflow skill interview you and rewrite it.
`

const taskBriefStub = `# %s

Edit this brief freely via ` + "`focusflow edit`" + ` or by composing a focusflow skill
session. Sections to cover: What / Why / Where / Done when / Out of scope /
Open questions.
`

// cmdAdd dispatches `focusflow add project|task|playbook ...`.
func cmdAdd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: add requires 'project', 'task', 'playbook', or 'owner'")
		return 2
	}
	switch args[0] {
	case "project":
		return addProject(args[1:])
	case "task":
		return addTask(args[1:])
	case "playbook":
		return addPlaybook(args[1:])
	case "owner":
		return addOwner(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown add subcommand %q\n", args[0])
	return 2
}

func addProject(args []string) int {
	if len(args) == 0 || args[0] == "" || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: add project requires a name")
		return 2
	}
	name := args[0]
	fs := flagSet("add project")
	slugFlag := fs.String("slug", "", "short user-chosen slug (default: auto-generated from name)")
	workDir := fs.String("work-dir", "", "absolute path to the project's work directory (required)")
	priority := fs.String("priority", "medium", "high|medium|low")
	mkdir := fs.Bool("mkdir", false, "create --work-dir if it does not exist")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if !isValidPriority(*priority) {
		fmt.Fprintf(os.Stderr, "error: priority must be high|medium|low, got %q\n", *priority)
		return 2
	}
	if *workDir == "" {
		fmt.Fprintln(os.Stderr, "error: --work-dir is required for projects")
		return 2
	}
	abs, err := resolveWorkDir(*workDir, *mkdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
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

	var slug string
	if *slugFlag != "" {
		slug = *slugFlag
	} else {
		baseSlug, err := Slugify(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		slug, err = uniqueSlug(db, "projects", baseSlug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	now := store.NowISO()
	if _, err := db.Exec(
		`INSERT INTO projects (slug, name, status, priority, work_dir, created_at, updated_at)
		 VALUES (?, ?, 'active', ?, ?, ?, ?)`,
		slug, name, *priority, abs, now, now,
	); err != nil {
		fmt.Fprintf(os.Stderr, "error: insert project: %v\n", err)
		return 1
	}

	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	projDir := filepath.Join(root, "projects", slug)
	if err := os.MkdirAll(filepath.Join(projDir, "updates"), privDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	briefPath := filepath.Join(projDir, "brief.md")
	if _, err := os.Stat(briefPath); os.IsNotExist(err) {
		if err := os.WriteFile(briefPath, []byte(fmt.Sprintf(projectBriefStub, name)), privFile); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	if err := store.UpsertWorkdir(db, abs, "", "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Printf("Created project %q at %s\n", slug, projDir)
	fmt.Printf("Next: focusflow add task \"<name>\" --project %s\n", slug)
	return 0
}

func addTask(args []string) int {
	if len(args) == 0 || args[0] == "" || looksLikeFlag(args[0]) {
		fmt.Fprintln(os.Stderr, "error: add task requires a name")
		return 2
	}
	name := args[0]
	fs := flagSet("add task")
	slugFlag := fs.String("slug", "", "short user-chosen slug (default: auto-generated from name)")
	project := fs.String("project", "", "parent project slug (optional)")
	workDir := fs.String("work-dir", "", "work directory (overrides project default)")
	priority := fs.String("priority", "medium", "high|medium|low")
	dueFlag := fs.String("due", "", "due date (YYYY-MM-DD, today, tomorrow, monday, 3d)")
	mkdir := fs.Bool("mkdir", false, "create --work-dir if it does not exist")
	assignee := fs.String("assignee", "", "person or agent this task is assigned to")
	var tags stringSliceFlag
	fs.Var(&tags, "tag", "tag to apply (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if !isValidPriority(*priority) {
		fmt.Fprintf(os.Stderr, "error: priority must be high|medium|low, got %q\n", *priority)
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

	var slug string
	if *slugFlag != "" {
		slug = *slugFlag
	} else {
		baseSlug, err := Slugify(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		slug, err = uniqueSlug(db, "tasks", baseSlug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	// Resolve project first, since it may supply the default work_dir.
	var projectSlug any = nil
	var projectWorkDir string
	if *project != "" {
		p, err := store.GetProject(db, *project)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fmt.Fprintf(os.Stderr, "error: project %q not found\n", *project)
				return 1
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		projectSlug = p.Slug
		projectWorkDir = p.WorkDir
	}

	// Resolve work_dir with the three-way decision from spec §5.2:
	//   - --work-dir given       → use it (must exist or --mkdir)
	//   - --project given, no wd → inherit from project
	//   - both omitted           → auto-create ~/.focusflow/tasks/<slug>/workspace/
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	taskDir := filepath.Join(root, "tasks", slug)

	var absWorkDir string
	switch {
	case *workDir != "":
		absWorkDir, err = resolveWorkDir(*workDir, *mkdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	case projectWorkDir != "":
		absWorkDir = projectWorkDir
	default:
		// Floating workspace lives inside the private focusflow root.
		absWorkDir = filepath.Join(taskDir, "workspace")
		if err := os.MkdirAll(absWorkDir, privDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: create workspace %s: %v\n", absWorkDir, err)
			return 1
		}
	}

	// Parse optional due date.
	var dueDate any = nil
	if *dueFlag != "" {
		d, err := parseDueDate(*dueFlag, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --due: %v\n", err)
			return 2
		}
		dueDate = d.Format("2006-01-02")
	}

	now := store.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, due_date, status_changed_at, created_at, updated_at)
		 VALUES (?, ?, ?, 'backlog', ?, ?, ?, ?, ?, ?)`,
		slug, name, projectSlug, *priority, absWorkDir, dueDate, now, now, now,
	); err != nil {
		fmt.Fprintf(os.Stderr, "error: insert task: %v\n", err)
		return 1
	}

	if *assignee != "" {
		if _, err := db.Exec(`UPDATE tasks SET assignee = ? WHERE slug = ?`, *assignee, slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: set assignee: %v\n", err)
			return 1
		}
	}
	for _, tag := range tags {
		if err := store.AddTag(db, slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	if err := os.MkdirAll(filepath.Join(taskDir, "updates"), privDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	briefPath := filepath.Join(taskDir, "brief.md")
	if _, err := os.Stat(briefPath); os.IsNotExist(err) {
		if err := os.WriteFile(briefPath, []byte(fmt.Sprintf(taskBriefStub, name)), privFile); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}

	if err := store.UpsertWorkdir(db, absWorkDir, "", "", ""); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if projectSlug == nil {
		fmt.Printf("Created floating task %q at %s\n", slug, taskDir)
	} else {
		fmt.Printf("Created task %q in project %q\n", slug, *project)
	}
	fmt.Printf("Next: focusflow do %s\n", slug)
	return 0
}

// resolveWorkDir canonicalizes path to an absolute path, verifies it
// exists (or mkdirs it if create=true). Returns the abs path.
func resolveWorkDir(path string, create bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("work-dir is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("work-dir %s is not a directory", abs)
		}
		return abs, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", abs, err)
	}
	if !create {
		return "", fmt.Errorf("work-dir %s does not exist (pass --mkdir to create)", abs)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", abs, err)
	}
	return abs, nil
}

// uniqueSlug returns base if no row with that slug exists in table;
// otherwise appends -2, -3, ... until it finds an unused one.
func uniqueSlug(db *sql.DB, table, base string) (string, error) {
	slug := base
	n := 2
	for {
		var exists int
		// nolint:gosec — table name is hardcoded ("projects" or "tasks").
		q := "SELECT 1 FROM " + table + " WHERE slug = ?"
		err := db.QueryRow(q, slug).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return slug, nil
		}
		if err != nil {
			return "", err
		}
		slug = fmt.Sprintf("%s-%d", base, n)
		n++
		if n > 1000 {
			return "", fmt.Errorf("slug %q: too many collisions", base)
		}
	}
}

func isValidPriority(p string) bool {
	return p == "high" || p == "medium" || p == "low"
}
