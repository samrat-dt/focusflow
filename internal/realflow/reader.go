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
}

// Project is the subset of a real-flow project row the mirror needs.
type Project struct {
	Slug     string
	Name     string
	Status   string
	Priority string
	WorkDir  string
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
