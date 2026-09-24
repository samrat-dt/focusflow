package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Memory is one thing the human said or decided that future work should
// know: an answer to an agent's question, an instruction given to a task,
// or an explicitly recorded decision or note. Memories are injected into
// every session that falls within their scope (see RecallMemories), so the
// human never has to say the same thing twice.
type Memory struct {
	ID          string
	Kind        string // answer | instruction | decision | note
	Scope       string // task | project | global
	TaskSlug    sql.NullString
	ProjectSlug sql.NullString
	Question    sql.NullString
	Body        string
	Source      string // message | do | dashboard | agent | cli
	Pinned      bool
	Status      string // active | forgotten
	CreatedAt   string
}

const MemoryCols = "id, kind, scope, task_slug, project_slug, question, body, source, pinned, status, created_at"

var (
	MemoryKinds  = []string{"answer", "instruction", "decision", "note"}
	MemoryScopes = []string{"task", "project", "global"}
)

const memoriesDDL = `
CREATE TABLE IF NOT EXISTS memories (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('answer','instruction','decision','note')),
    scope         TEXT NOT NULL CHECK (scope IN ('task','project','global')),
    task_slug     TEXT,
    project_slug  TEXT,
    question      TEXT,
    body          TEXT NOT NULL,
    source        TEXT NOT NULL,
    pinned        INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','forgotten')),
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memories_task    ON memories(task_slug, status, created_at);
CREATE INDEX IF NOT EXISTS idx_memories_project ON memories(project_slug, status, created_at);
`

func migrateV4Memories(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, memoriesDDL); err != nil {
		tx.Rollback()
		return fmt.Errorf("create memories: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func ScanMemory(row interface{ Scan(dest ...any) error }) (*Memory, error) {
	var m Memory
	var pinned int
	if err := row.Scan(&m.ID, &m.Kind, &m.Scope, &m.TaskSlug, &m.ProjectSlug, &m.Question, &m.Body, &m.Source, &pinned, &m.Status, &m.CreatedAt); err != nil {
		return nil, err
	}
	m.Pinned = pinned != 0
	return &m, nil
}

// deliveryCue is the "(via focusflow: reply with …)" suffix the dashboard
// appends to messages so an agent knows how to answer. It is plumbing, not
// something the human said, so it is never remembered.
var deliveryCue = regexp.MustCompile(`\s*\(via focusflow:[^)]*(\([^)]*\)[^)]*)*\)\s*$`)

// CleanHumanText strips delivery cues and collapses whitespace.
func CleanHumanText(s string) string {
	s = deliveryCue.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// InsertMemory validates and stores m. ID and CreatedAt must be set.
func InsertMemory(db *sql.DB, m *Memory) error {
	if !contains(MemoryKinds, m.Kind) {
		return fmt.Errorf("kind must be one of %s", strings.Join(MemoryKinds, ", "))
	}
	if !contains(MemoryScopes, m.Scope) {
		return fmt.Errorf("scope must be one of %s", strings.Join(MemoryScopes, ", "))
	}
	if strings.TrimSpace(m.Body) == "" {
		return errors.New("a memory needs some text")
	}
	switch m.Scope {
	case "task":
		if !m.TaskSlug.Valid {
			return errors.New("a task memory needs a task")
		}
	case "project":
		if !m.ProjectSlug.Valid {
			return errors.New("a project memory needs a project")
		}
	}
	if m.Status == "" {
		m.Status = "active"
	}
	_, err := db.Exec(`INSERT INTO memories (`+MemoryCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Kind, m.Scope, m.TaskSlug, m.ProjectSlug, m.Question, m.Body, m.Source, boolToInt(m.Pinned), m.Status, m.CreatedAt)
	return err
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// PendingQuestion returns the task's latest message to the human if it
// arrived after the human last wrote to that task: the question a new
// message from the human is answering. Empty when there is none.
func PendingQuestion(db *sql.DB, taskSlug string) (string, error) {
	var body, at string
	var rowid int64
	err := db.QueryRow(`SELECT body, created_at, rowid FROM bus_messages
		WHERE from_task_slug = ? AND to_assignee = 'user' AND to_task_slug IS NULL
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, taskSlug).Scan(&body, &at, &rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bus_messages
		WHERE to_task_slug = ? AND from_task_slug IS NULL AND from_assignee = 'user'
		  AND (created_at > ? OR (created_at = ? AND rowid > ?))`, taskSlug, at, at, rowid).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	return CleanHumanText(body), nil
}

// RememberHumanInput records what the human just told a task. It is an
// answer when the task had asked something since the human last wrote,
// otherwise an instruction. Messages to a project assistant are scoped to
// the whole project. Call it BEFORE inserting the human's new bus message,
// so the question is still pending. Returns nil (and stores nothing) for
// empty text or an unknown task.
func RememberHumanInput(db *sql.DB, id, taskSlug, text, source string) (*Memory, error) {
	body := CleanHumanText(text)
	if body == "" {
		return nil, nil
	}
	var project sql.NullString
	var kind string
	err := db.QueryRow(`SELECT project_slug, kind FROM tasks WHERE slug = ?`, taskSlug).Scan(&project, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q, err := PendingQuestion(db, taskSlug)
	if err != nil {
		return nil, err
	}
	m := &Memory{ID: id, Kind: "instruction", Scope: "task", TaskSlug: nullStr(taskSlug), ProjectSlug: project, Body: body, Source: source, CreatedAt: NowISO()}
	if q != "" {
		m.Kind = "answer"
		m.Question = nullStr(truncateRunes(q, 300))
	}
	if kind == "project_agent" && project.Valid {
		m.Scope = "project"
	}
	if err := InsertMemory(db, m); err != nil {
		return nil, err
	}
	return m, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// MemoryFilter selects memories for listing. Zero value = every active one.
type MemoryFilter struct {
	TaskSlug    string
	ProjectSlug string
	GlobalOnly  bool
	IncludeGone bool
}

func ListMemories(db *sql.DB, f MemoryFilter) ([]*Memory, error) {
	var where []string
	var args []any
	if !f.IncludeGone {
		where = append(where, "status = 'active'")
	}
	switch {
	case f.GlobalOnly:
		where = append(where, "scope = 'global'")
	case f.TaskSlug != "":
		where = append(where, "task_slug = ?")
		args = append(args, f.TaskSlug)
	case f.ProjectSlug != "":
		where = append(where, "project_slug = ?")
		args = append(args, f.ProjectSlug)
	}
	q := `SELECT ` + MemoryCols + ` FROM memories`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, rowid DESC"
	return queryMemories(db, q, args...)
}

func queryMemories(db *sql.DB, q string, args ...any) ([]*Memory, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Memory
	for rows.Next() {
		m, err := ScanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecallMemories is what a session for taskSlug should know, most binding
// first: pinned and global memories, then the project's, then the task's
// own. Each group is newest first; limit caps each group.
func RecallMemories(db *sql.DB, taskSlug, projectSlug string, limit int) (global, project, task []*Memory, err error) {
	global, err = queryMemories(db, `SELECT `+MemoryCols+` FROM memories
		WHERE status = 'active' AND (scope = 'global' OR (pinned = 1 AND scope = 'project' AND project_slug = ?))
		ORDER BY pinned DESC, created_at DESC LIMIT ?`, projectSlug, limit)
	if err != nil {
		return
	}
	if projectSlug != "" {
		project, err = queryMemories(db, `SELECT `+MemoryCols+` FROM memories
			WHERE status = 'active' AND pinned = 0 AND project_slug = ?
			  AND (scope = 'project' OR (scope = 'task' AND task_slug != ? AND kind IN ('answer','decision')))
			ORDER BY created_at DESC LIMIT ?`, projectSlug, taskSlug, limit)
		if err != nil {
			return
		}
	}
	task, err = queryMemories(db, `SELECT `+MemoryCols+` FROM memories
		WHERE status = 'active' AND scope = 'task' AND task_slug = ?
		ORDER BY pinned DESC, created_at DESC LIMIT ?`, taskSlug, limit)
	return
}

func GetMemory(db *sql.DB, id string) (*Memory, error) {
	return ScanMemory(db.QueryRow(`SELECT `+MemoryCols+` FROM memories WHERE id = ?`, id))
}

// UpdateMemory applies one of: forget, restore, pin, unpin, promote
// (task → project, or project → global).
func UpdateMemory(db *sql.DB, id, op string) (*Memory, error) {
	m, err := GetMemory(db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no memory %q", id)
	}
	if err != nil {
		return nil, err
	}
	switch op {
	case "forget":
		m.Status = "forgotten"
	case "restore":
		m.Status = "active"
	case "pin":
		m.Pinned = true
	case "unpin":
		m.Pinned = false
	case "promote":
		switch {
		case m.Scope == "task" && m.ProjectSlug.Valid:
			m.Scope = "project"
		case m.Scope == "task", m.Scope == "project":
			m.Scope = "global"
		default:
			return nil, errors.New("this memory already applies everywhere")
		}
	default:
		return nil, fmt.Errorf("unknown memory operation %q", op)
	}
	_, err = db.Exec(`UPDATE memories SET status = ?, pinned = ?, scope = ? WHERE id = ?`, m.Status, boolToInt(m.Pinned), m.Scope, id)
	return m, err
}
