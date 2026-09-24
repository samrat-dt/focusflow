package store

import (
	"database/sql"
	"fmt"
)

// Owner is an autonomous recurring agent: a charter (owners/<slug>/brief.md)
// plus a cadence. Each "tick" spawns a task tagged owner:<slug> that works
// the charter and journals to owners/<slug>/updates/.
type Owner struct {
	Slug           string
	Name           string
	WorkDir        string
	ProjectSlug    sql.NullString
	Status         string // active | paused | retired
	Every          string // fallback cadence, e.g. "30m", "2h", "1d"
	NextWakeAt     sql.NullString
	LastTickAt     sql.NullString
	LastTickStatus sql.NullString
	TickPID        sql.NullInt64
	TickStarted    sql.NullString
	Harness        sql.NullString
	CreatedAt      string
	UpdatedAt      string
	ArchivedAt     sql.NullString
}

const OwnerCols = "slug, name, work_dir, project_slug, status, every, next_wake_at, last_tick_at, last_tick_status, tick_pid, tick_started, harness, created_at, updated_at, archived_at"

func scanOwner(row interface{ Scan(dest ...any) error }) (*Owner, error) {
	var o Owner
	err := row.Scan(&o.Slug, &o.Name, &o.WorkDir, &o.ProjectSlug, &o.Status, &o.Every, &o.NextWakeAt,
		&o.LastTickAt, &o.LastTickStatus, &o.TickPID, &o.TickStarted, &o.Harness, &o.CreatedAt, &o.UpdatedAt, &o.ArchivedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func GetOwner(db *sql.DB, slug string) (*Owner, error) {
	return scanOwner(db.QueryRow("SELECT "+OwnerCols+" FROM owners WHERE slug = ?", slug))
}

// ListOwners returns owners ordered by slug; retired/archived ones only
// when includeArchived.
func ListOwners(db *sql.DB, includeArchived bool) ([]*Owner, error) {
	q := "SELECT " + OwnerCols + " FROM owners"
	if !includeArchived {
		q += " WHERE archived_at IS NULL"
	}
	rows, err := db.Query(q + " ORDER BY slug")
	if err != nil {
		return nil, fmt.Errorf("list owners: %w", err)
	}
	defer rows.Close()
	var out []*Owner
	for rows.Next() {
		o, err := scanOwner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func InsertOwner(db *sql.DB, o *Owner) error {
	now := NowISO()
	_, err := db.Exec(`INSERT INTO owners (slug, name, work_dir, project_slug, status, every, next_wake_at, harness, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'paused', ?, NULL, ?, ?, ?)`,
		o.Slug, o.Name, o.WorkDir, o.ProjectSlug, o.Every, o.Harness, now, now)
	if err != nil {
		return fmt.Errorf("insert owner %s: %w", o.Slug, err)
	}
	return nil
}

// SetOwnerStatus changes status (and archived_at for retired) and, when
// nextWake is non-empty, next_wake_at.
func SetOwnerStatus(db *sql.DB, slug, status, nextWake string) error {
	now := NowISO()
	archived := any(nil)
	if status == "retired" {
		archived = now
	}
	_, err := db.Exec(`UPDATE owners SET status = ?, archived_at = ?, next_wake_at = COALESCE(NULLIF(?, ''), next_wake_at), updated_at = ? WHERE slug = ?`,
		status, archived, nextWake, now, slug)
	if err != nil {
		return fmt.Errorf("set owner status %s: %w", slug, err)
	}
	return nil
}

func SetOwnerNextWake(db *sql.DB, slug, when string) error {
	_, err := db.Exec(`UPDATE owners SET next_wake_at = ?, updated_at = ? WHERE slug = ?`, when, NowISO(), slug)
	if err != nil {
		return fmt.Errorf("set next wake %s: %w", slug, err)
	}
	return nil
}

func RecordOwnerTick(db *sql.DB, slug, status, nextWake string) error {
	now := NowISO()
	_, err := db.Exec(`UPDATE owners SET last_tick_at = ?, last_tick_status = ?, tick_started = ?, next_wake_at = ?, updated_at = ? WHERE slug = ?`,
		now, status, now, nextWake, now, slug)
	if err != nil {
		return fmt.Errorf("record owner tick %s: %w", slug, err)
	}
	return nil
}

func DeleteOwner(db *sql.DB, slug string) error {
	if _, err := db.Exec(`DELETE FROM owners WHERE slug = ?`, slug); err != nil {
		return fmt.Errorf("delete owner %s: %w", slug, err)
	}
	return nil
}
