// Package db implements the SQLite data layer for focusflow.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
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
    source        TEXT NOT NULL DEFAULT 'native',
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
    source                TEXT NOT NULL DEFAULT 'native',
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

-- Message bus: directed messages between "user" (the local human) and a
-- task's bound session, plus broadcasts. A message is addressed to either
-- a specific task's own mailbox (to_task_slug set) or an assignee-level
-- mailbox like "user" (to_assignee set, to_task_slug NULL). See
-- internal/app/message.go and internal/app/inbox.go for the CLI surface;
-- the focusflow skill §4.18 documents the behavior.
CREATE TABLE IF NOT EXISTS bus_messages (
    id              TEXT PRIMARY KEY,
    from_assignee   TEXT NOT NULL,
    from_task_slug  TEXT REFERENCES tasks(slug),
    to_assignee     TEXT NOT NULL,
    to_task_slug    TEXT REFERENCES tasks(slug),
    body            TEXT NOT NULL,
    urgent          INTEGER NOT NULL DEFAULT 0,
    broadcast       INTEGER NOT NULL DEFAULT 0,
    reply_to        TEXT REFERENCES bus_messages(id),
    mail            TEXT NOT NULL DEFAULT 'unread' CHECK (mail IN ('unread','read')),
    created_at      TEXT NOT NULL,
    read_at         TEXT
);

CREATE INDEX IF NOT EXISTS idx_bus_messages_to_task     ON bus_messages(to_task_slug, mail, created_at);
CREATE INDEX IF NOT EXISTS idx_bus_messages_to_assignee ON bus_messages(to_assignee, mail, created_at);

CREATE TABLE IF NOT EXISTS bus_watches (
    watcher     TEXT NOT NULL,
    watched     TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (watcher, watched)
);

CREATE TABLE IF NOT EXISTS bus_nudges (
    task_slug  TEXT PRIMARY KEY,
    nudged_at  TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS bus_surfaced (
    task_slug   TEXT NOT NULL,
    message_id  TEXT NOT NULL,
    PRIMARY KEY (task_slug, message_id)
);

-- Owners: autonomous recurring agents (charter + cadence + tick state).
CREATE TABLE IF NOT EXISTS owners (
    slug              TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    work_dir          TEXT NOT NULL,
    project_slug      TEXT REFERENCES projects(slug),
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','retired')),
    every             TEXT NOT NULL,
    next_wake_at      TEXT,
    last_tick_at      TEXT,
    last_tick_status  TEXT,
    tick_pid          INTEGER,
    tick_started      TEXT,
    harness           TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    archived_at       TEXT
);
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
	Source     string // always 'native' (legacy column)
	CreatedAt  string
	UpdatedAt  string
	ArchivedAt sql.NullString
	// AgentTaskSlug is the hidden kind='project_agent' task that holds the
	// project's one permanent session (see `focusflow project agent`).
	AgentTaskSlug sql.NullString
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
	Source             string // always 'native' (legacy column)
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
	TerminalRef        sql.NullString // terminal tab/session this task was last opened in
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
	ExcludeAgents   bool     // hide hidden project-agent (coordinator) tasks when Kind is ""
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

// dsnFor builds the connection string every focusflow process uses. The
// pragmas ride in the DSN — not a one-off db.Exec — so EVERY connection in
// database/sql's pool inherits them (a plain `PRAGMA foreign_keys = ON`
// only affects the single connection that ran it):
//   - journal_mode(WAL): readers (the dashboard, other commands, hooks)
//     never block on a writer and vice versa.
//   - busy_timeout(30000): contention waits instead of failing SQLITE_BUSY.
//   - foreign_keys(1): enforced on all connections.
//   - _txlock=immediate: write transactions take the write lock at BEGIN,
//     so two concurrent read-then-write transactions serialize cleanly
//     rather than failing on lock upgrade.
func dsnFor(path string) string {
	q := url.Values{}
	for _, p := range []string{"busy_timeout(30000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

// SchemaVersion is the schema generation this binary understands, stored
// in SQLite's PRAGMA user_version. Version 1 is the baseline: schemaDDL
// plus runMigrations' idempotent column/table upgrades, which is what
// every pre-versioning database (user_version 0) is brought to on first
// open. From here on, a schema change MUST be a new entry in
// versionedMigrations (never an edit to schemaDDL alone — an already-
// stamped database would skip it) and bump SchemaVersion.
const SchemaVersion = 6

// versionedMigrations[i] upgrades a database from version i+1 to i+2 (the
// baseline, version 1, is applied by ensureSchema). Each step runs on one
// dedicated connection and manages its own transaction, because some
// (table rebuilds) must run with foreign_keys OFF, which SQLite ignores
// inside a transaction. The step and its user_version bump commit together.
var versionedMigrations = []func(ctx context.Context, conn *sql.Conn) error{
	migrateV2Statuses,
	migrateV3ProjectAgentsAndTerminal,
	migrateV4Memories,
	migrateV5HouseRules,
	migrateV6RunPolicy,
}

// ErrSchemaTooNew is returned when the database was written by a newer
// focusflow than this binary; opening it read-write could corrupt data.
type ErrSchemaTooNew struct{ Have, Max int }

func (e ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("database schema v%d is newer than this focusflow understands (v%d) — upgrade the binary (`make install`)", e.Have, e.Max)
}

// OpenDB opens (or creates) the SQLite database at path and brings its
// schema up to SchemaVersion. It is the ONE way every command, hook and
// test opens focusflow.db. When the database is already current the
// only cost is one PRAGMA read — no DDL, no write lock.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureSchema(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if v > SchemaVersion {
		return ErrSchemaTooNew{Have: v, Max: SchemaVersion}
	}
	if v == SchemaVersion {
		return nil
	}
	if v < 1 {
		if _, err := db.Exec(schemaDDL); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := runMigrations(db); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("stamp schema version: %w", err)
		}
		v = 1
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration connection: %w", err)
	}
	defer conn.Close()
	for ; v < SchemaVersion; v++ {
		if err := versionedMigrations[v-1](ctx, conn); err != nil {
			return fmt.Errorf("migrate to v%d: %w", v+1, err)
		}
	}
	return nil
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

	// source is a legacy column: every row is 'native'. It is kept (rather
	// than dropped) because SQLite cannot drop a column that a CHECK
	// constraint references on existing databases.
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

const ProjectCols = "slug, name, status, priority, work_dir, source, created_at, updated_at, archived_at, agent_task_slug"

func ScanProject(row interface{ Scan(dest ...any) error }) (*Project, error) {
	var p Project
	err := row.Scan(&p.Slug, &p.Name, &p.Status, &p.Priority, &p.WorkDir, &p.Source, &p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt, &p.AgentTaskSlug)
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

const TaskCols = "slug, name, project_slug, status, kind, playbook_slug, priority, work_dir, source, waiting_on, due_date, status_changed_at, session_id, session_started, session_last_resumed, assignee, harness, auto_run_status, auto_run_pid, auto_run_started, auto_run_finished, auto_run_log, created_at, updated_at, archived_at, terminal_ref"

func ScanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	err := row.Scan(
		&t.Slug, &t.Name, &t.ProjectSlug, &t.Status, &t.Kind, &t.PlaybookSlug,
		&t.Priority, &t.WorkDir, &t.Source,
		&t.WaitingOn, &t.DueDate, &t.StatusChangedAt, &t.SessionID,
		&t.SessionStarted, &t.SessionLastResumed,
		&t.Assignee, &t.Harness, &t.AutoRunStatus, &t.AutoRunPID, &t.AutoRunStarted, &t.AutoRunFinished, &t.AutoRunLog,
		&t.CreatedAt, &t.UpdatedAt, &t.ArchivedAt, &t.TerminalRef,
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
	if filter.ExcludeAgents {
		where = append(where, "kind != 'project_agent'")
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
