// Package realflow is a read-only reader for real flow's own database,
// ~/.flow/flow.db. It exists solely to support focusflow's session-start
// mirror (see internal/app/mirror.go): pulling the current session's
// bound flow project + task into focusflow's own database, one-way only.
//
// Every connection this package opens is mode=ro at the SQLite driver
// level, so an accidental write fails fast instead of relying on
// discipline alone. Real flow's database is never written to by
// anything in this package.
package realflow

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Root returns real flow's data root. Honors $FLOW_ROOT (flow's own
// override env var — see the flow skill's "Paths in this skill" note),
// falling back to ~/.flow. Mirrors flow-reader.js's defaultRoot() so a
// mirror run looks in exactly the place real flow itself would.
func Root() (string, error) {
	if r := os.Getenv("FLOW_ROOT"); r != "" {
		return r, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".flow"), nil
}

// DBPath returns the path to real flow's flow.db under Root(). Does not
// check that the file exists.
func DBPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "flow.db"), nil
}

// Task is the subset of a real-flow task row the mirror needs.
type Task struct {
	Slug        string
	Name        string
	ProjectSlug string // "" for a floating task
	Status      string
	Priority    string
	WorkDir     string
	ArchivedAt  string // "" if not archived
}

// Project is the subset of a real-flow project row the mirror needs.
type Project struct {
	Slug       string
	Name       string
	Status     string
	Priority   string
	WorkDir    string
	ArchivedAt string // "" if not archived
}

// BusMessage is the subset of a real-flow bus_messages row the
// one-time migration needs. Fields that have no focusflow equivalent
// (sender_session_id, delivered_at, waited_s, acked_by) are dropped —
// see AllBusMessages.
type BusMessage struct {
	ID           string
	CreatedAt    string
	Kind         string // "message" | "broadcast"
	FromAssignee string
	FromTaskSlug string // "" if none
	ToAssignee   string
	ToTaskSlug   string // "" if none
	Body         string
	Urgent       bool
	Status       string // "pending" | "delivered" | "acked"
	AckedAt      string // "" if not acked
	ReplyTo      string // "" if none
}

// openReadOnly opens dbPath as a read-only SQLite connection. mode=ro
// makes the driver itself reject any write, and busy_timeout bounds how
// long a read waits on a lock held by a concurrent real-flow writer.
func openReadOnly(dbPath string, busyTimeoutMs int) (*sql.DB, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))
	dsn := "file:" + dbPath + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s read-only: %w", dbPath, err)
	}
	return db, nil
}

// LookupBySessionID reverse-looks-up the real-flow task bound to
// sessionID (the current Claude Code / Codex session id — see the flow
// skill §10), plus its project if it has one. Returns (nil, nil, nil)
// with no error if no real-flow task is bound to this session — that's
// the common case for any session flow itself didn't spawn, not a
// failure.
func LookupBySessionID(dbPath, sessionID string, busyTimeoutMs int) (*Task, *Project, error) {
	if sessionID == "" {
		return nil, nil, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	db, err := openReadOnly(dbPath, busyTimeoutMs)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	var t Task
	var projectSlug sql.NullString
	row := db.QueryRow(
		`SELECT slug, name, project_slug, status, priority, work_dir FROM tasks WHERE session_id = ?`,
		sessionID,
	)
	if err := row.Scan(&t.Slug, &t.Name, &projectSlug, &t.Status, &t.Priority, &t.WorkDir); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("query task by session: %w", err)
	}
	if projectSlug.Valid {
		t.ProjectSlug = projectSlug.String
	}

	var project *Project
	if t.ProjectSlug != "" {
		var p Project
		prow := db.QueryRow(
			`SELECT slug, name, status, priority, work_dir FROM projects WHERE slug = ?`,
			t.ProjectSlug,
		)
		switch err := prow.Scan(&p.Slug, &p.Name, &p.Status, &p.Priority, &p.WorkDir); err {
		case nil:
			project = &p
		case sql.ErrNoRows:
			// Task references a project slug that doesn't resolve (stale
			// data mid-edit on flow's side). Mirror the task alone.
		default:
			return nil, nil, fmt.Errorf("query project %s: %w", t.ProjectSlug, err)
		}
	}

	return &t, project, nil
}

// AllProjects returns every project row in real flow's flow.db,
// including archived ones. Unlike LookupBySessionID (which resolves at
// most one session-bound task), this backs the one-time bulk migration
// command — it needs the full picture, not just what's currently in
// flight.
func AllProjects(dbPath string, busyTimeoutMs int) ([]Project, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	db, err := openReadOnly(dbPath, busyTimeoutMs)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT slug, name, status, priority, work_dir, archived_at FROM projects ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("query all projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		var archivedAt sql.NullString
		if err := rows.Scan(&p.Slug, &p.Name, &p.Status, &p.Priority, &p.WorkDir, &archivedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		if archivedAt.Valid {
			p.ArchivedAt = archivedAt.String
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllTasks returns every task row in real flow's flow.db, including
// archived ones. See AllProjects.
func AllTasks(dbPath string, busyTimeoutMs int) ([]Task, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	db, err := openReadOnly(dbPath, busyTimeoutMs)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT slug, name, project_slug, status, priority, work_dir, archived_at FROM tasks ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("query all tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		var projectSlug, archivedAt sql.NullString
		if err := rows.Scan(&t.Slug, &t.Name, &projectSlug, &t.Status, &t.Priority, &t.WorkDir, &archivedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		if projectSlug.Valid {
			t.ProjectSlug = projectSlug.String
		}
		if archivedAt.Valid {
			t.ArchivedAt = archivedAt.String
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllBusMessages returns every message row in real flow's flow.db,
// oldest first. The migration relies on this ordering: a reply always
// has a later created_at than the message it replies to, so inserting
// in this order guarantees a reply's target row already exists —
// satisfying focusflow's bus_messages(reply_to) foreign key without
// needing a second pass.
func AllBusMessages(dbPath string, busyTimeoutMs int) ([]BusMessage, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stat %s: %w", dbPath, err)
	}
	db, err := openReadOnly(dbPath, busyTimeoutMs)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id, created_at, kind, from_assignee, from_task_slug, to_assignee, to_task_slug,
		       body, urgent, status, acked_at, reply_to
		FROM bus_messages
		ORDER BY created_at ASC, rowid ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query all bus messages: %w", err)
	}
	defer rows.Close()

	var out []BusMessage
	for rows.Next() {
		var m BusMessage
		var fromTaskSlug, toTaskSlug, ackedAt, replyTo sql.NullString
		var urgent int
		if err := rows.Scan(&m.ID, &m.CreatedAt, &m.Kind, &m.FromAssignee, &fromTaskSlug, &m.ToAssignee, &toTaskSlug,
			&m.Body, &urgent, &m.Status, &ackedAt, &replyTo); err != nil {
			return nil, fmt.Errorf("scan bus message: %w", err)
		}
		m.Urgent = urgent != 0
		if fromTaskSlug.Valid {
			m.FromTaskSlug = fromTaskSlug.String
		}
		if toTaskSlug.Valid {
			m.ToTaskSlug = toTaskSlug.String
		}
		if ackedAt.Valid {
			m.AckedAt = ackedAt.String
		}
		if replyTo.Valid {
			m.ReplyTo = replyTo.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
