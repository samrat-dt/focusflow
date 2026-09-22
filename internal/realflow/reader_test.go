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
			slug TEXT PRIMARY KEY, name TEXT, status TEXT, priority TEXT, work_dir TEXT
		);
		CREATE TABLE tasks (
			slug TEXT PRIMARY KEY, name TEXT, project_slug TEXT, status TEXT,
			priority TEXT, work_dir TEXT, session_id TEXT
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
