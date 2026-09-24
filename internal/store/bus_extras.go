package store

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ---------- watches ----------

// Watch subscribes watcher to broadcasts from watched (a task slug,
// project slug, or assignee name). Idempotent.
func Watch(db *sql.DB, watcher, watched string) error {
	_, err := db.Exec(`INSERT INTO bus_watches (watcher, watched, created_at) VALUES (?, ?, ?)
		ON CONFLICT(watcher, watched) DO NOTHING`, watcher, watched, NowISO())
	if err != nil {
		return fmt.Errorf("watch %s -> %s: %w", watcher, watched, err)
	}
	return nil
}

// Unwatch removes a subscription and reports whether one existed.
func Unwatch(db *sql.DB, watcher, watched string) (bool, error) {
	res, err := db.Exec(`DELETE FROM bus_watches WHERE watcher = ? AND watched = ?`, watcher, watched)
	if err != nil {
		return false, fmt.Errorf("unwatch %s -> %s: %w", watcher, watched, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListWatches returns what watcher currently watches, sorted.
func ListWatches(db *sql.DB, watcher string) ([]string, error) {
	rows, err := db.Query(`SELECT watched FROM bus_watches WHERE watcher = ? ORDER BY watched`, watcher)
	if err != nil {
		return nil, fmt.Errorf("list watches: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WatchersOf returns the distinct watchers subscribed to any of targets.
func WatchersOf(db *sql.DB, targets ...string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, t := range targets {
		if t == "" {
			continue
		}
		rows, err := db.Query(`SELECT watcher FROM bus_watches WHERE watched = ? ORDER BY watcher`, t)
		if err != nil {
			return nil, fmt.Errorf("watchers of %s: %w", t, err)
		}
		for rows.Next() {
			var w string
			if err := rows.Scan(&w); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	sort.Strings(out)
	return out, nil
}

// ---------- inbox stats ----------

// InboxStats summarizes the human's mailbox the way `focusflow inbox stats`
// reports it: messages (not broadcasts) addressed to the user's assignee-level
// queue that have been answered vs. are still pending, plus wait times
// (created -> read).
type InboxStats struct {
	Answered   int
	Pending    int
	Broadcasts int
	Average    time.Duration
	Median     time.Duration
	Worst      time.Duration
}

func InboxStatsFor(db *sql.DB, assignee string) (*InboxStats, error) {
	var s InboxStats
	if err := db.QueryRow(`SELECT COUNT(*) FROM bus_messages WHERE broadcast = 1`).Scan(&s.Broadcasts); err != nil {
		return nil, fmt.Errorf("count broadcasts: %w", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM bus_messages WHERE broadcast = 0 AND mail = 'unread' AND to_assignee = ? AND to_task_slug IS NULL`, assignee).Scan(&s.Pending); err != nil {
		return nil, fmt.Errorf("count pending: %w", err)
	}
	rows, err := db.Query(`SELECT created_at, read_at FROM bus_messages
		WHERE broadcast = 0 AND mail = 'read' AND to_assignee = ? AND to_task_slug IS NULL AND read_at IS NOT NULL`, assignee)
	if err != nil {
		return nil, fmt.Errorf("query answered: %w", err)
	}
	defer rows.Close()
	var waits []time.Duration
	for rows.Next() {
		var created, read string
		if err := rows.Scan(&created, &read); err != nil {
			return nil, err
		}
		c, err1 := time.Parse(time.RFC3339, created)
		r, err2 := time.Parse(time.RFC3339, read)
		w := time.Duration(0)
		if err1 == nil && err2 == nil && r.After(c) {
			w = r.Sub(c)
		}
		waits = append(waits, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.Answered = len(waits)
	if len(waits) > 0 {
		sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
		var total time.Duration
		for _, w := range waits {
			total += w
		}
		s.Average = total / time.Duration(len(waits))
		s.Median = waits[len(waits)/2]
		s.Worst = waits[len(waits)-1]
	}
	return &s, nil
}

// ---------- nudges / surfaced ----------

// NudgeState is a task's row in bus_nudges (zero value if never nudged).
type NudgeState struct {
	NudgedAt string
	Attempts int
}

func GetNudgeState(db *sql.DB, taskSlug string) (NudgeState, error) {
	var st NudgeState
	err := db.QueryRow(`SELECT nudged_at, attempts FROM bus_nudges WHERE task_slug = ?`, taskSlug).Scan(&st.NudgedAt, &st.Attempts)
	if err == sql.ErrNoRows {
		return NudgeState{}, nil
	}
	if err != nil {
		return NudgeState{}, fmt.Errorf("get nudge state %s: %w", taskSlug, err)
	}
	return st, nil
}

// RecordNudge stamps a nudge and bumps the attempt counter.
func RecordNudge(db *sql.DB, taskSlug string) error {
	_, err := db.Exec(`INSERT INTO bus_nudges (task_slug, nudged_at, attempts) VALUES (?, ?, 1)
		ON CONFLICT(task_slug) DO UPDATE SET nudged_at = excluded.nudged_at, attempts = bus_nudges.attempts + 1`,
		taskSlug, NowISO())
	if err != nil {
		return fmt.Errorf("record nudge %s: %w", taskSlug, err)
	}
	return nil
}

// ResetNudges clears a task's nudge backoff (e.g. once it consumed mail).
func ResetNudges(db *sql.DB, taskSlug string) error {
	if _, err := db.Exec(`DELETE FROM bus_nudges WHERE task_slug = ?`, taskSlug); err != nil {
		return fmt.Errorf("reset nudges %s: %w", taskSlug, err)
	}
	return nil
}

// MarkSurfaced records that message ids were shown to taskSlug so they
// aren't nudged about again.
func MarkSurfaced(db *sql.DB, taskSlug string, ids ...string) error {
	for _, id := range ids {
		if _, err := db.Exec(`INSERT OR IGNORE INTO bus_surfaced (task_slug, message_id) VALUES (?, ?)`, taskSlug, id); err != nil {
			return fmt.Errorf("mark surfaced %s/%s: %w", taskSlug, id, err)
		}
	}
	return nil
}

// PendingUnsurfacedForTask returns unread broadcasts fanned out to the
// task's own mailbox that haven't been surfaced to it yet, oldest first.
func PendingUnsurfacedForTask(db *sql.DB, taskSlug string) ([]*BusMessage, error) {
	rows, err := db.Query(`SELECT `+BusMessageCols+` FROM bus_messages
		WHERE broadcast = 1 AND mail = 'unread' AND to_task_slug = ?
		  AND id NOT IN (SELECT message_id FROM bus_surfaced WHERE task_slug = ?)
		ORDER BY created_at ASC, rowid ASC`, taskSlug, taskSlug)
	if err != nil {
		return nil, fmt.Errorf("pending unsurfaced for %s: %w", taskSlug, err)
	}
	defer rows.Close()
	var out []*BusMessage
	for rows.Next() {
		m, err := ScanBusMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
