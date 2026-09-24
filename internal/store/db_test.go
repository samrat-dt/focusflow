package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "focusflow.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertProject(t *testing.T, db *sql.DB, slug, name, wd, priority string) {
	t.Helper()
	now := NowISO()
	_, err := db.Exec(`INSERT INTO projects (slug, name, status, priority, work_dir, created_at, updated_at) VALUES (?, ?, 'active', ?, ?, ?, ?)`,
		slug, name, priority, wd, now, now)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
}

func insertTask(t *testing.T, db *sql.DB, slug, name, status, priority, wd string, project any) {
	t.Helper()
	now := NowISO()
	_, err := db.Exec(`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, name, project, status, priority, wd, now, now)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

func TestOpenDBCreatesSchema(t *testing.T) {
	db := openTempDB(t)
	for _, tbl := range []string{"projects", "tasks", "workdirs"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", tbl, err)
		}
	}
}

func TestOpenDBIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focusflow.db")
	db1, err := OpenDB(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db1.Close()
	db2, err := OpenDB(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	db2.Close()
}

func TestProjectCRUD(t *testing.T) {
	db := openTempDB(t)
	insertProject(t, db, "alpha", "Alpha Project", "/tmp/alpha", "high")
	got, err := GetProject(db, "alpha")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Slug != "alpha" || got.Name != "Alpha Project" || got.Priority != "high" || got.WorkDir != "/tmp/alpha" {
		t.Errorf("unexpected project: %+v", got)
	}
	if _, err := GetProject(db, "nope"); err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestListProjectsFilters(t *testing.T) {
	db := openTempDB(t)
	insertProject(t, db, "alpha", "Alpha", "/tmp/alpha", "high")
	insertProject(t, db, "beta", "Beta", "/tmp/beta", "medium")
	if _, err := db.Exec(`UPDATE projects SET status='done' WHERE slug='beta'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := db.Exec(`UPDATE projects SET archived_at=? WHERE slug='alpha'`, NowISO()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, err := ListProjects(db, ProjectFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "beta" {
		t.Errorf("default filter: got %v", got)
	}
	got, err = ListProjects(db, ProjectFilter{IncludeArchived: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("include archived: got %d", len(got))
	}
}

func TestTaskCRUD(t *testing.T) {
	db := openTempDB(t)
	insertProject(t, db, "proj", "Proj", "/tmp/proj", "medium")
	insertTask(t, db, "work", "Some Work", "backlog", "medium", "/tmp/proj", "proj")
	got, err := GetTask(db, "work")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Slug != "work" || !got.ProjectSlug.Valid || got.ProjectSlug.String != "proj" {
		t.Errorf("unexpected task: %+v", got)
	}
	insertTask(t, db, "float", "Floating", "in-progress", "high", "/tmp/float", nil)
	floating, err := GetTask(db, "float")
	if err != nil {
		t.Fatalf("GetTask floating: %v", err)
	}
	if floating.ProjectSlug.Valid {
		t.Errorf("expected null project_slug")
	}
}

func TestWorkdirUpsert(t *testing.T) {
	db := openTempDB(t)
	if err := UpsertWorkdir(db, "/tmp/repo", "repo", "", "git@github.com:foo/bar.git"); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	got, err := GetWorkdir(db, "/tmp/repo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Name.Valid || got.Name.String != "repo" {
		t.Errorf("name: got %+v", got.Name)
	}
}

func TestNowISO(t *testing.T) {
	s := NowISO()
	if len(s) < 19 {
		t.Errorf("NowISO too short: %q", s)
	}
}

func TestMigrationAddsDueDateAndStatusChangedAt(t *testing.T) {
	db := openTempDB(t)
	for _, col := range []string{"due_date", "status_changed_at"} {
		has, err := columnExists(db, "tasks", col)
		if err != nil {
			t.Fatalf("columnExists(%s): %v", col, err)
		}
		if !has {
			t.Errorf("column %s should exist after migration", col)
		}
	}
}

// TestOpenDBOnPreMigrationDB simulates an existing user upgrading from a
// pre-feat/playbooks focusflow.db: tasks table exists but lacks the kind and
// playbook_slug columns. OpenDB must apply migrations cleanly without
// CREATE INDEX failing on the missing columns.
//
// Regression test: see commit fixing "no such column: kind" — schemaDDL
// used to include `CREATE INDEX ... ON tasks(kind)` which fails before
// runMigrations gets a chance to ALTER TABLE.
func TestOpenDBOnPreMigrationDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")

	// Create a "pre-migration" DB by hand: just the original tasks schema,
	// no kind/playbook_slug columns, no playbooks table.
	pre, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pre.Exec(`
		CREATE TABLE projects (
			slug TEXT PRIMARY KEY, name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active', priority TEXT NOT NULL DEFAULT 'medium',
			work_dir TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
			archived_at TEXT
		);
		CREATE TABLE tasks (
			slug TEXT PRIMARY KEY, name TEXT NOT NULL,
			project_slug TEXT, status TEXT NOT NULL DEFAULT 'backlog',
			priority TEXT NOT NULL DEFAULT 'medium', work_dir TEXT NOT NULL,
			waiting_on TEXT, session_id TEXT, session_started TEXT,
			session_last_resumed TEXT, created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL, archived_at TEXT
		);
		CREATE TABLE workdirs (
			path TEXT PRIMARY KEY, name TEXT, git_remote TEXT,
			last_used_at TEXT, created_at TEXT NOT NULL
		);
		INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at)
			VALUES ('legacy', 'Legacy task', 'in-progress', 'high', '/tmp', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`); err != nil {
		pre.Close()
		t.Fatalf("seed pre-migration DB: %v", err)
	}
	pre.Close()

	// Now reopen via OpenDB — must not error.
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB on pre-migration DB: %v", err)
	}
	defer db.Close()

	// Verify the legacy row is still readable and has kind='regular' default.
	var kind string
	if err := db.QueryRow(`SELECT kind FROM tasks WHERE slug='legacy'`).Scan(&kind); err != nil {
		t.Fatalf("read legacy row after migration: %v", err)
	}
	if kind != "regular" {
		t.Errorf("legacy row kind: got %q, want regular", kind)
	}

	// Verify the new playbooks table is queryable.
	if _, err := db.Exec(`SELECT slug FROM playbooks LIMIT 1`); err != nil {
		t.Errorf("playbooks table not created: %v", err)
	}

	// Verify the new indexes exist.
	for _, idx := range []string{"idx_tasks_kind", "idx_tasks_playbook_slug", "idx_playbooks_project"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("index %s not created: %v", idx, err)
		}
	}
}

func TestPlaybooksTableExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name='playbooks'`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("playbooks table missing")
	}
	rows.Close()

	now := NowISO()
	wd := t.TempDir()
	if _, err := db.Exec(
		`INSERT INTO playbooks (slug, name, work_dir, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"p1", "Playbook 1", wd, now, now,
	); err != nil {
		t.Fatalf("insert playbook: %v", err)
	}
	var slug, name, gotWD string
	err = db.QueryRow(`SELECT slug, name, work_dir FROM playbooks WHERE slug='p1'`).Scan(&slug, &name, &gotWD)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Playbook 1" || gotWD != wd {
		t.Errorf("unexpected: slug=%q name=%q wd=%q", slug, name, gotWD)
	}
}

func TestMigrationAddsTasksKindAndPlaybookSlug(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	hasKind, err := columnExists(db, "tasks", "kind")
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind {
		t.Error("tasks.kind column missing")
	}
	hasPB, err := columnExists(db, "tasks", "playbook_slug")
	if err != nil {
		t.Fatal(err)
	}
	if !hasPB {
		t.Error("tasks.playbook_slug column missing")
	}

	// Default kind should be 'regular' for new rows.
	now := NowISO()
	wd := t.TempDir()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES (?, ?, 'backlog', 'medium', ?, ?, ?)`,
		"t1", "Task 1", wd, now, now,
	); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM tasks WHERE slug='t1'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "regular" {
		t.Errorf("default kind: got %q, want regular", kind)
	}
}

func TestPlaybookCRUD(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "focusflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wd := t.TempDir()
	if err := UpsertPlaybook(db, &Playbook{
		Slug:    "triage-cs",
		Name:    "Triage CS inbox",
		WorkDir: wd,
	}); err != nil {
		t.Fatalf("UpsertPlaybook: %v", err)
	}

	pb, err := GetPlaybook(db, "triage-cs")
	if err != nil {
		t.Fatalf("GetPlaybook: %v", err)
	}
	if pb.Name != "Triage CS inbox" || pb.WorkDir != wd {
		t.Errorf("got %+v", pb)
	}

	pbs, err := ListPlaybooks(db, PlaybookFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pbs) != 1 {
		t.Errorf("ListPlaybooks: got %d, want 1", len(pbs))
	}
}

func TestTaskWithKindAndPlaybookSlug(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "focusflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wd := t.TempDir()
	now := NowISO()
	if err := UpsertPlaybook(db, &Playbook{Slug: "p1", Name: "P1", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
		 VALUES (?, ?, 'backlog', 'playbook_run', ?, 'medium', ?, ?, ?)`,
		"p1--2026-04-30-10-30", "p1 run", "p1", wd, now, now,
	); err != nil {
		t.Fatal(err)
	}

	task, err := GetTask(db, "p1--2026-04-30-10-30")
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != "playbook_run" {
		t.Errorf("Kind: got %q", task.Kind)
	}
	if !task.PlaybookSlug.Valid || task.PlaybookSlug.String != "p1" {
		t.Errorf("PlaybookSlug: got %+v", task.PlaybookSlug)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db.Close()
}

// TestNativeRowsDefaultToSourceNative pins the default: any row inserted
// through the normal `focusflow add` path (no explicit `source` column)
// is 'native' (the only value in use).
func TestNativeRowsDefaultToSourceNative(t *testing.T) {
	db := openTempDB(t)
	insertProject(t, db, "p", "P", "/tmp/p", "medium")
	insertTask(t, db, "t", "T", "backlog", "medium", "/tmp/t", nil)

	p, err := GetProject(db, "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "native" {
		t.Errorf("project.Source = %q, want native", p.Source)
	}
	tk, err := GetTask(db, "t")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Source != "native" {
		t.Errorf("task.Source = %q, want native", tk.Source)
	}
}

func TestMigration_AddsAssigneeHarnessAutoRunTagsColumns(t *testing.T) {
	db := openTempDB(t)

	for _, col := range []string{"assignee", "harness", "auto_run_status", "auto_run_pid", "auto_run_started", "auto_run_finished", "auto_run_log"} {
		has, err := columnExists(db, "tasks", col)
		if err != nil {
			t.Fatalf("columnExists(%s): %v", col, err)
		}
		if !has {
			t.Errorf("expected tasks.%s to exist after migration", col)
		}
	}

	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='task_tags'`).Scan(&tableCount); err != nil {
		t.Fatalf("check task_tags table: %v", err)
	}
	if tableCount != 1 {
		t.Errorf("expected task_tags table to exist, got count=%d", tableCount)
	}

	var idxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_tasks_session_id'`).Scan(&idxCount); err != nil {
		t.Fatalf("check idx_tasks_session_id: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("expected idx_tasks_session_id unique index to exist, got count=%d", idxCount)
	}
}

func TestMigration_OpeningTwiceIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}
	db1.Close()
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("second OpenDB (should be idempotent): %v", err)
	}
	db2.Close()
}

func TestSessionIDUniqueIndex_RejectsDuplicateSessionID(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "task-a", "A", "backlog", "medium", "/tmp", nil)
	insertTask(t, db, "task-b", "B", "backlog", "medium", "/tmp", nil)
	if _, err := db.Exec(`UPDATE tasks SET session_id='dup-session' WHERE slug='task-a'`); err != nil {
		t.Fatalf("set session_id on task-a: %v", err)
	}
	_, err := db.Exec(`UPDATE tasks SET session_id='dup-session' WHERE slug='task-b'`)
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation assigning the same session_id to a second task")
	}
}

func TestTagCRUD(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "tagged-task", "Tagged", "backlog", "medium", "/tmp", nil)

	if err := AddTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := AddTag(db, "tagged-task", "frontend"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	// Adding the same tag twice must not error (idempotent).
	if err := AddTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("AddTag (duplicate): %v", err)
	}

	tags, err := ListTagsForTask(db, "tagged-task")
	if err != nil {
		t.Fatalf("ListTagsForTask: %v", err)
	}
	if got, want := tags, []string{"frontend", "urgent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagsForTask = %v, want %v", got, want)
	}

	all, err := ListAllTags(db)
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllTags: expected 2 distinct tags, got %d (%v)", len(all), all)
	}

	if err := RemoveTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}
	tags, _ = ListTagsForTask(db, "tagged-task")
	if got, want := tags, []string{"frontend"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after RemoveTag: ListTagsForTask = %v, want %v", got, want)
	}

	if err := ClearTags(db, "tagged-task"); err != nil {
		t.Fatalf("ClearTags: %v", err)
	}
	tags, _ = ListTagsForTask(db, "tagged-task")
	if len(tags) != 0 {
		t.Errorf("after ClearTags: expected no tags, got %v", tags)
	}
}

func TestListTasks_FilterByTags_ANDSemantics(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "both-tags", "Both", "backlog", "medium", "/tmp", nil)
	insertTask(t, db, "one-tag", "One", "backlog", "medium", "/tmp", nil)
	AddTag(db, "both-tags", "a")
	AddTag(db, "both-tags", "b")
	AddTag(db, "one-tag", "a")

	tasks, err := ListTasks(db, TaskFilter{Tags: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Slug != "both-tags" {
		t.Errorf("ListTasks with Tags=[a,b] (AND semantics) = %+v, want only both-tags", tasks)
	}
}

func TestListTasks_FilterByTags_CaseInsensitive(t *testing.T) {
	db := openTempDB(t)
	insertTask(t, db, "upper-tagged", "Upper", "backlog", "medium", "/tmp", nil)
	AddTag(db, "upper-tagged", "Urgent")

	tasks, err := ListTasks(db, TaskFilter{Tags: []string{"urgent"}})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Slug != "upper-tagged" {
		t.Errorf("ListTasks with Tags=[urgent] should case-insensitively match stored tag 'Urgent', got %+v", tasks)
	}
}

func TestOpenDBWALForeignKeysAndConcurrentWriters(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "focusflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}

	// foreign_keys must hold on EVERY pooled connection, not just one.
	db.SetMaxOpenConns(4)
	conns := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var fk int
		if err := c.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Fatalf("conn %d foreign_keys = %d (%v)", i, fk, err)
		}
	}
	for _, c := range conns {
		c.Close()
	}

	// Concurrent read-then-write transactions must serialize, never fail BUSY.
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.Begin()
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback()
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM workdirs").Scan(&n); err != nil {
				errs <- err
				return
			}
			if _, err := tx.Exec("INSERT INTO workdirs (path, created_at) VALUES (?, ?)", fmt.Sprintf("/p/%d", i), NowISO()); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent tx failed: %v", err)
		}
	}
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSchemaVersioning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focusflow.db")

	// Fresh DB is stamped current.
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, db); v != SchemaVersion {
		t.Fatalf("fresh user_version = %d, want %d", v, SchemaVersion)
	}

	// A current DB is not re-migrated: a dropped table stays dropped.
	if _, err := db.Exec("DROP TABLE bus_watches"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='bus_watches'").Scan(&n)
	if n != 0 {
		t.Error("current-version DB was needlessly re-migrated")
	}

	// A pre-versioning DB (user_version 0) is upgraded and stamped.
	if _, err := db.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='bus_watches'").Scan(&n)
	if n != 1 || userVersion(t, db) != SchemaVersion {
		t.Errorf("v0 DB not upgraded (bus_watches=%d, version=%d)", n, userVersion(t, db))
	}

	// A DB from the future is refused, not silently opened.
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion+5)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := OpenDB(path); err == nil {
		t.Error("expected ErrSchemaTooNew")
	} else if _, ok := err.(ErrSchemaTooNew); !ok {
		t.Errorf("err = %T %v, want ErrSchemaTooNew", err, err)
	}
}
