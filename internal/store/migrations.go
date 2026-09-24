package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// withForeignKeysOff runs fn inside a transaction on conn with
// foreign_keys disabled (the documented procedure for rebuilding a table),
// re-enabling them afterwards and verifying no violations were introduced.
func withForeignKeysOff(ctx context.Context, conn *sql.Conn, version int, fn func(tx *sql.Tx) error) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		tx.Rollback()
		return err
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations > 0 {
		tx.Rollback()
		return fmt.Errorf("%d foreign key violation(s) after migration", violations)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

var createTasksRe = regexp.MustCompile(`(?i)^CREATE TABLE\s+"?tasks"?`)

// migrateV2Statuses widens the tasks.status CHECK to the six stages by
// rebuilding the table (SQLite cannot alter a CHECK in place). All rows,
// columns and indexes are preserved.
func migrateV2Statuses(ctx context.Context, conn *sql.Conn) error {
	return withForeignKeysOff(ctx, conn, 2, func(tx *sql.Tx) error {
		var ddl string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&ddl); err != nil {
			return fmt.Errorf("read tasks DDL: %w", err)
		}
		if strings.Contains(ddl, "'paused'") {
			return nil // already wide (fresh DB created from a newer DDL)
		}
		oldCheck := regexp.MustCompile(`CHECK\s*\(\s*status\s+IN\s*\(\s*'backlog'\s*,\s*'in-progress'\s*,\s*'done'\s*\)\s*\)`)
		if !oldCheck.MatchString(ddl) {
			if regexp.MustCompile(`(?i)CHECK\s*\(\s*status\b`).MatchString(ddl) {
				return fmt.Errorf("tasks.status has an unrecognised CHECK; refusing to guess")
			}
			return nil // no status CHECK at all (very old schema): nothing to widen
		}
		newDDL := oldCheck.ReplaceAllString(ddl, statusCheckSQL)
		// The hidden per-project coordinator task needs its own kind.
		kindCheck := regexp.MustCompile(`CHECK\s*\(\s*kind\s+IN\s*\(\s*'regular'\s*,\s*'playbook_run'\s*\)\s*\)`)
		newDDL = kindCheck.ReplaceAllString(newDDL, "CHECK (kind IN ('regular','playbook_run','project_agent'))")
		newDDL = createTasksRe.ReplaceAllString(newDDL, "CREATE TABLE tasks_new")

		idxRows, err := tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name='tasks' AND sql IS NOT NULL`)
		if err != nil {
			return err
		}
		var indexes []string
		for idxRows.Next() {
			var s string
			if err := idxRows.Scan(&s); err != nil {
				idxRows.Close()
				return err
			}
			indexes = append(indexes, s)
		}
		idxRows.Close()

		for _, stmt := range []string{
			newDDL,
			`INSERT INTO tasks_new SELECT * FROM tasks`,
			`DROP TABLE tasks`,
			`ALTER TABLE tasks_new RENAME TO tasks`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", firstLine(stmt), err)
			}
		}
		for _, stmt := range indexes {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("recreate index: %w", err)
			}
		}
		return nil
	})
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// migrateV3ProjectAgentsAndTerminal adds: projects.agent_task_slug (the
// hidden coordinator task backing a project's one session), tasks.terminal_ref
// (the terminal tab/session a task was last opened in, so `do` can focus it
// instead of opening another), and bus_messages.delivered_at (when the
// recipient's session first surfaced a message — a real delivery receipt).
func migrateV3ProjectAgentsAndTerminal(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	add := func(table, column, decl string) error {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return err
		}
		has := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			if name == column {
				has = true
			}
		}
		rows.Close()
		if has {
			return nil
		}
		_, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
		return err
	}
	for _, c := range [][3]string{
		{"projects", "agent_task_slug", "TEXT"},
		{"tasks", "terminal_ref", "TEXT"},
		{"bus_messages", "delivered_at", "TEXT"},
	} {
		if err := add(c[0], c[1], c[2]); err != nil {
			tx.Rollback()
			return fmt.Errorf("add %s.%s: %w", c[0], c[1], err)
		}
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
