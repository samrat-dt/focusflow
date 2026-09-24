package app

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"syscall"

	"focusflow/internal/store"
)

// pauseRequestBody is what an open terminal session is asked when the user
// pauses its task. The dashboard never kills a window someone may be
// looking at; the session wraps up on its own.
const pauseRequestBody = "Pause requested: wrap up now — save a short progress note, leave the work in a resumable state, then stop. Do not start anything new. The task is now paused."

// setStatus moves a task to status, stamping status_changed_at only when
// it actually changes.
func setStatus(db *sql.DB, slug, status string) error {
	now := store.NowISO()
	_, err := db.Exec(`UPDATE tasks SET status = ?,
		status_changed_at = CASE WHEN status != ? THEN ? ELSE status_changed_at END,
		updated_at = ? WHERE slug = ?`, status, status, now, now, slug)
	return err
}

// stageArgs parses "<ref>" and rejects flags: these verbs take one task
// (or, for pause, a project/ ref).
func stageRef(verb string, args []string) (string, int) {
	if len(args) != 1 || looksLikeFlag(args[0]) {
		fmt.Fprintf(os.Stderr, "error: usage: focusflow %s <task-ref>\n", verb)
		return "", 2
	}
	return args[0], 0
}

// cmdSetStage implements `focusflow next <ref>` and `focusflow review <ref>`:
// plain stage moves. `review` is what an agent runs when it believes the
// goal is met; the dashboard's Approve then runs `done`.
func cmdSetStage(stage string, args []string) int {
	ref, rc := stageRef(stage, args)
	if rc != 0 {
		return rc
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	task, rc := findTask(db, ref)
	if task == nil {
		return rc
	}
	if task.Status == store.StatusDone {
		fmt.Fprintf(os.Stderr, "error: task %q is done; reopen it first (focusflow update task %s --status in-progress)\n", task.Slug, task.Slug)
		return 1
	}
	if err := setStatus(db, task.Slug, stage); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s → %s\n", task.Slug, stage)
	if stage == store.StatusReview {
		fmt.Println("Waiting for approval; the dashboard's Approve (or `focusflow done`) closes it out.")
	}
	return 0
}

// cmdPause implements `focusflow pause <ref>`. Pause has two independent
// halves: the task's STATUS becomes paused, and whatever is running is
// dealt with according to how it runs —
//   - a headless run is stopped (its process gets SIGTERM);
//   - a live terminal session is asked, over the bus, to wrap up (it stays
//     open; "open in a terminal" is a separate marker from the status).
//
// `pause project/<slug>` pauses the project's coordinator and every
// in-progress subtask.
func cmdPause(args []string) int {
	ref, rc := stageRef("pause", args)
	if rc != 0 {
		return rc
	}
	db, err := openOwnerDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	if strings.HasPrefix(ref, "project/") {
		return pauseProject(db, strings.TrimPrefix(ref, "project/"))
	}
	task, rc := findTask(db, ref)
	if task == nil {
		return rc
	}
	if task.Status == store.StatusDone {
		fmt.Fprintf(os.Stderr, "error: task %q is done; there is nothing to pause\n", task.Slug)
		return 1
	}
	msg, err := pauseTask(db, task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Println(msg)
	return 0
}

func pauseTask(db *sql.DB, task *store.Task) (string, error) {
	notes := []string{}
	// Headless run: stop it.
	if task.AutoRunStatus.Valid && task.AutoRunStatus.String == "running" && task.AutoRunPID.Valid {
		if err := syscall.Kill(int(task.AutoRunPID.Int64), syscall.SIGTERM); err == nil || err == syscall.ESRCH {
			now := store.NowISO()
			db.Exec(`UPDATE tasks SET auto_run_status='stopped', auto_run_finished=?, updated_at=? WHERE slug=?`, now, now, task.Slug)
			notes = append(notes, "stopped the headless run")
		}
	}
	// Live terminal: ask it politely.
	if task.SessionID.Valid && isSessionLive(task.SessionID.String) && !(len(notes) > 0) {
		id, err := newMessageID()
		if err != nil {
			return "", err
		}
		m := &store.BusMessage{
			ID: id, FromAssignee: "user", ToAssignee: "user",
			ToTaskSlug: sql.NullString{String: task.Slug, Valid: true},
			Body:       pauseRequestBody, Urgent: true, CreatedAt: store.NowISO(),
		}
		if err := store.InsertBusMessage(db, m); err != nil {
			return "", err
		}
		notes = append(notes, "asked the open terminal session to wrap up (it stays open)")
	}
	if err := setStatus(db, task.Slug, store.StatusPaused); err != nil {
		return "", err
	}
	msg := task.Slug + " → paused"
	if len(notes) > 0 {
		msg += " (" + strings.Join(notes, "; ") + ")"
	}
	return msg, nil
}

func pauseProject(db *sql.DB, projectSlug string) int {
	p, err := ResolveProject(db, projectSlug, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: project %q not found\n", projectSlug)
		return 1
	}
	rows, err := db.Query(`SELECT `+store.TaskCols+` FROM tasks WHERE project_slug = ? AND archived_at IS NULL AND status IN ('in-progress','next')`, p.Slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var tasks []*store.Task
	for rows.Next() {
		if t, err := store.ScanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	rows.Close()
	if len(tasks) == 0 {
		fmt.Printf("nothing running in %s\n", p.Slug)
		return 0
	}
	for _, t := range tasks {
		msg, err := pauseTask(db, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", t.Slug, err)
			return 1
		}
		fmt.Println(msg)
	}
	return 0
}
