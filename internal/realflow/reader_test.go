package realflow

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// fakeFlowDB creates a minimal flow.db fixture at <dir>/flow.db with just
// the columns LookupBySessionID reads. Real flow's actual schema has
// more columns (assignee, harness, auto_run_*, task_tags, ...) but this
// package only ever SELECTs named columns, never `SELECT *`, so the
// fixture only needs to be a superset-compatible subset.
func fakeFlowDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE projects (
			slug TEXT PRIMARY KEY, name TEXT, status TEXT, priority TEXT, work_dir TEXT,
			archived_at TEXT
		);
		CREATE TABLE tasks (
			slug TEXT PRIMARY KEY, name TEXT, project_slug TEXT, status TEXT,
			priority TEXT, work_dir TEXT, session_id TEXT, archived_at TEXT
		);
		CREATE TABLE bus_messages (
			id TEXT PRIMARY KEY, created_at TEXT, kind TEXT, from_assignee TEXT,
			from_task_slug TEXT, to_assignee TEXT, to_task_slug TEXT, body TEXT,
			urgent INTEGER, status TEXT, delivered_at TEXT, acked_at TEXT, reply_to TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	return path
}

func insertFixtureProject(t *testing.T, path, slug, name, status, priority, workDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO projects (slug, name, status, priority, work_dir) VALUES (?, ?, ?, ?, ?)`,
		slug, name, status, priority, workDir); err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
}

func insertFixtureTask(t *testing.T, path, slug, name string, projectSlug any, status, priority, workDir, sessionID string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, session_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		slug, name, projectSlug, status, priority, workDir, sessionID,
	); err != nil {
		t.Fatalf("insert fixture task: %v", err)
	}
}

func insertFixtureProjectArchived(t *testing.T, path, slug, name, status, priority, workDir, archivedAt string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO projects (slug, name, status, priority, work_dir, archived_at) VALUES (?, ?, ?, ?, ?, ?)`,
		slug, name, status, priority, workDir, archivedAt); err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
}

func insertFixtureTaskArchived(t *testing.T, path, slug, name string, projectSlug any, status, priority, workDir, archivedAt string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, archived_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		slug, name, projectSlug, status, priority, workDir, archivedAt,
	); err != nil {
		t.Fatalf("insert fixture task: %v", err)
	}
}

func insertFixtureBusMessage(t *testing.T, path string, id, createdAt, kind, fromAssignee string, fromTaskSlug any, toAssignee string, toTaskSlug any, body string, urgent int, status string, deliveredAt, ackedAt, replyTo any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`INSERT INTO bus_messages (id, created_at, kind, from_assignee, from_task_slug, to_assignee, to_task_slug, body, urgent, status, delivered_at, acked_at, reply_to)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, createdAt, kind, fromAssignee, fromTaskSlug, toAssignee, toTaskSlug, body, urgent, status, deliveredAt, ackedAt, replyTo,
	); err != nil {
		t.Fatalf("insert fixture bus message: %v", err)
	}
}

func TestLookupBySessionIDFloatingTask(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureTask(t, path, "my-task", "My Task", nil, "in-progress", "high", "/tmp/work", "sess-1")

	task, project, err := LookupBySessionID(path, "sess-1", 500)
	if err != nil {
		t.Fatalf("LookupBySessionID: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.Slug != "my-task" || task.Name != "My Task" || task.Status != "in-progress" ||
		task.Priority != "high" || task.WorkDir != "/tmp/work" {
		t.Errorf("task mismatch: %+v", task)
	}
	if task.ProjectSlug != "" {
		t.Errorf("expected floating task (no project), got project_slug=%q", task.ProjectSlug)
	}
	if project != nil {
		t.Errorf("expected nil project for floating task, got %+v", project)
	}
}

func TestLookupBySessionIDWithProject(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureProject(t, path, "proj-a", "Project A", "active", "medium", "/tmp/proj")
	insertFixtureTask(t, path, "task-a", "Task A", "proj-a", "backlog", "low", "/tmp/proj", "sess-2")

	task, project, err := LookupBySessionID(path, "sess-2", 500)
	if err != nil {
		t.Fatalf("LookupBySessionID: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ProjectSlug != "proj-a" {
		t.Errorf("task.ProjectSlug = %q, want proj-a", task.ProjectSlug)
	}
	if project == nil {
		t.Fatal("expected a project, got nil")
	}
	if project.Slug != "proj-a" || project.Name != "Project A" || project.Status != "active" ||
		project.Priority != "medium" || project.WorkDir != "/tmp/proj" {
		t.Errorf("project mismatch: %+v", project)
	}
}

func TestLookupBySessionIDNoMatch(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureTask(t, path, "other-task", "Other", nil, "backlog", "low", "/tmp/x", "sess-other")

	task, project, err := LookupBySessionID(path, "sess-does-not-exist", 500)
	if err != nil {
		t.Fatalf("LookupBySessionID should not error on no match: %v", err)
	}
	if task != nil || project != nil {
		t.Errorf("expected (nil, nil) for unbound session, got task=%+v project=%+v", task, project)
	}
}

func TestLookupBySessionIDEmptySessionID(t *testing.T) {
	path := fakeFlowDB(t)
	task, project, err := LookupBySessionID(path, "", 500)
	if err != nil || task != nil || project != nil {
		t.Errorf("empty sessionID should short-circuit to (nil, nil, nil), got task=%+v project=%+v err=%v", task, project, err)
	}
}

func TestLookupBySessionIDMissingDB(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "flow.db")
	_, _, err := LookupBySessionID(missing, "sess-1", 500)
	if err == nil {
		t.Fatal("expected an error for a missing flow.db, got nil")
	}
}

// TestLookupBySessionIDTaskWithDanglingProjectRef pins the defensive
// fallback: if a task references a project slug that doesn't resolve
// (e.g. flow's own data is mid-edit), the task still mirrors — just
// without a project.
func TestLookupBySessionIDTaskWithDanglingProjectRef(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureTask(t, path, "orphan-task", "Orphan", "ghost-project", "backlog", "low", "/tmp/x", "sess-3")

	task, project, err := LookupBySessionID(path, "sess-3", 500)
	if err != nil {
		t.Fatalf("LookupBySessionID: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task despite dangling project ref")
	}
	if project != nil {
		t.Errorf("expected nil project for dangling ref, got %+v", project)
	}
}

// TestAllProjectsReturnsEverythingIncludingArchived pins the bulk-read
// path used by the one-time migration command: unlike LookupBySessionID
// (which only ever resolves one session-bound task), AllProjects must
// return every project row regardless of archived_at, since the
// migration needs to preserve archived state on the focusflow side too.
func TestAllProjectsReturnsEverythingIncludingArchived(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureProject(t, path, "proj-live", "Live Project", "active", "medium", "/tmp/live")
	insertFixtureProjectArchived(t, path, "proj-dead", "Dead Project", "done", "low", "/tmp/dead", "2026-01-01T00:00:00Z")

	projects, err := AllProjects(path, 500)
	if err != nil {
		t.Fatalf("AllProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d: %+v", len(projects), projects)
	}
	bySlug := map[string]Project{}
	for _, p := range projects {
		bySlug[p.Slug] = p
	}
	if bySlug["proj-live"].ArchivedAt != "" {
		t.Errorf("proj-live.ArchivedAt = %q, want empty", bySlug["proj-live"].ArchivedAt)
	}
	if bySlug["proj-dead"].ArchivedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("proj-dead.ArchivedAt = %q, want 2026-01-01T00:00:00Z", bySlug["proj-dead"].ArchivedAt)
	}
}

func TestAllProjectsEmptyDB(t *testing.T) {
	path := fakeFlowDB(t)
	projects, err := AllProjects(path, 500)
	if err != nil {
		t.Fatalf("AllProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

// TestAllTasksReturnsEverythingIncludingArchived mirrors
// TestAllProjectsReturnsEverythingIncludingArchived for tasks, and also
// covers a floating task (no project_slug).
func TestAllTasksReturnsEverythingIncludingArchived(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureTask(t, path, "task-live", "Live Task", nil, "in-progress", "high", "/tmp/live", "sess-live")
	insertFixtureTaskArchived(t, path, "task-dead", "Dead Task", "proj-a", "done", "low", "/tmp/dead", "2026-02-02T00:00:00Z")

	tasks, err := AllTasks(path, 500)
	if err != nil {
		t.Fatalf("AllTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d: %+v", len(tasks), tasks)
	}
	bySlug := map[string]Task{}
	for _, tk := range tasks {
		bySlug[tk.Slug] = tk
	}
	if bySlug["task-live"].ProjectSlug != "" {
		t.Errorf("task-live.ProjectSlug = %q, want empty (floating)", bySlug["task-live"].ProjectSlug)
	}
	if bySlug["task-live"].ArchivedAt != "" {
		t.Errorf("task-live.ArchivedAt = %q, want empty", bySlug["task-live"].ArchivedAt)
	}
	dead := bySlug["task-dead"]
	if dead.ProjectSlug != "proj-a" || dead.ArchivedAt != "2026-02-02T00:00:00Z" {
		t.Errorf("task-dead mismatch: %+v", dead)
	}
}

// TestAllBusMessagesOrderedByCreatedAt pins the ordering guarantee the
// migration relies on: messages come back oldest-first so a reply
// (reply_to) is always processed after the message it replies to,
// letting the migration insert rows in an order that satisfies
// focusflow's bus_messages(reply_to) foreign key.
func TestAllBusMessagesOrderedByCreatedAt(t *testing.T) {
	path := fakeFlowDB(t)
	insertFixtureBusMessage(t, path, "msg-2", "2026-01-02T00:00:00Z", "message", "user", nil, "user", "task-a",
		"second", 0, "acked", "2026-01-02T00:00:01Z", "2026-01-02T00:00:02Z", "msg-1")
	insertFixtureBusMessage(t, path, "msg-1", "2026-01-01T00:00:00Z", "broadcast", "user", "task-a", "user", nil,
		"first", 1, "pending", nil, nil, nil)

	msgs, err := AllBusMessages(path, 500)
	if err != nil {
		t.Fatalf("AllBusMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].ID != "msg-1" || msgs[1].ID != "msg-2" {
		t.Fatalf("expected chronological order [msg-1, msg-2], got [%s, %s]", msgs[0].ID, msgs[1].ID)
	}

	first := msgs[0]
	if first.Kind != "broadcast" || first.FromAssignee != "user" || first.FromTaskSlug != "task-a" ||
		first.ToAssignee != "user" || first.ToTaskSlug != "" || first.Body != "first" || !first.Urgent ||
		first.Status != "pending" || first.ReplyTo != "" {
		t.Errorf("first message mismatch: %+v", first)
	}

	second := msgs[1]
	if second.Kind != "message" || second.ToTaskSlug != "task-a" || second.Status != "acked" ||
		second.AckedAt != "2026-01-02T00:00:02Z" || second.ReplyTo != "msg-1" {
		t.Errorf("second message mismatch: %+v", second)
	}
}
