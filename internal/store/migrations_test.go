package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// buildV1DB creates a database exactly as a pre-stages focusflow left it:
// the v1 baseline DDL, user_version 1, and rows that depend on the tasks
// table through foreign keys.
func buildV1DB(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(schemaDDL); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(raw); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`INSERT INTO projects (slug, name, status, priority, work_dir, created_at, updated_at) VALUES ('p','P','active','medium','/p','x','x')`,
		`INSERT INTO tasks (slug, name, project_slug, status, priority, work_dir, session_id, created_at, updated_at) VALUES ('t1','T1','p','in-progress','high','/w','sess-1','x','x')`,
		`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES ('t2','T2','done','low','/w','x','x')`,
		`INSERT INTO task_tags (task_slug, tag, created_at) VALUES ('t1','ui','x')`,
		`INSERT INTO bus_messages (id, from_assignee, to_assignee, to_task_slug, body, mail, created_at) VALUES ('m1','user','user','t1','hi','unread','x')`,
		`PRAGMA user_version = 1`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES ('x','X','paused','low','/w','x','x')`); err == nil {
		t.Fatal("v1 schema must reject the paused status (test precondition)")
	}
}

func TestMigrateV2WidensStatusesPreservingEverything(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focusflow.db")
	buildV1DB(t, path)

	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB on a v1 database: %v", err)
	}
	defer db.Close()
	if v := userVersion(t, db); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}

	for _, st := range Statuses {
		if _, err := db.Exec(`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES (?, 'n', ?, 'low', '/w', 'x', 'x')`, "s-"+st, st); err != nil {
			t.Errorf("status %q rejected after migration: %v", st, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tasks (slug, name, kind, status, priority, work_dir, created_at, updated_at) VALUES ('agent','n','project_agent','backlog','low','/w','x','x')`); err != nil {
		t.Errorf("project_agent kind rejected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (slug, name, kind, status, priority, work_dir, created_at, updated_at) VALUES ('badkind','n','bogus','backlog','low','/w','x','x')`); err == nil {
		t.Error("an invalid kind must still be rejected")
	}
	if _, err := db.Exec(`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES ('bad','n','bogus','low','/w','x','x')`); err == nil {
		t.Error("an invalid status must still be rejected")
	}

	// Data, dependents and constraints survive the rebuild.
	got, err := GetTask(db, "t1")
	if err != nil || got.Name != "T1" || got.ProjectSlug.String != "p" || got.SessionID.String != "sess-1" || got.Status != "in-progress" {
		t.Fatalf("t1 = %+v, %v", got, err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM task_tags WHERE task_slug='t1'`).Scan(&n)
	if n != 1 {
		t.Error("task_tags row lost")
	}
	db.QueryRow(`SELECT COUNT(*) FROM bus_messages WHERE to_task_slug='t1'`).Scan(&n)
	if n != 1 {
		t.Error("bus message lost")
	}
	if _, err := db.Exec(`INSERT INTO tasks (slug, name, status, priority, work_dir, session_id, created_at, updated_at) VALUES ('dup','n','backlog','low','/w','sess-1','x','x')`); err == nil {
		t.Error("the unique session_id index was not recreated")
	}
	if _, err := db.Exec(`INSERT INTO task_tags (task_slug, tag, created_at) VALUES ('ghost','x','x')`); err == nil {
		t.Error("foreign keys must be enforced again after the migration")
	}
	for _, col := range []struct{ table, name string }{{"projects", "agent_task_slug"}, {"tasks", "terminal_ref"}, {"bus_messages", "delivered_at"}} {
		if has, _ := columnExists(db, col.table, col.name); !has {
			t.Errorf("%s.%s missing after v3", col.table, col.name)
		}
	}
	if err := ensureSchema(db); err != nil {
		t.Errorf("re-running ensureSchema on a current DB: %v", err)
	}
}

func TestFreshDatabaseIsCurrentAndAcceptsAllStatuses(t *testing.T) {
	db := openTempDB(t)
	if v := userVersion(t, db); v != SchemaVersion {
		t.Fatalf("fresh version = %d", v)
	}
	if _, err := db.Exec(`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES ('r','r','review','low','/w','x','x')`); err != nil {
		t.Errorf("fresh DB rejects review: %v", err)
	}
	if !IsValidStatus("paused") || IsValidStatus("bogus") {
		t.Error("IsValidStatus")
	}
}
