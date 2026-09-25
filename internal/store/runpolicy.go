package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RunPolicy is how a project allows unattended (background) runs: whether they may start at all, and
// an optional time limit after which a run is stopped.
type RunPolicy struct {
	On           bool
	LimitMinutes int // 0 = no limit
}

func migrateV6RunPolicy(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	has := func(col string) bool {
		var n int
		_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = ?`, col).Scan(&n)
		return n > 0
	}
	var steps []string
	if !has("auto_runs") {
		steps = append(steps, `ALTER TABLE projects ADD COLUMN auto_runs TEXT NOT NULL DEFAULT 'on' CHECK (auto_runs IN ('on','off'))`)
	}
	if !has("run_limit_minutes") {
		steps = append(steps, `ALTER TABLE projects ADD COLUMN run_limit_minutes INTEGER`)
	}
	steps = append(steps, `PRAGMA user_version = 6`)
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			tx.Rollback()
			return fmt.Errorf("run policy migration: %w", err)
		}
	}
	return tx.Commit()
}

// GetRunPolicy returns the project's policy; a task with no project (or an unknown one) may always run.
func GetRunPolicy(db *sql.DB, projectSlug string) (RunPolicy, error) {
	if projectSlug == "" {
		return RunPolicy{On: true}, nil
	}
	var mode string
	var limit sql.NullInt64
	err := db.QueryRow(`SELECT auto_runs, run_limit_minutes FROM projects WHERE slug = ?`, projectSlug).Scan(&mode, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		return RunPolicy{On: true}, nil
	}
	if err != nil {
		return RunPolicy{}, err
	}
	return RunPolicy{On: mode != "off", LimitMinutes: int(limit.Int64)}, nil
}

// SetRunPolicy changes what is given; nil leaves a field as it is. limit 0 removes the limit.
func SetRunPolicy(db *sql.DB, projectSlug string, on *bool, limit *int) error {
	if on != nil {
		mode := "off"
		if *on {
			mode = "on"
		}
		if _, err := db.Exec(`UPDATE projects SET auto_runs = ?, updated_at = ? WHERE slug = ?`, mode, NowISO(), projectSlug); err != nil {
			return err
		}
	}
	if limit != nil {
		var v any
		if *limit > 0 {
			v = *limit
		}
		if _, err := db.Exec(`UPDATE projects SET run_limit_minutes = ?, updated_at = ? WHERE slug = ?`, v, NowISO(), projectSlug); err != nil {
			return err
		}
	}
	return nil
}
