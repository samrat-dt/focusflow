package app

import (
	"database/sql"
	"focusflow/internal/flowdb"
	"path/filepath"
	"strings"
	"testing"
)

// insertRealFlowProjectArchived and insertRealFlowTaskArchived extend
// mirror_test.go's fixture helpers with an archived_at value, for
// exercising the migration's archived-state carry-over.
func insertRealFlowProjectArchived(t *testing.T, dbPath, slug, name, status, priority, workDir, archivedAt string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer db.Close()
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO projects (slug, name, status, priority, work_dir, archived_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, status, priority, workDir, archivedAt, now, now,
	); err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
}

func insertRealFlowTaskArchived(t *testing.T, dbPath, slug, name string, projectSlug any, status, priority, workDir, archivedAt string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer db.Close()
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, archived_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, projectSlug, status, priority, workDir, archivedAt, now, now,
	); err != nil {
		t.Fatalf("insert fixture task: %v", err)
	}
}

func insertRealFlowBusMessage(t *testing.T, dbPath, id, createdAt, kind, fromAssignee string, fromTaskSlug any, toAssignee string, toTaskSlug any, body string, status string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO bus_messages (id, created_at, kind, from_assignee, from_task_slug, to_assignee, to_task_slug, body, urgent, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, createdAt, kind, fromAssignee, fromTaskSlug, toAssignee, toTaskSlug, body, status,
	); err != nil {
		t.Fatalf("insert fixture bus message: %v", err)
	}
}

// migrateTestEnv is mirrorTestEnv's sibling for migrate_test.go: an
// isolated focusflow root/db and an isolated real-flow root/db, but
// using focusflow's own schema (via flowdb.OpenDB) as the fixture flow.db
// shape plus the bus_messages columns the migration reads, since flow's
// real bus_messages table has a different shape than focusflow's own.
func migrateTestEnv(t *testing.T) (focusDB *sql.DB, realDBPath string) {
	t.Helper()
	_, focusDB, realDBPath = mirrorTestEnv(t)

	rdb, err := sql.Open("sqlite", realDBPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer rdb.Close()
	if _, err := rdb.Exec(`
		DROP TABLE bus_messages;
		CREATE TABLE bus_messages (
			id TEXT PRIMARY KEY, created_at TEXT, kind TEXT, from_assignee TEXT,
			from_task_slug TEXT, to_assignee TEXT, to_task_slug TEXT, body TEXT,
			urgent INTEGER, status TEXT, delivered_at TEXT, acked_at TEXT, reply_to TEXT
		);
	`); err != nil {
		t.Fatalf("create fixture bus_messages: %v", err)
	}
	return focusDB, realDBPath
}

// TestMigrateFromFlowCopiesAllProjectsAndTasks pins the primary
// Done-when scenario: every real-flow project/task, including archived
// ones, ends up mirrored into focusflow.db in one pass — not just the
// one bound to the current session, which is all the SessionStart mirror
// ever reaches.
func TestMigrateFromFlowCopiesAllProjectsAndTasks(t *testing.T) {
	focusDB, realDBPath := migrateTestEnv(t)
	insertRealFlowProject(t, realDBPath, "proj-a", "Project A", "active", "medium", "/tmp/proj-a")
	insertRealFlowTask(t, realDBPath, "task-a", "Task A", "proj-a", "in-progress", "high", "/tmp/proj-a", "")
	insertRealFlowTaskArchived(t, realDBPath, "task-old", "Old Task", nil, "done", "low", "/tmp/old", "2026-01-01T00:00:00Z")
	insertRealFlowProjectArchived(t, realDBPath, "proj-old", "Old Project", "done", "low", "/tmp/old-proj", "2026-01-02T00:00:00Z")

	if rc := cmdMigrateFromFlow(nil); rc != 0 {
		t.Fatalf("cmdMigrateFromFlow rc=%d", rc)
	}

	task, err := flowdb.GetTask(focusDB, "task-a")
	if err != nil {
		t.Fatalf("GetTask task-a: %v", err)
	}
	if task.Source != "flow-mirror" || task.Status != "in-progress" {
		t.Errorf("task-a mismatch: %+v", task)
	}

	old, err := flowdb.GetTask(focusDB, "task-old")
	if err != nil {
		t.Fatalf("GetTask task-old: %v", err)
	}
	if !old.ArchivedAt.Valid || old.ArchivedAt.String != "2026-01-01T00:00:00Z" {
		t.Errorf("task-old.ArchivedAt = %+v, want 2026-01-01T00:00:00Z", old.ArchivedAt)
	}

	oldProj, err := flowdb.GetProject(focusDB, "proj-old")
	if err != nil {
		t.Fatalf("GetProject proj-old: %v", err)
	}
	if !oldProj.ArchivedAt.Valid || oldProj.ArchivedAt.String != "2026-01-02T00:00:00Z" {
		t.Errorf("proj-old.ArchivedAt = %+v, want 2026-01-02T00:00:00Z", oldProj.ArchivedAt)
	}
}

// TestMigrateFromFlowIsIdempotent pins re-run safety: running the
// migration twice must not error, duplicate rows, or clobber fields a
// user may have since edited natively... except mirror rows always
// refresh from flow, which is the documented mirror contract.
func TestMigrateFromFlowIsIdempotent(t *testing.T) {
	focusDB, realDBPath := migrateTestEnv(t)
	insertRealFlowTask(t, realDBPath, "task-b", "Task B", nil, "backlog", "low", "/tmp/b", "")

	if rc := cmdMigrateFromFlow(nil); rc != 0 {
		t.Fatalf("first run rc=%d", rc)
	}
	if rc := cmdMigrateFromFlow(nil); rc != 0 {
		t.Fatalf("second run rc=%d", rc)
	}

	var count int
	if err := focusDB.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = 'task-b'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for task-b after two migration runs, got %d", count)
	}
}

// TestMigrateFromFlowNeverTouchesNativeRows is the migration's version
// of mirror_test.go's native-collision guard.
func TestMigrateFromFlowNeverTouchesNativeRows(t *testing.T) {
	focusDB, realDBPath := migrateTestEnv(t)

	now := flowdb.NowISO()
	if _, err := focusDB.Exec(
		`INSERT INTO tasks (slug, name, status, priority, work_dir, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'native', ?, ?)`,
		"shared-slug", "My Native Task", "in-progress", "high", "/native/dir", now, now,
	); err != nil {
		t.Fatalf("insert native task: %v", err)
	}
	insertRealFlowTask(t, realDBPath, "shared-slug", "Flow Task", nil, "done", "low", "/flow/dir", "")

	if rc := cmdMigrateFromFlow(nil); rc != 0 {
		t.Fatalf("cmdMigrateFromFlow rc=%d", rc)
	}

	task, err := flowdb.GetTask(focusDB, "shared-slug")
	if err != nil {
		t.Fatal(err)
	}
	if task.Source != "native" || task.Name != "My Native Task" {
		t.Errorf("native task was modified by migration: %+v", task)
	}
}

// TestMigrateFromFlowCopiesMessagesPreservingReplyOrder pins the
// message-history backfill: historical messages come across with acked
// ones landing as read, pending ones as unread, and a reply chain intact
// even though focusflow's bus_messages(reply_to) is a foreign key.
func TestMigrateFromFlowCopiesMessagesPreservingReplyOrder(t *testing.T) {
	focusDB, realDBPath := migrateTestEnv(t)
	insertRealFlowTask(t, realDBPath, "task-a", "Task A", nil, "backlog", "low", "/tmp/a", "")
	insertRealFlowBusMessage(t, realDBPath, "msg-1", "2026-01-01T00:00:00Z", "message", "user", nil, "user", "task-a", "hello", "acked")

	rdb, err := sql.Open("sqlite", realDBPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	if _, err := rdb.Exec(`INSERT INTO bus_messages (id, created_at, kind, from_assignee, from_task_slug, to_assignee, to_task_slug, body, urgent, status, reply_to)
		VALUES ('msg-2', '2026-01-01T00:01:00Z', 'message', 'user', 'task-a', 'user', NULL, 'reply body', 0, 'pending', 'msg-1')`); err != nil {
		t.Fatalf("insert reply fixture: %v", err)
	}
	rdb.Close()

	if rc := cmdMigrateFromFlow(nil); rc != 0 {
		t.Fatalf("cmdMigrateFromFlow rc=%d", rc)
	}

	m1, err := flowdb.GetBusMessage(focusDB, "msg-1")
	if err != nil {
		t.Fatalf("GetBusMessage msg-1: %v", err)
	}
	if m1.Mail != "read" || m1.ToTaskSlug.String != "task-a" {
		t.Errorf("msg-1 mismatch: %+v", m1)
	}

	m2, err := flowdb.GetBusMessage(focusDB, "msg-2")
	if err != nil {
		t.Fatalf("GetBusMessage msg-2: %v", err)
	}
	if m2.Mail != "unread" || !m2.ReplyTo.Valid || m2.ReplyTo.String != "msg-1" {
		t.Errorf("msg-2 mismatch: %+v", m2)
	}
}

// TestMigrateFromFlowPrintsSummary pins a minimal output contract: the
// command reports what it did, since it's a one-time, user-invoked
// operation the user needs to be able to eyeball.
func TestMigrateFromFlowPrintsSummary(t *testing.T) {
	_, realDBPath := migrateTestEnv(t)
	insertRealFlowTask(t, realDBPath, "task-sum", "Task Sum", nil, "backlog", "low", "/tmp/sum", "")

	out := captureStdout(t, func() {
		if rc := cmdMigrateFromFlow(nil); rc != 0 {
			t.Fatalf("cmdMigrateFromFlow rc=%d", rc)
		}
	})
	if !strings.Contains(out, "Tasks") {
		t.Errorf("expected summary output to mention tasks, got:\n%s", out)
	}
}

// TestMigrateFromFlowMissingRealFlow pins the no-real-flow-installed
// case: a clean, friendly error rather than a panic or stack trace.
func TestMigrateFromFlowMissingRealFlow(t *testing.T) {
	focusRoot := t.TempDir()
	realRoot := t.TempDir() // no flow.db created here
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FOCUSFLOW_ROOT", focusRoot)
	t.Setenv("FLOW_ROOT", realRoot)
	if _, err := flowdb.OpenDB(filepath.Join(focusRoot, "focusflow.db")); err != nil {
		t.Fatalf("init focusflow.db: %v", err)
	}

	rc := cmdMigrateFromFlow(nil)
	if rc == 0 {
		t.Fatal("expected non-zero rc when real flow.db doesn't exist")
	}
}
