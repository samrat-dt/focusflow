package app

import (
	"database/sql"
	"fmt"
	"focusflow/internal/flowdb"
	"focusflow/internal/realflow"
	"os"
)

// migrateBusyTimeoutMs bounds lock-contention waits against real flow's
// database. Unlike mirrorBusyTimeoutMs (which backs the SessionStart hot
// path and must stay tiny), this command is explicit, foreground, and
// user-invoked once — a slightly longer wait for a possibly-busy real
// flow.db is an acceptable, expected cost, not a UX regression.
const migrateBusyTimeoutMs = 3000

// cmdMigrateFromFlow implements `focusflow migrate-from-flow` — a
// one-time, idempotent bulk backfill of every real-flow project, task,
// and historical message into focusflow's own database. It exists
// because the SessionStart mirror (mirror.go) only ever reaches the one
// task bound to the current session; entities real flow tracks that no
// session has touched under focusflow stay invisible to focusflow
// forever without this. Safe to re-run: every write goes through the
// same source='flow-mirror' guarded upserts the mirror hook uses, plus
// INSERT OR IGNORE for messages, so a native row or an already-migrated
// message is never touched twice.
func cmdMigrateFromFlow(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println(`focusflow migrate-from-flow

One-time bulk backfill: copies every project, task, and historical
message from real flow's ~/.flow/flow.db into focusflow's own database.
Read-only against real flow; safe to re-run.`)
		return 0
	}

	realDBPath, err := realflow.DBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(realDBPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: real flow.db not found at %s: %v\n", realDBPath, err)
		return 1
	}

	projects, err := realflow.AllProjects(realDBPath, migrateBusyTimeoutMs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading real-flow projects: %v\n", err)
		return 1
	}
	tasks, err := realflow.AllTasks(realDBPath, migrateBusyTimeoutMs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading real-flow tasks: %v\n", err)
		return 1
	}
	messages, err := realflow.AllBusMessages(realDBPath, migrateBusyTimeoutMs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading real-flow messages: %v\n", err)
		return 1
	}

	focusDBPath, err := flowDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := openConcurrentDB(focusDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open focusflow.db: %v\n", err)
		return 1
	}
	defer db.Close()

	root, err := flowRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var projectsMigrated, projectsSkipped int
	for _, p := range projects {
		applied, err := flowdb.UpsertMirroredProject(db, p.Slug, p.Name, p.Status, p.Priority, p.WorkDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: migrate project %s: %v\n", p.Slug, err)
			return 1
		}
		if !applied {
			projectsSkipped++
			continue
		}
		projectsMigrated++
		if err := ensureMirroredEntityFiles(root, "projects", p.Slug, p.Name); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if p.ArchivedAt != "" {
			if err := flowdb.SetMirroredProjectArchivedAt(db, p.Slug, p.ArchivedAt); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
		}
	}

	var tasksMigrated, tasksSkipped int
	for _, tk := range tasks {
		applied, err := flowdb.UpsertMirroredTask(db, tk.Slug, tk.Name, tk.ProjectSlug, tk.Status, tk.Priority, tk.WorkDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: migrate task %s: %v\n", tk.Slug, err)
			return 1
		}
		if !applied {
			tasksSkipped++
			continue
		}
		tasksMigrated++
		if err := ensureMirroredEntityFiles(root, "tasks", tk.Slug, tk.Name); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if tk.ArchivedAt != "" {
			if err := flowdb.SetMirroredTaskArchivedAt(db, tk.Slug, tk.ArchivedAt); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
		}
	}

	var messagesMigrated, messagesSkipped, messagesFailed int
	for _, m := range messages {
		fm := translateBusMessage(m)
		inserted, err := flowdb.InsertBusMessageIfAbsent(db, fm)
		if err != nil {
			// A message whose task reference no longer resolves (e.g. a
			// foreign key mismatch) shouldn't abort the whole migration —
			// report it and move on.
			fmt.Fprintf(os.Stderr, "warning: skipping message %s: %v\n", m.ID, err)
			messagesFailed++
			continue
		}
		if inserted {
			messagesMigrated++
		} else {
			messagesSkipped++
		}
	}

	fmt.Printf("Projects: %d migrated, %d skipped (native collision)\n", projectsMigrated, projectsSkipped)
	fmt.Printf("Tasks: %d migrated, %d skipped (native collision)\n", tasksMigrated, tasksSkipped)
	fmt.Printf("Messages: %d migrated, %d already present, %d failed\n", messagesMigrated, messagesSkipped, messagesFailed)
	return 0
}

// translateBusMessage maps a real-flow message onto focusflow's
// bus_messages shape. The two schemas model the same concept
// differently: flow's kind ('message'|'broadcast') becomes focusflow's
// Broadcast bool; flow's three-state status ('pending'|'delivered'|
// 'acked') collapses onto focusflow's two-state mail ('unread'|'read') —
// only 'acked' (the recipient actually consumed it) counts as read,
// since focusflow has no equivalent of flow's separate "delivered but
// not yet read" state. Fields with no focusflow equivalent
// (sender_session_id, delivered_at, waited_s, acked_by) are dropped.
func translateBusMessage(m realflow.BusMessage) *flowdb.BusMessage {
	fm := &flowdb.BusMessage{
		ID:           m.ID,
		FromAssignee: m.FromAssignee,
		ToAssignee:   m.ToAssignee,
		Body:         m.Body,
		Urgent:       m.Urgent,
		Broadcast:    m.Kind == "broadcast",
		CreatedAt:    m.CreatedAt,
		Mail:         "unread",
	}
	if m.FromTaskSlug != "" {
		fm.FromTaskSlug = sql.NullString{String: m.FromTaskSlug, Valid: true}
	}
	if m.ToTaskSlug != "" {
		fm.ToTaskSlug = sql.NullString{String: m.ToTaskSlug, Valid: true}
	}
	if m.ReplyTo != "" {
		fm.ReplyTo = sql.NullString{String: m.ReplyTo, Valid: true}
	}
	if m.Status == "acked" {
		fm.Mail = "read"
		readAt := m.AckedAt
		if readAt == "" {
			readAt = m.CreatedAt
		}
		fm.ReadAt = sql.NullString{String: readAt, Valid: true}
	}
	return fm
}
