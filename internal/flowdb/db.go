// Package db implements the SQLite data layer for focusflow.
package flowdb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schemaDDL is the full DDL for focusflow.db. Each statement is idempotent
// (CREATE ... IF NOT EXISTS) so OpenDB can run this on every startup.
//
// Note on NULL-safe equality: SQLite's `IS` operator treats NULLs as
// equal (NULL IS NULL → true, 'x' IS 'x' → true). Code that needs
// optimistic-lock updates against a preSessionID that may be NULL
// should use `WHERE session_id IS ?` rather than `= ?`.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS projects (
    slug          TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','done')),
    priority      TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('high','medium','low')),
    work_dir      TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'native' CHECK (source IN ('native','flow-mirror')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    archived_at   TEXT
);

CREATE TABLE IF NOT EXISTS playbooks (
    slug          TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    project_slug  TEXT REFERENCES projects(slug),
    work_dir      TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    archived_at   TEXT
);

CREATE TABLE IF NOT EXISTS tasks (
    slug                  TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    project_slug          TEXT REFERENCES projects(slug),
    status                TEXT NOT NULL DEFAULT 'backlog' CHECK (status IN ('backlog','in-progress','done')),
    kind                  TEXT NOT NULL DEFAULT 'regular' CHECK (kind IN ('regular','playbook_run')),
    playbook_slug         TEXT REFERENCES playbooks(slug),
    priority              TEXT NOT NULL DEFAULT 'medium' CHECK (priority IN ('high','medium','low')),
    work_dir              TEXT NOT NULL,
    source                TEXT NOT NULL DEFAULT 'native' CHECK (source IN ('native','flow-mirror')),
    waiting_on            TEXT,
    due_date              TEXT,
    status_changed_at     TEXT,
    session_id            TEXT,
    session_started       TEXT,
    session_last_resumed  TEXT,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL,
    archived_at           TEXT
);

CREATE TABLE IF NOT EXISTS workdirs (
    path          TEXT PRIMARY KEY,
    name          TEXT,
    description   TEXT,
    git_remote    TEXT,
    last_used_at  TEXT,
    created_at    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_project    ON tasks(project_slug);
CREATE INDEX IF NOT EXISTS idx_tasks_status     ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_updated_at ON tasks(updated_at);
`

// indexesPostMigrate are indexes that depend on columns added by
// runMigrations. Running them in schemaDDL before migrations would fail
// against an existing pre-migration DB ("no such column"), so they live
// here and run AFTER migrations land.
const indexesPostMigrate = `
CREATE INDEX IF NOT EXISTS idx_tasks_kind          ON tasks(kind);
CREATE INDEX IF NOT EXISTS idx_tasks_playbook_slug ON tasks(playbook_slug);
CREATE INDEX IF NOT EXISTS idx_playbooks_project   ON playbooks(project_slug);
CREATE INDEX IF NOT EXISTS idx_task_tags_tag ON task_tags(tag);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_session_id ON tasks(session_id) WHERE session_id IS NOT NULL;
`

// ---------- models ----------

// Project mirrors the projects table.
type Project struct {
	Slug       string
	Name       string
	Status     string
	Priority   string
	WorkDir    string
	Source     string // 'native' | 'flow-mirror'
	CreatedAt  string
	UpdatedAt  string
	ArchivedAt sql.NullString
}

// Task mirrors the tasks table. ProjectSlug is nullable for floating tasks.
type Task struct {
	Slug               string
	Name               string
	ProjectSlug        sql.NullString
	Status             string
	Kind               string         // 'regular' | 'playbook_run'
	PlaybookSlug       sql.NullString // set when Kind='playbook_run'
	Priority           string
	WorkDir            string
	Source             string // 'native' | 'flow-mirror'
	WaitingOn          sql.NullString
	DueDate            sql.NullString
	StatusChangedAt    sql.NullString
	SessionID          sql.NullString
	SessionStarted     sql.NullString
	SessionLastResumed sql.NullString
	Assignee           sql.NullString
	Harness            sql.NullString
	AutoRunStatus      sql.NullString // "" | "running" | "completed" | "dead"
	AutoRunPID         sql.NullInt64
	AutoRunStarted     sql.NullString
	AutoRunFinished    sql.NullString
	AutoRunLog         sql.NullString
	CreatedAt          string
	UpdatedAt          string
	ArchivedAt         sql.NullString
}

// Workdir mirrors the workdirs convenience registry.
type Workdir struct {
	Path        string
	Name        sql.NullString
	Description sql.NullString
	GitRemote   sql.NullString
	LastUsedAt  sql.NullString
	CreatedAt   string
}

// TaskFilter holds optional filters for ListTasks.
type TaskFilter struct {
	Status          string
	Project         string
	Priority        string
	Kind            string // "regular" (default), "playbook_run", or "" for all
	PlaybookSlug    string // optional; filter to runs of one playbook
	Since           string // RFC3339 or "" for no lower bound
	IncludeArchived bool
	ExcludeDone     bool     // hide status=done; ignored if Status is set explicitly
	Tags            []string // AND semantics: a task must carry every listed tag
}

// ProjectFilter is the equivalent for ListProjects.
type ProjectFilter struct {
	Status          string
	IncludeArchived bool
}

// ---------- lifecycle ----------

// NowISO returns the current time formatted as RFC3339.
func NowISO() string {
	return time.Now().Format(time.RFC3339)
}

// NullIfEmpty returns a *string pointing to s, or nil if s is empty.
func NullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// OpenDB opens (or creates) the SQLite database at path, ensures the
// schema is present, and runs idempotent migrations.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func runMigrations(db *sql.DB) error {
	has, err := columnExists(db, "workdirs", "description")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE workdirs ADD COLUMN description TEXT`); err != nil {
			return fmt.Errorf("add workdirs.description: %w", err)
		}
	}
	has, err = columnExists(db, "tasks", "due_date")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN due_date TEXT`); err != nil {
			return fmt.Errorf("add tasks.due_date: %w", err)
		}
	}
	has, err = columnExists(db, "tasks", "status_changed_at")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN status_changed_at TEXT`); err != nil {
			return fmt.Errorf("add tasks.status_changed_at: %w", err)
		}
	}

	// playbooks table: created via schemaDDL on every OpenDB, so no ALTER needed
	// for the table itself. Just ensure tasks columns are present.

	has, err = columnExists(db, "tasks", "kind")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'regular'`); err != nil {
			return fmt.Errorf("add tasks.kind: %w", err)
		}
		// Note: SQLite doesn't allow CHECK constraints on ADD COLUMN; the
		// CHECK is only enforced for fresh tables (see schemaDDL). Application
		// code should validate enum values before insert.
	}

	has, err = columnExists(db, "tasks", "playbook_slug")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN playbook_slug TEXT REFERENCES playbooks(slug)`); err != nil {
			return fmt.Errorf("add tasks.playbook_slug: %w", err)
		}
	}

	// source marks rows mirrored in read-only from real flow (vs
	// user-created directly in focusflow). SQLite doesn't allow a CHECK
	// constraint on ADD COLUMN (see the kind comment above); application
	// code (UpsertMirroredProject / UpsertMirroredTask) is the only writer
	// of 'flow-mirror' and validates it.
	has, err = columnExists(db, "projects", "source")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE projects ADD COLUMN source TEXT NOT NULL DEFAULT 'native'`); err != nil {
			return fmt.Errorf("add projects.source: %w", err)
		}
	}
	has, err = columnExists(db, "tasks", "source")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN source TEXT NOT NULL DEFAULT 'native'`); err != nil {
			return fmt.Errorf("add tasks.source: %w", err)
		}
	}

	for _, col := range []struct{ name, ddl string }{
		{"assignee", "assignee TEXT"},
		{"harness", "harness TEXT"},
		{"auto_run_status", "auto_run_status TEXT"},
		{"auto_run_pid", "auto_run_pid INTEGER"},
		{"auto_run_started", "auto_run_started TEXT"},
		{"auto_run_finished", "auto_run_finished TEXT"},
		{"auto_run_log", "auto_run_log TEXT"},
	} {
		has, err := columnExists(db, "tasks", col.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN ` + col.ddl); err != nil {
				return fmt.Errorf("add tasks.%s: %w", col.name, err)
			}
		}
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS task_tags (
		    task_slug   TEXT NOT NULL REFERENCES tasks(slug) ON DELETE CASCADE,
		    tag         TEXT NOT NULL,
		    created_at  TEXT NOT NULL,
		    PRIMARY KEY (task_slug, tag)
		)
	`); err != nil {
		return fmt.Errorf("create task_tags: %w", err)
	}

	// Indexes that depend on columns added above. Safe to run after every
	// migration pass — CREATE INDEX IF NOT EXISTS is idempotent, and by
	// this point all referenced columns exist.
	if _, err := db.Exec(indexesPostMigrate); err != nil {
		return fmt.Errorf("create post-migrate indexes: %w", err)
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ---------- project queries ----------

const ProjectCols = "slug, name, status, priority, work_dir, source, created_at, updated_at, archived_at"

func ScanProject(row interface{ Scan(dest ...any) error }) (*Project, error) {
	var p Project
	err := row.Scan(&p.Slug, &p.Name, &p.Status, &p.Priority, &p.WorkDir, &p.Source, &p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetProject(db *sql.DB, slug string) (*Project, error) {
	row := db.QueryRow("SELECT "+ProjectCols+" FROM projects WHERE slug = ?", slug)
	return ScanProject(row)
}

func ListProjects(db *sql.DB, filter ProjectFilter) ([]*Project, error) {
	var where []string
	var args []any
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if !filter.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	q := "SELECT " + ProjectCols + " FROM projects"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY slug"
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := ScanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- task queries ----------

const TaskCols = "slug, name, project_slug, status, kind, playbook_slug, priority, work_dir, source, waiting_on, due_date, status_changed_at, session_id, session_started, session_last_resumed, assignee, harness, auto_run_status, auto_run_pid, auto_run_started, auto_run_finished, auto_run_log, created_at, updated_at, archived_at"

func ScanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	err := row.Scan(
		&t.Slug, &t.Name, &t.ProjectSlug, &t.Status, &t.Kind, &t.PlaybookSlug,
		&t.Priority, &t.WorkDir, &t.Source,
		&t.WaitingOn, &t.DueDate, &t.StatusChangedAt, &t.SessionID,
		&t.SessionStarted, &t.SessionLastResumed,
		&t.Assignee, &t.Harness, &t.AutoRunStatus, &t.AutoRunPID, &t.AutoRunStarted, &t.AutoRunFinished, &t.AutoRunLog,
		&t.CreatedAt, &t.UpdatedAt, &t.ArchivedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func GetTask(db *sql.DB, slug string) (*Task, error) {
	row := db.QueryRow("SELECT "+TaskCols+" FROM tasks WHERE slug = ?", slug)
	return ScanTask(row)
}

func ListTasks(db *sql.DB, filter TaskFilter) ([]*Task, error) {
	var where []string
	var args []any
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	} else if filter.ExcludeDone {
		where = append(where, "status != 'done'")
	}
	if filter.Project != "" {
		where = append(where, "project_slug = ?")
		args = append(args, filter.Project)
	}
	if filter.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, filter.Kind)
	}
	if filter.PlaybookSlug != "" {
		where = append(where, "playbook_slug = ?")
		args = append(args, filter.PlaybookSlug)
	}
	if filter.Priority != "" {
		where = append(where, "priority = ?")
		args = append(args, filter.Priority)
	}
	if filter.Since != "" {
		where = append(where, "updated_at >= ?")
		args = append(args, filter.Since)
	}
	for _, tag := range filter.Tags {
		where = append(where, "slug IN (SELECT task_slug FROM task_tags WHERE LOWER(tag) = LOWER(?))")
		args = append(args, tag)
	}
	if !filter.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	q := "SELECT " + TaskCols + " FROM tasks"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END, slug`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := ScanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------- workdir queries ----------

const WorkdirCols = "path, name, description, git_remote, last_used_at, created_at"

func ScanWorkdir(row interface{ Scan(dest ...any) error }) (*Workdir, error) {
	var w Workdir
	err := row.Scan(&w.Path, &w.Name, &w.Description, &w.GitRemote, &w.LastUsedAt, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func GetWorkdir(db *sql.DB, path string) (*Workdir, error) {
	row := db.QueryRow("SELECT "+WorkdirCols+" FROM workdirs WHERE path = ?", path)
	return ScanWorkdir(row)
}

func ListWorkdirs(db *sql.DB) ([]*Workdir, error) {
	q := "SELECT " + WorkdirCols + " FROM workdirs ORDER BY last_used_at IS NULL, last_used_at DESC, path"
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list workdirs: %w", err)
	}
	defer rows.Close()
	var out []*Workdir
	for rows.Next() {
		w, err := ScanWorkdir(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workdir: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func UpsertWorkdir(db *sql.DB, path, name, description, gitRemote string) error {
	now := NowISO()
	_, err := db.Exec(`
		INSERT INTO workdirs (path, name, description, git_remote, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name         = COALESCE(NULLIF(excluded.name, ''),        workdirs.name),
			description  = COALESCE(NULLIF(excluded.description, ''), workdirs.description),
			git_remote   = COALESCE(NULLIF(excluded.git_remote, ''),  workdirs.git_remote),
			last_used_at = excluded.last_used_at
	`, path, NullIfEmpty(name), NullIfEmpty(description), NullIfEmpty(gitRemote), now, now)
	if err != nil {
		return fmt.Errorf("upsert workdir %s: %w", path, err)
	}
	return nil
}

// ---------- playbook models ----------

// Playbook mirrors the playbooks table.
type Playbook struct {
	Slug        string
	Name        string
	ProjectSlug sql.NullString
	WorkDir     string
	CreatedAt   string
	UpdatedAt   string
	ArchivedAt  sql.NullString
}

// PlaybookFilter holds optional filters for ListPlaybooks.
type PlaybookFilter struct {
	Project         string
	IncludeArchived bool
}

// ---------- playbook queries ----------

const PlaybookCols = "slug, name, project_slug, work_dir, created_at, updated_at, archived_at"

func ScanPlaybook(row interface{ Scan(dest ...any) error }) (*Playbook, error) {
	var p Playbook
	err := row.Scan(&p.Slug, &p.Name, &p.ProjectSlug, &p.WorkDir, &p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetPlaybook(db *sql.DB, slug string) (*Playbook, error) {
	row := db.QueryRow("SELECT "+PlaybookCols+" FROM playbooks WHERE slug = ?", slug)
	return ScanPlaybook(row)
}

func ListPlaybooks(db *sql.DB, filter PlaybookFilter) ([]*Playbook, error) {
	var where []string
	var args []any
	if filter.Project != "" {
		where = append(where, "project_slug = ?")
		args = append(args, filter.Project)
	}
	if !filter.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}
	q := "SELECT " + PlaybookCols + " FROM playbooks"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY slug"
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list playbooks: %w", err)
	}
	defer rows.Close()
	var out []*Playbook
	for rows.Next() {
		p, err := ScanPlaybook(rows)
		if err != nil {
			return nil, fmt.Errorf("scan playbook: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpsertPlaybook inserts a new playbook or updates an existing row by slug.
// Updates touch name, project_slug, work_dir, updated_at; archived_at is
// not touched here (use a dedicated archive command).
func UpsertPlaybook(db *sql.DB, pb *Playbook) error {
	now := NowISO()
	if pb.CreatedAt == "" {
		pb.CreatedAt = now
	}
	pb.UpdatedAt = now
	_, err := db.Exec(`
		INSERT INTO playbooks (slug, name, project_slug, work_dir, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name         = excluded.name,
			project_slug = excluded.project_slug,
			work_dir     = excluded.work_dir,
			updated_at   = excluded.updated_at
	`, pb.Slug, pb.Name, pb.ProjectSlug, pb.WorkDir, pb.CreatedAt, pb.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert playbook %s: %w", pb.Slug, err)
	}
	return nil
}

// ---------- tag queries ----------

// AddTag associates tag with taskSlug. Idempotent — adding an existing
// tag is a no-op, not an error.
func AddTag(db *sql.DB, taskSlug, tag string) error {
	_, err := db.Exec(
		`INSERT INTO task_tags (task_slug, tag, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(task_slug, tag) DO NOTHING`,
		taskSlug, tag, NowISO(),
	)
	if err != nil {
		return fmt.Errorf("add tag %q to %s: %w", tag, taskSlug, err)
	}
	return nil
}

// RemoveTag disassociates tag from taskSlug. Removing a tag that isn't
// present is a no-op, not an error.
func RemoveTag(db *sql.DB, taskSlug, tag string) error {
	if _, err := db.Exec(`DELETE FROM task_tags WHERE task_slug = ? AND tag = ?`, taskSlug, tag); err != nil {
		return fmt.Errorf("remove tag %q from %s: %w", tag, taskSlug, err)
	}
	return nil
}

// ClearTags removes all tags from taskSlug.
func ClearTags(db *sql.DB, taskSlug string) error {
	if _, err := db.Exec(`DELETE FROM task_tags WHERE task_slug = ?`, taskSlug); err != nil {
		return fmt.Errorf("clear tags for %s: %w", taskSlug, err)
	}
	return nil
}

// ListTagsForTask returns taskSlug's tags, sorted alphabetically.
func ListTagsForTask(db *sql.DB, taskSlug string) ([]string, error) {
	rows, err := db.Query(`SELECT tag FROM task_tags WHERE task_slug = ? ORDER BY tag`, taskSlug)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", taskSlug, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

// TagCount pairs a tag with how many non-archived tasks carry it.
type TagCount struct {
	Tag   string
	Count int
}

// ListAllTags returns every distinct tag in use (across non-archived
// tasks) with its usage count, sorted alphabetically by tag.
func ListAllTags(db *sql.DB) ([]TagCount, error) {
	rows, err := db.Query(`
		SELECT tt.tag, COUNT(*) FROM task_tags tt
		JOIN tasks t ON t.slug = tt.task_slug
		WHERE t.archived_at IS NULL
		GROUP BY tt.tag ORDER BY tt.tag
	`)
	if err != nil {
		return nil, fmt.Errorf("list all tags: %w", err)
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// ---------- flow-mirror queries ----------
//
// One-way, read-only-sourced upserts for rows mirrored in from real
// flow's flow.db (see internal/realflow). Both are guarded by
// `WHERE <table>.source = 'flow-mirror'` on the DO UPDATE clause: if a
// row with the same slug already exists and its source is 'native' (i.e.
// created directly in focusflow, not by a prior mirror run), the WHERE
// evaluates false, SQLite silently skips the update, and the native row
// is left completely untouched — no error, no partial write. The
// returned bool tells the caller whether the mirror was actually applied
// (true) or skipped due to a native collision (false), so callers can
// decide whether to also touch on-disk brief/updates files.

// UpsertMirroredProject inserts a project mirrored from real flow, or
// refreshes name/status/priority/work_dir if a mirror-owned row with this
// slug already exists.
func UpsertMirroredProject(db *sql.DB, slug, name, status, priority, workDir string) (applied bool, err error) {
	now := NowISO()
	res, err := db.Exec(`
		INSERT INTO projects (slug, name, status, priority, work_dir, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'flow-mirror', ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name       = excluded.name,
			status     = excluded.status,
			priority   = excluded.priority,
			work_dir   = excluded.work_dir,
			updated_at = excluded.updated_at
		WHERE projects.source = 'flow-mirror'
	`, slug, name, status, priority, workDir, now, now)
	if err != nil {
		return false, fmt.Errorf("upsert mirrored project %s: %w", slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}

// UpsertMirroredTask is the task-table equivalent of
// UpsertMirroredProject. projectSlug may be empty for a floating task.
// session_id and its siblings are deliberately never written here — a
// mirrored task's session lifecycle belongs to real flow, not focusflow.
func UpsertMirroredTask(db *sql.DB, slug, name, projectSlug, status, priority, workDir string) (applied bool, err error) {
	now := NowISO()
	res, err := db.Exec(`
		INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, source, status_changed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'flow-mirror', ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name         = excluded.name,
			project_slug = excluded.project_slug,
			status       = excluded.status,
			priority     = excluded.priority,
			work_dir     = excluded.work_dir,
			updated_at   = excluded.updated_at
		WHERE tasks.source = 'flow-mirror'
	`, slug, name, NullIfEmpty(projectSlug), status, priority, workDir, now, now, now)
	if err != nil {
		return false, fmt.Errorf("upsert mirrored task %s: %w", slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}
