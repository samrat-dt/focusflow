package flowdb

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// BusMessage mirrors the bus_messages table — one row on focusflow's
// message bus. A message is addressed to exactly one mailbox: either a
// specific task's own queue (ToTaskSlug set) or an assignee-level queue
// like "user" (ToAssignee set, ToTaskSlug empty).
type BusMessage struct {
	ID           string
	FromAssignee string
	FromTaskSlug sql.NullString
	ToAssignee   string
	ToTaskSlug   sql.NullString
	Body         string
	Urgent       bool
	Broadcast    bool
	ReplyTo      sql.NullString
	Mail         string // "unread" | "read"
	CreatedAt    string
	ReadAt       sql.NullString
}

const BusMessageCols = "id, from_assignee, from_task_slug, to_assignee, to_task_slug, body, urgent, broadcast, reply_to, mail, created_at, read_at"

func ScanBusMessage(row interface{ Scan(dest ...any) error }) (*BusMessage, error) {
	var m BusMessage
	var urgent, broadcast int
	err := row.Scan(
		&m.ID, &m.FromAssignee, &m.FromTaskSlug, &m.ToAssignee, &m.ToTaskSlug,
		&m.Body, &urgent, &broadcast, &m.ReplyTo, &m.Mail, &m.CreatedAt, &m.ReadAt,
	)
	if err != nil {
		return nil, err
	}
	m.Urgent = urgent != 0
	m.Broadcast = broadcast != 0
	return &m, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// InsertBusMessage inserts a new message row. m.ID and m.CreatedAt must
// already be set by the caller; Mail is always initialized to 'unread'.
func InsertBusMessage(db *sql.DB, m *BusMessage) error {
	_, err := db.Exec(`
		INSERT INTO bus_messages (id, from_assignee, from_task_slug, to_assignee, to_task_slug, body, urgent, broadcast, reply_to, mail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread', ?)
	`, m.ID, m.FromAssignee, m.FromTaskSlug, m.ToAssignee, m.ToTaskSlug, m.Body, boolToInt(m.Urgent), boolToInt(m.Broadcast), m.ReplyTo, m.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert bus message %s: %w", m.ID, err)
	}
	return nil
}

// GetBusMessage fetches a message by id.
func GetBusMessage(db *sql.DB, id string) (*BusMessage, error) {
	row := db.QueryRow("SELECT "+BusMessageCols+" FROM bus_messages WHERE id = ?", id)
	return ScanBusMessage(row)
}

// MailboxFilter identifies one mailbox to read from: either a specific
// task's own queue (TaskSlug set) or an assignee-level queue such as
// "user" (Assignee set, TaskSlug empty).
type MailboxFilter struct {
	Assignee string
	TaskSlug string
}

func (f MailboxFilter) whereClause(args *[]any) string {
	if f.TaskSlug != "" {
		*args = append(*args, f.TaskSlug)
		return "to_task_slug = ?"
	}
	*args = append(*args, f.Assignee)
	return "to_assignee = ? AND to_task_slug IS NULL"
}

// ListBusMessages returns messages in the given mailbox, oldest first.
// Unread only unless all is true.
func ListBusMessages(db *sql.DB, f MailboxFilter, all bool) ([]*BusMessage, error) {
	var args []any
	where := []string{f.whereClause(&args)}
	if !all {
		where = append(where, "mail = 'unread'")
	}
	// rowid (SQLite's implicit insertion-order column) breaks ties within
	// the same created_at second — NowISO()'s RFC3339 timestamps only have
	// second resolution, so two messages sent in the same second would
	// otherwise sort by their random id, not send order.
	q := "SELECT " + BusMessageCols + " FROM bus_messages WHERE " + strings.Join(where, " AND ") + " ORDER BY created_at ASC, rowid ASC"
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list bus messages: %w", err)
	}
	defer rows.Close()
	var out []*BusMessage
	for rows.Next() {
		m, err := ScanBusMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan bus message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PopOldestUnread atomically reads the oldest unread message in the given
// mailbox and, unless keepUnread is true, marks it read in the same
// transaction. Returns (nil, nil) if the mailbox has no unread message.
func PopOldestUnread(db *sql.DB, f MailboxFilter, keepUnread bool) (*BusMessage, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var args []any
	where := f.whereClause(&args)
	row := tx.QueryRow("SELECT "+BusMessageCols+" FROM bus_messages WHERE "+where+" AND mail='unread' ORDER BY created_at ASC, rowid ASC LIMIT 1", args...)
	m, err := ScanBusMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		committed = true
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select oldest unread: %w", err)
	}

	if !keepUnread {
		now := NowISO()
		if _, err := tx.Exec(`UPDATE bus_messages SET mail='read', read_at=? WHERE id=?`, now, m.ID); err != nil {
			return nil, fmt.Errorf("mark read: %w", err)
		}
		m.Mail = "read"
		m.ReadAt = sql.NullString{String: now, Valid: true}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return m, nil
}

// MarkRead marks a specific message read by id, idempotently. Returns
// found=false if no message with that id exists at all (already-read is
// still found=true, since the caller's intent — "this is acked" — holds).
func MarkRead(db *sql.DB, id string) (found bool, err error) {
	now := NowISO()
	res, err := db.Exec(`UPDATE bus_messages SET mail='read', read_at=? WHERE id=? AND mail='unread'`, now, id)
	if err != nil {
		return false, fmt.Errorf("mark read %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	} else if n > 0 {
		return true, nil
	}
	var exists int
	err = db.QueryRow(`SELECT 1 FROM bus_messages WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check existence %s: %w", id, err)
	}
	return true, nil
}
