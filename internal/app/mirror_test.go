package app

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"focusflow/internal/flowdb"
	"focusflow/internal/realflow"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mirrorTestEnv wires up an isolated focusflow root (empty, initialized
// DB) and an isolated real-flow root (empty flow.db, reusing focusflow's
// own schema since the mirror only ever reads a handful of named
// columns present in both). Returns the focusflow root dir, the
// focusflow.db handle, and the real flow.db path.
func mirrorTestEnv(t *testing.T) (focusRoot string, focusDB *sql.DB, realDBPath string) {
	t.Helper()
	focusRoot = t.TempDir()
	realRoot := t.TempDir()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("FOCUSFLOW_ROOT", focusRoot)
	t.Setenv("FLOW_ROOT", realRoot)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")

	for _, sub := range []string{"projects", "tasks"} {
		if err := os.MkdirAll(filepath.Join(focusRoot, sub), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	db, err := flowdb.OpenDB(filepath.Join(focusRoot, "focusflow.db"))
	if err != nil {
		t.Fatalf("open focusflow.db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	realDBPath = filepath.Join(realRoot, "flow.db")
	rdb, err := flowdb.OpenDB(realDBPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	rdb.Close()

	return focusRoot, db, realDBPath
}

func insertRealFlowProject(t *testing.T, dbPath, slug, name, status, priority, workDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer db.Close()
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO projects (slug, name, status, priority, work_dir, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		slug, name, status, priority, workDir, now, now,
	); err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
}

func insertRealFlowTask(t *testing.T, dbPath, slug, name string, projectSlug any, status, priority, workDir, sessionID string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	defer db.Close()
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, session_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, projectSlug, status, priority, workDir, sessionID, now, now,
	); err != nil {
		t.Fatalf("insert fixture task: %v", err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return string(sum[:])
}

// TestMirrorFirstTimeCreatesProjectAndTask pins the primary Done-when
// scenario: a brand-new flow task (with project) bound to the current
// session gets mirrored into an empty focusflow.db, source-tagged, and
// gets on-disk brief/updates files.
func TestMirrorFirstTimeCreatesProjectAndTask(t *testing.T) {
	focusRoot, db, realDBPath := mirrorTestEnv(t)
	insertRealFlowProject(t, realDBPath, "proj-a", "Project A", "active", "medium", "/tmp/proj-a")
	insertRealFlowTask(t, realDBPath, "task-a", "Task A", "proj-a", "in-progress", "high", "/tmp/proj-a", "sess-a")

	mirrorFromFlow("sess-a")

	task, err := flowdb.GetTask(db, "task-a")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Name != "Task A" || task.Status != "in-progress" || task.Priority != "high" || task.WorkDir != "/tmp/proj-a" {
		t.Errorf("mirrored task fields mismatch: %+v", task)
	}
	if task.Source != "flow-mirror" {
		t.Errorf("task.Source = %q, want flow-mirror", task.Source)
	}
	if !task.ProjectSlug.Valid || task.ProjectSlug.String != "proj-a" {
		t.Errorf("task.ProjectSlug = %+v, want proj-a", task.ProjectSlug)
	}

	proj, err := flowdb.GetProject(db, "proj-a")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj.Source != "flow-mirror" {
		t.Errorf("proj.Source = %q, want flow-mirror", proj.Source)
	}

	// On-disk files created for both.
	for _, p := range []string{
		filepath.Join(focusRoot, "tasks", "task-a", "brief.md"),
		filepath.Join(focusRoot, "projects", "proj-a", "brief.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
}

// TestMirrorFloatingTask pins the no-project path.
func TestMirrorFloatingTask(t *testing.T) {
	_, db, realDBPath := mirrorTestEnv(t)
	insertRealFlowTask(t, realDBPath, "task-float", "Floating Task", nil, "backlog", "low", "/tmp/float", "sess-f")

	mirrorFromFlow("sess-f")

	task, err := flowdb.GetTask(db, "task-float")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ProjectSlug.Valid {
		t.Errorf("expected floating task, got project_slug=%q", task.ProjectSlug.String)
	}
	if task.Source != "flow-mirror" {
		t.Errorf("task.Source = %q, want flow-mirror", task.Source)
	}
}

// TestMirrorIdempotentRefresh pins the second Done-when scenario:
// re-mirroring an already-mirrored task refreshes fields in place, never
// duplicates the row.
func TestMirrorIdempotentRefresh(t *testing.T) {
	_, db, realDBPath := mirrorTestEnv(t)
	insertRealFlowTask(t, realDBPath, "task-b", "Task B", nil, "backlog", "low", "/tmp/b", "sess-b")

	mirrorFromFlow("sess-b")

	// Mutate the real-flow row (as if the user changed it in a real flow
	// session between two session-starts) and re-mirror.
	rdb, err := sql.Open("sqlite", realDBPath)
	if err != nil {
		t.Fatalf("open fixture flow.db: %v", err)
	}
	if _, err := rdb.Exec(`UPDATE tasks SET status = 'in-progress', priority = 'high' WHERE slug = 'task-b'`); err != nil {
		t.Fatalf("update fixture task: %v", err)
	}
	rdb.Close()

	mirrorFromFlow("sess-b")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = 'task-b'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for task-b after two mirror runs, got %d", count)
	}

	task, err := flowdb.GetTask(db, "task-b")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != "in-progress" || task.Priority != "high" {
		t.Errorf("expected refreshed fields, got status=%q priority=%q", task.Status, task.Priority)
	}
}

// TestMirrorNeverTouchesNativeTask pins the third Done-when scenario: a
// focusflow-native task with a slug that happens to collide with a
// real-flow task's slug is never overwritten by a mirror run.
func TestMirrorNeverTouchesNativeTask(t *testing.T) {
	_, db, realDBPath := mirrorTestEnv(t)

	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, priority, work_dir, source, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'native', ?, ?)`,
		"shared-slug", "My Native Task", "in-progress", "high", "/native/dir", now, now,
	); err != nil {
		t.Fatalf("insert native task: %v", err)
	}

	insertRealFlowTask(t, realDBPath, "shared-slug", "Totally Different Flow Task", nil, "done", "low", "/flow/dir", "sess-collide")

	mirrorFromFlow("sess-collide")

	task, err := flowdb.GetTask(db, "shared-slug")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Name != "My Native Task" || task.Status != "in-progress" || task.Priority != "high" || task.WorkDir != "/native/dir" {
		t.Errorf("native task was modified by mirror run: %+v", task)
	}
	if task.Source != "native" {
		t.Errorf("task.Source = %q, want native (must not flip to flow-mirror)", task.Source)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = 'shared-slug'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for shared-slug, got %d (mirror must not insert a duplicate either)", count)
	}
}

// TestMirrorNeverTouchesNativeProject is the project-table equivalent of
// TestMirrorNeverTouchesNativeTask.
func TestMirrorNeverTouchesNativeProject(t *testing.T) {
	_, db, realDBPath := mirrorTestEnv(t)

	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO projects (slug, name, status, priority, work_dir, source, created_at, updated_at) VALUES (?, ?, 'active', ?, ?, 'native', ?, ?)`,
		"shared-proj", "My Native Project", "high", "/native/proj", now, now,
	); err != nil {
		t.Fatalf("insert native project: %v", err)
	}

	insertRealFlowProject(t, realDBPath, "shared-proj", "Flow Project", "done", "low", "/flow/proj")
	insertRealFlowTask(t, realDBPath, "task-c", "Task C", "shared-proj", "backlog", "low", "/flow/proj", "sess-c")

	mirrorFromFlow("sess-c")

	proj, err := flowdb.GetProject(db, "shared-proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj.Name != "My Native Project" || proj.Source != "native" {
		t.Errorf("native project was modified by mirror run: %+v", proj)
	}

	// The task itself still mirrors in fine — only the colliding project
	// row is protected.
	task, err := flowdb.GetTask(db, "task-c")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Source != "flow-mirror" {
		t.Errorf("task.Source = %q, want flow-mirror", task.Source)
	}
}

// TestMirrorSlowReadNeverBlocks pins the fourth Done-when scenario: a
// hung real-flow read must never delay the session start. Substitutes
// realFlowLookup with a double that blocks well past a shrunk
// mirrorTimeout, and asserts mirrorFromFlow returns promptly and applies
// no write.
func TestMirrorSlowReadNeverBlocks(t *testing.T) {
	_, db, _ := mirrorTestEnv(t)

	oldLookup := realFlowLookup
	oldTimeout := mirrorTimeout
	mirrorTimeout = 30 * time.Millisecond
	unblock := make(chan struct{})
	t.Cleanup(func() {
		realFlowLookup = oldLookup
		mirrorTimeout = oldTimeout
		close(unblock) // let the leaked goroutine finish so it doesn't outlive the test
	})
	realFlowLookup = func(dbPath, sessionID string, busyTimeoutMs int) (*realflow.Task, *realflow.Project, error) {
		<-unblock
		return &realflow.Task{Slug: "should-not-appear", Name: "x", Status: "backlog", Priority: "low", WorkDir: "/x"}, nil, nil
	}

	start := time.Now()
	mirrorFromFlow("sess-slow")
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("mirrorFromFlow took %s, expected it to return promptly after the %s timeout", elapsed, mirrorTimeout)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = 'should-not-appear'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no row written while the read was still hanging, got %d", count)
	}
}

// TestMirrorFailingReadNeverCrashes pins the "failing" half of the fourth
// Done-when scenario: an error from the real-flow read is swallowed, not
// propagated as a panic or a nonzero-looking failure the caller has to
// handle.
func TestMirrorFailingReadNeverCrashes(t *testing.T) {
	_, db, _ := mirrorTestEnv(t)

	oldLookup := realFlowLookup
	t.Cleanup(func() { realFlowLookup = oldLookup })
	realFlowLookup = func(dbPath, sessionID string, busyTimeoutMs int) (*realflow.Task, *realflow.Project, error) {
		return nil, nil, errors.New("simulated real-flow read failure")
	}

	// Must not panic.
	mirrorFromFlow("sess-failing")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows written on a failing read, got %d", count)
	}
}

// TestMirrorNoBoundSession pins the common no-op case: a session that
// isn't bound to any real-flow task (LookupBySessionID returns nil, nil,
// nil) leaves focusflow.db untouched.
func TestMirrorNoBoundSession(t *testing.T) {
	_, db, realDBPath := mirrorTestEnv(t)
	insertRealFlowTask(t, realDBPath, "someone-elses-task", "Not Us", nil, "backlog", "low", "/x", "sess-other")

	mirrorFromFlow("sess-unbound")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows written for an unbound session, got %d", count)
	}
}

// TestMirrorRealFlowDBNeverWritten pins the "real flow is never written
// to" invariant via a checksum before/after, the same rigor CLAUDE.md
// says focusflow-bootstrap used.
func TestMirrorRealFlowDBNeverWritten(t *testing.T) {
	_, _, realDBPath := mirrorTestEnv(t)
	insertRealFlowProject(t, realDBPath, "proj-z", "Project Z", "active", "medium", "/tmp/z")
	insertRealFlowTask(t, realDBPath, "task-z", "Task Z", "proj-z", "in-progress", "high", "/tmp/z", "sess-z")

	before := fileSHA256(t, realDBPath)
	mirrorFromFlow("sess-z")
	after := fileSHA256(t, realDBPath)

	if before != after {
		t.Fatal("real flow.db changed after a mirror run — mirror must be strictly read-only against real flow")
	}
}

// TestMirroredRowsAreMarkedInOutput pins the "clearly marked" Done-when
// requirement: `focusflow show task/project` and `focusflow list
// tasks/projects` must visibly distinguish a mirrored row from a native
// one, so the guard in UpsertMirroredTask/UpsertMirroredProject isn't the
// only place the distinction is legible.
func TestMirroredRowsAreMarkedInOutput(t *testing.T) {
	_, _, realDBPath := mirrorTestEnv(t)
	insertRealFlowProject(t, realDBPath, "mproj", "Mirrored Project", "active", "medium", "/tmp/mproj")
	insertRealFlowTask(t, realDBPath, "mtask", "Mirrored Task", "mproj", "in-progress", "high", "/tmp/mproj", "sess-mark")

	mirrorFromFlow("sess-mark")

	taskOut := captureStdout(t, func() {
		if rc := showTaskCmd([]string{"mtask"}); rc != 0 {
			t.Fatalf("showTaskCmd rc=%d", rc)
		}
	})
	if !strings.Contains(taskOut, "mirror:") {
		t.Errorf("show task output missing a mirror marker:\n%s", taskOut)
	}

	projOut := captureStdout(t, func() {
		if rc := showProjectCmd([]string{"mproj"}); rc != 0 {
			t.Fatalf("showProjectCmd rc=%d", rc)
		}
	})
	if !strings.Contains(projOut, "mirror:") {
		t.Errorf("show project output missing a mirror marker:\n%s", projOut)
	}

	listTasksOut := captureStdout(t, func() {
		if rc := listTasksCmd(nil); rc != 0 {
			t.Fatalf("listTasksCmd rc=%d", rc)
		}
	})
	if !strings.Contains(listTasksOut, "[mirror]") {
		t.Errorf("list tasks output missing a mirror marker:\n%s", listTasksOut)
	}

	listProjectsOut := captureStdout(t, func() {
		if rc := listProjectsCmd(nil); rc != 0 {
			t.Fatalf("listProjectsCmd rc=%d", rc)
		}
	})
	if !strings.Contains(listProjectsOut, "[mirror]") {
		t.Errorf("list projects output missing a mirror marker:\n%s", listProjectsOut)
	}
}

// TestCurrentHarnessSessionID pins the env-var precedence rule from the
// flow skill's §10: Claude Code's CLAUDE_CODE_SESSION_ID wins over
// Codex's CODEX_THREAD_ID when (implausibly) both are set, and "" when
// neither is.
func TestCurrentHarnessSessionID(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	if got := currentHarnessSessionID(); got != "" {
		t.Errorf("got %q, want empty", got)
	}

	t.Setenv("CODEX_THREAD_ID", "codex-1")
	if got := currentHarnessSessionID(); got != "codex-1" {
		t.Errorf("got %q, want codex-1", got)
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-1")
	if got := currentHarnessSessionID(); got != "claude-1" {
		t.Errorf("got %q, want claude-1 (Claude Code takes precedence)", got)
	}
}
