package app

import (
	"database/sql"
	"fmt"
	"focusflow/internal/store"
	"os"
	"strings"
)

// cmdWatch implements `focusflow watch <task|project|assignee> [--as <assignee>] | --list | --rm <target>`.
// A watch subscribes a watcher (the current task's own mailbox when run
// inside a task session, otherwise the human's "user" queue, or whatever
// --as names) to broadcasts posted by the watched task, its project, or
// its assignee.
func cmdWatch(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(`focusflow watch <task|project|assignee> [--as <assignee>]
focusflow watch --list [--as <assignee>]
focusflow watch --rm <target> [--as <assignee>]

Subscribe to broadcasts posted by a task, project, or assignee. Without
--as, the watcher is the current task's mailbox (or "user" outside a task
session).`)
		return 0
	}

	var as, rm string
	var list bool
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--list":
			list = true
		case a == "--as", a == "--rm":
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "error: %s requires a value\n", a)
				return 2
			}
			if a == "--as" {
				as = args[i]
			} else {
				rm = args[i]
			}
		case strings.HasPrefix(a, "--as="):
			as = strings.TrimPrefix(a, "--as=")
		case strings.HasPrefix(a, "--rm="):
			rm = strings.TrimPrefix(a, "--rm=")
		case looksLikeFlag(a):
			fmt.Fprintf(os.Stderr, "error: unknown flag %q\n", a)
			return 2
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) > 1 {
		fmt.Fprintln(os.Stderr, "error: too many positional arguments")
		return 2
	}
	modes := 0
	if list {
		modes++
	}
	if rm != "" {
		modes++
	}
	if len(positionals) == 1 {
		modes++
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "error: give exactly one of <target>, --list, or --rm <target>")
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openConcurrentDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	watcher := as
	if watcher == "" {
		watcher = resolveCurrentTaskSlug(db)
	}
	if watcher == "" {
		watcher = "user"
	}

	switch {
	case list:
		watched, err := store.ListWatches(db, watcher)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(watched) == 0 {
			fmt.Printf("%s watches nothing\n", watcher)
			return 0
		}
		for _, w := range watched {
			fmt.Println(w)
		}
	case rm != "":
		removed, err := store.Unwatch(db, watcher, rm)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if !removed {
			fmt.Fprintf(os.Stderr, "error: %s was not watching %s\n", watcher, rm)
			return 1
		}
		fmt.Printf("%s stopped watching %s\n", watcher, rm)
	default:
		target := positionals[0]
		if watcher == target {
			fmt.Fprintln(os.Stderr, "error: cannot watch your own address")
			return 2
		}
		if err := store.Watch(db, watcher, target); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("%s now watches %s\n", watcher, target)
	}
	return 0
}

// broadcastFanOut delivers a copy of a broadcast to every watcher of the
// sending task, its project, or its assignee. A watcher that is a task
// slug gets the copy in that task's own mailbox; any other watcher name is
// treated as an assignee-level mailbox. The human's own "user" queue
// already receives the original, so a bare "user" watcher is skipped.
// Returns how many copies were delivered.
func broadcastFanOut(db *sql.DB, orig *store.BusMessage) (int, error) {
	if !orig.FromTaskSlug.Valid {
		return 0, nil
	}
	sender := orig.FromTaskSlug.String
	targets := []string{sender}
	var project, assignee sql.NullString
	if err := db.QueryRow(`SELECT project_slug, assignee FROM tasks WHERE slug = ?`, sender).Scan(&project, &assignee); err == nil {
		targets = append(targets, project.String, assignee.String)
	}
	watchers, err := store.WatchersOf(db, targets...)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, w := range watchers {
		if w == sender || w == "user" {
			continue
		}
		id, err := newMessageID()
		if err != nil {
			return n, err
		}
		cp := *orig
		cp.ID = id
		var isTask int
		_ = db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = ? AND archived_at IS NULL`, w).Scan(&isTask)
		if isTask > 0 {
			cp.ToAssignee = "user"
			cp.ToTaskSlug = sql.NullString{String: w, Valid: true}
		} else {
			cp.ToAssignee = w
			cp.ToTaskSlug = sql.NullString{}
		}
		if err := store.InsertBusMessage(db, &cp); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
