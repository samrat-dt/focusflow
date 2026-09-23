package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"focusflow/internal/flowdb"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListProjects_FormatTSVAccepted(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "proj-a", "Proj A", filepath.Join(root, "x"), "high")
	out := captureStdout(t, func() {
		rc := cmdList([]string{"projects", "--format", "tsv"})
		if rc != 0 {
			t.Fatalf("list projects --format tsv: rc=%d", rc)
		}
	})
	if !strings.Contains(out, "proj-a") {
		t.Errorf("expected proj-a in tsv output, got:\n%s", out)
	}
}

func TestListPlaybooks_FormatTSVAccepted(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "alpha", Name: "Alpha", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	out := captureShowStdout(t, func() {
		rc := cmdList([]string{"playbooks", "--format", "tsv"})
		if rc != 0 {
			t.Fatalf("list playbooks --format tsv: rc=%d", rc)
		}
	})
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected alpha in tsv output, got:\n%s", out)
	}
}

func TestListTags_FormatFlags(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "tagged", "Tagged", "backlog", "medium", filepath.Join(root, "x"), nil)
	if err := flowdb.AddTag(db, "tagged", "urgent"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		rc := cmdList([]string{"tags", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list tags --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 1 || rows[0]["tag"] != "urgent" {
		t.Errorf("rows = %v, want one row with tag urgent", rows)
	}

	tsvOut := captureStdout(t, func() {
		rc := cmdList([]string{"tags", "--format", "tsv"})
		if rc != 0 {
			t.Fatalf("list tags --format tsv: rc=%d", rc)
		}
	})
	if !strings.Contains(tsvOut, "urgent") {
		t.Errorf("expected urgent in tsv output, got:\n%s", tsvOut)
	}

	// --no-color/--no-truncate must at least be accepted (surface parity).
	if rc := cmdList([]string{"tags", "--no-color", "--no-truncate"}); rc != 0 {
		t.Errorf("list tags --no-color --no-truncate: rc=%d", rc)
	}
}

func TestListRuns_FormatFlags(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "p", Name: "P", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	insertRunTaskForSlug(t, db, "p--run-1", "p", wd)

	out := captureShowStdout(t, func() {
		rc := cmdList([]string{"runs", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list runs --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 1 || rows[0]["slug"] != "p--run-1" {
		t.Errorf("rows = %v, want one row with slug p--run-1", rows)
	}

	tsvOut := captureShowStdout(t, func() {
		rc := cmdList([]string{"runs", "--format", "tsv"})
		if rc != 0 {
			t.Fatalf("list runs --format tsv: rc=%d", rc)
		}
	})
	if !strings.Contains(tsvOut, "p--run-1") {
		t.Errorf("expected p--run-1 in tsv output, got:\n%s", tsvOut)
	}

	if rc := cmdList([]string{"runs", "--no-color", "--no-truncate"}); rc != 0 {
		t.Errorf("list runs --no-color --no-truncate: rc=%d", rc)
	}
}

func TestListTasks_TagFilter(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "task-a", "A", "backlog", "medium", filepath.Join(root, "x"), nil)
	insertTask(t, db, "task-b", "B", "backlog", "medium", filepath.Join(root, "x"), nil)
	if err := flowdb.AddTag(db, "task-a", "urgent"); err != nil {
		t.Fatal(err)
	}
	if err := flowdb.AddTag(db, "task-b", "backend"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--tag", "urgent"}); rc != 0 {
			t.Fatalf("list tasks --tag urgent: rc=%d", rc)
		}
	})
	if !strings.Contains(out, "task-a") {
		t.Errorf("expected task-a in output, got:\n%s", out)
	}
	if strings.Contains(out, "task-b") {
		t.Errorf("expected task-b to be filtered out, got:\n%s", out)
	}
}

func TestListTasks_NoTruncateShowsFullLongSlug(t *testing.T) {
	root, db := showListEditDB(t)
	longSlug := "a-very-long-task-slug-that-exceeds-forty-characters-easily"
	insertTask(t, db, longSlug, "Long", "backlog", "medium", filepath.Join(root, "x"), nil)

	truncated := captureStdout(t, func() {
		cmdList([]string{"tasks"})
	})
	if strings.Contains(truncated, longSlug) {
		t.Errorf("expected default output to truncate slug, but found the full slug:\n%s", truncated)
	}

	full := captureStdout(t, func() {
		cmdList([]string{"tasks", "--no-truncate"})
	})
	if !strings.Contains(full, longSlug) {
		t.Errorf("expected --no-truncate output to contain the full slug, got:\n%s", full)
	}
}

func TestListTasks_FormatJSON_CallsPSOnceRegardlessOfTaskCount(t *testing.T) {
	root, db := showListEditDB(t)
	for i := 0; i < 5; i++ {
		slug := fmt.Sprintf("sessioned-%d", i)
		insertTask(t, db, slug, slug, "in-progress", "medium", filepath.Join(root, "x"), nil)
		if _, err := db.Exec(`UPDATE tasks SET session_id = ? WHERE slug = ?`, slug+"-session", slug); err != nil {
			t.Fatal(err)
		}
	}

	origPSLines := psLines
	var calls int
	psLines = func() ([]string, error) {
		calls++
		return nil, nil
	}
	defer func() { psLines = origPSLines }()

	out := captureStdout(t, func() {
		rc := cmdList([]string{"tasks", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list tasks --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	if calls != 1 {
		t.Errorf("psLines() called %d times for 5 sessioned tasks, want exactly 1 (ps should be forked once per render, not once per task)", calls)
	}
}

func TestListTasks_FormatJSON_RefreshesDeadAutoRun(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "auto-dead", "AutoDead", "in-progress", "medium", filepath.Join(root, "x"), nil)

	// A pid guaranteed not to be running: spawn `true`, wait for it to exit.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start throwaway process: %v", err)
	}
	deadPID := cmd.Process.Pid
	cmd.Wait()

	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=? WHERE slug='auto-dead'`,
		deadPID,
	); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		rc := cmdList([]string{"tasks", "--format", "json", "--kind", "all"})
		if rc != 0 {
			t.Fatalf("list tasks --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if rows[0]["auto_run"] != "dead" {
		t.Errorf("auto_run = %v, want dead (list tasks must refresh stale auto_run_status via refreshAutoRunStatus)", rows[0]["auto_run"])
	}

	// The DB row itself should now also read 'dead', not just the rendered output.
	task, err := flowdb.GetTask(db, "auto-dead")
	if err != nil {
		t.Fatal(err)
	}
	if task.AutoRunStatus.String != "dead" {
		t.Errorf("stored AutoRunStatus = %q, want dead", task.AutoRunStatus.String)
	}
}

func TestListTasks_FormatJSON_ExactFieldSet(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "proj", "Proj", filepath.Join(root, "x"), "medium")
	insertTask(t, db, "task-a", "A", "backlog", "medium", filepath.Join(root, "x"), "proj")
	if err := flowdb.AddTag(db, "task-a", "urgent"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		rc := cmdList([]string{"tasks", "--format", "json", "--include-done", "--include-archived", "--kind", "all"})
		if rc != 0 {
			t.Fatalf("list tasks --format json: rc=%d", rc)
		}
	})

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput was:\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]

	wantKeys := []string{"slug", "name", "status", "priority", "project", "live", "updated", "tags", "archived", "auto_run", "age_days"}
	if len(row) != len(wantKeys) {
		t.Errorf("row has %d keys, want %d: got keys %v", len(row), len(wantKeys), keysOf(row))
	}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("missing expected key %q in row: %v", k, row)
		}
	}
	if _, hasKind := row["kind"]; hasKind {
		t.Error("row must NOT have a 'kind' key — live flow never emits one, even under --kind all")
	}
	if row["slug"] != "task-a" {
		t.Errorf("slug = %v, want task-a", row["slug"])
	}
	if row["project"] != "proj" {
		t.Errorf("project = %v, want proj", row["project"])
	}
	tags, ok := row["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("tags = %v, want [urgent]", row["tags"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestCmdListTasksEmpty(t *testing.T) {
	_, _ = showListEditDB(t)
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "(no tasks)") {
		t.Errorf("expected no-tasks msg; out=%q", out)
	}
}

func TestCmdListTasksMixedStatusFilter(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "demo", "Demo", filepath.Join(root, "repo"), "medium")
	insertTask(t, db, "ip", "In-prog", "in-progress", "high", filepath.Join(root, "repo"), "demo")
	insertTask(t, db, "bl", "Backlog", "backlog", "medium", filepath.Join(root, "repo"), "demo")
	insertTask(t, db, "dn", "Done", "done", "low", filepath.Join(root, "repo"), "demo")

	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--status", "in-progress"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "[IP]") || !strings.Contains(out, "ip") {
		t.Errorf("expected only [IP] row; out=%q", out)
	}
	if strings.Contains(out, "[BL]") || strings.Contains(out, "[DN]") {
		t.Errorf("unexpected rows leaked; out=%q", out)
	}
}

func TestCmdListTasksPrioritySort(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "c-low", "c", "backlog", "low", filepath.Join(root, "x"), nil)
	insertTask(t, db, "a-high", "a", "backlog", "high", filepath.Join(root, "x"), nil)
	insertTask(t, db, "b-med", "b", "backlog", "medium", filepath.Join(root, "x"), nil)

	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	ih := strings.Index(out, "a-high")
	im := strings.Index(out, "b-med")
	il := strings.Index(out, "c-low")
	if ih < 0 || im < 0 || il < 0 {
		t.Fatalf("missing rows; out=%q", out)
	}
	if !(ih < im && im < il) {
		t.Errorf("priority order wrong: high=%d, med=%d, low=%d", ih, im, il)
	}
}

func TestCmdListTasksStaleMarker(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "ancient", "A", "in-progress", "high", filepath.Join(root, "x"), nil)
	old := time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET updated_at = ? WHERE slug = ?`, old, "ancient"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "stale (") {
		t.Errorf("expected stale marker; out=%q", out)
	}
}

func TestCmdListTasksWaitingOn(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "waiter", "W", "in-progress", "high", filepath.Join(root, "x"), nil)
	if _, err := db.Exec(`UPDATE tasks SET waiting_on = ? WHERE slug = ?`, "Alice", "waiter"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "[waiting: Alice]") {
		t.Errorf("expected waiting annotation; out=%q", out)
	}
}

func TestCmdListTasksArchivedHiddenByDefault(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "alive", "A", "backlog", "high", filepath.Join(root, "x"), nil)
	// Use backlog (not done) for the archived row so this test isolates
	// archived-visibility from done-visibility.
	insertTask(t, db, "dead", "D", "backlog", "low", filepath.Join(root, "x"), nil)
	if _, err := db.Exec(`UPDATE tasks SET archived_at = ? WHERE slug = ?`, flowdb.NowISO(), "dead"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if strings.Contains(out, "dead") {
		t.Errorf("archived row leaked: %q", out)
	}
	out2 := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--include-archived"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out2, "dead") {
		t.Errorf("archived row missing with --include-archived: %q", out2)
	}
	if !strings.Contains(out2, "(archived)") {
		t.Errorf("archived marker missing: %q", out2)
	}
}

func TestCmdListTasksDoneHiddenByDefault(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "active", "A", "in-progress", "high", filepath.Join(root, "x"), nil)
	insertTask(t, db, "shipped", "S", "done", "high", filepath.Join(root, "x"), nil)

	// Default: done hidden.
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "active") {
		t.Errorf("active task missing: %q", out)
	}
	if strings.Contains(out, "shipped") {
		t.Errorf("done task leaked into default list: %q", out)
	}

	// --include-done: shows everything.
	out = captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--include-done"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "shipped") {
		t.Errorf("done task missing with --include-done: %q", out)
	}

	// Explicit --status done: shows only done (regardless of default).
	out = captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--status", "done"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "shipped") {
		t.Errorf("--status done should show done: %q", out)
	}
	if strings.Contains(out, "active") {
		t.Errorf("--status done should not show in-progress: %q", out)
	}
}

func TestCmdListTasksSinceToday(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "today-task", "A", "backlog", "high", filepath.Join(root, "x"), nil)
	// Set it to 12 hours ago so it's still "today" in any reasonable timezone.
	recent := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET updated_at = ? WHERE slug = ?`, recent, "today-task"); err != nil {
		t.Fatal(err)
	}
	insertTask(t, db, "ancient", "B", "backlog", "high", filepath.Join(root, "x"), nil)
	old := time.Now().Add(-72 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET updated_at = ? WHERE slug = ?`, old, "ancient"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--since", "today"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "today-task") {
		t.Errorf("expected today-task; out=%q", out)
	}
	if strings.Contains(out, "ancient") {
		t.Errorf("unexpected old row; out=%q", out)
	}
}

func TestCmdListTasksSinceMonday(t *testing.T) {
	// Just smoke-test that --since monday parses and runs; the exact
	// filtering semantics are covered by parseSince tests.
	_, _ = showListEditDB(t)
	if rc := cmdList([]string{"tasks", "--since", "monday"}); rc != 0 {
		t.Errorf("rc=%d", rc)
	}
}

func TestCmdListTasksSince7d(t *testing.T) {
	_, _ = showListEditDB(t)
	if rc := cmdList([]string{"tasks", "--since", "7d"}); rc != 0 {
		t.Errorf("rc=%d", rc)
	}
}

func TestCmdListTasksSinceDate(t *testing.T) {
	_, _ = showListEditDB(t)
	if rc := cmdList([]string{"tasks", "--since", "2020-01-01"}); rc != 0 {
		t.Errorf("rc=%d", rc)
	}
}

func TestCmdListTasksSinceBad(t *testing.T) {
	_, _ = showListEditDB(t)
	if rc := cmdList([]string{"tasks", "--since", "garble"}); rc != 2 {
		t.Errorf("rc=%d, want 2", rc)
	}
}

func TestCmdListTasksProjectFilter(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "p1", "P1", filepath.Join(root, "a"), "medium")
	insertProject(t, db, "p2", "P2", filepath.Join(root, "b"), "medium")
	insertTask(t, db, "t1", "T1", "backlog", "high", filepath.Join(root, "a"), "p1")
	insertTask(t, db, "t2", "T2", "backlog", "high", filepath.Join(root, "b"), "p2")
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--project", "p1"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "t1") {
		t.Errorf("missing t1; out=%q", out)
	}
	if strings.Contains(out, "t2") {
		t.Errorf("unexpected t2; out=%q", out)
	}
}

// ---------- projects ----------

func TestCmdListProjectsEmpty(t *testing.T) {
	_, _ = showListEditDB(t)
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"projects"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "(no projects)") {
		t.Errorf("expected no-projects msg; out=%q", out)
	}
}

func TestListProjects_FormatJSON(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "proj-a", "Proj A", filepath.Join(root, "x"), "high")

	out := captureStdout(t, func() {
		rc := cmdList([]string{"projects", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list projects --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput was:\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["slug"] != "proj-a" {
		t.Errorf("slug = %v, want proj-a", rows[0]["slug"])
	}
	if rows[0]["priority"] != "high" {
		t.Errorf("priority = %v, want high", rows[0]["priority"])
	}
}

func TestListProjects_NoTruncateShowsFullLongSlug(t *testing.T) {
	root, db := showListEditDB(t)
	longSlug := "a-very-long-project-slug-that-exceeds-forty-characters-easily"
	insertProject(t, db, longSlug, "Long", filepath.Join(root, "x"), "medium")

	truncated := captureStdout(t, func() {
		cmdList([]string{"projects"})
	})
	if strings.Contains(truncated, longSlug) {
		t.Errorf("expected default output to truncate slug, but found the full slug:\n%s", truncated)
	}

	full := captureStdout(t, func() {
		cmdList([]string{"projects", "--no-truncate"})
	})
	if !strings.Contains(full, longSlug) {
		t.Errorf("expected --no-truncate output to contain the full slug, got:\n%s", full)
	}
}

func TestCmdListProjectsBreakdown(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "big", "Big", filepath.Join(root, "x"), "high")
	insertTask(t, db, "b1", "B1", "in-progress", "medium", filepath.Join(root, "x"), "big")
	insertTask(t, db, "b2", "B2", "in-progress", "medium", filepath.Join(root, "x"), "big")
	insertTask(t, db, "b3", "B3", "backlog", "medium", filepath.Join(root, "x"), "big")
	insertTask(t, db, "b4", "B4", "done", "low", filepath.Join(root, "x"), "big")
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"projects"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "big") {
		t.Errorf("missing project row; out=%q", out)
	}
	if !strings.Contains(out, "2 IP") || !strings.Contains(out, "1 BL") || !strings.Contains(out, "1 DN") {
		t.Errorf("missing breakdown; out=%q", out)
	}
}

func TestCmdListProjectsArchivedHidden(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "live", "L", filepath.Join(root, "x"), "high")
	insertProject(t, db, "gone", "G", filepath.Join(root, "y"), "low")
	if _, err := db.Exec(`UPDATE projects SET archived_at = ? WHERE slug = ?`, flowdb.NowISO(), "gone"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"projects"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if strings.Contains(out, "gone") {
		t.Errorf("archived leaked; out=%q", out)
	}
	out2 := captureStdout(t, func() {
		if rc := cmdList([]string{"projects", "--include-archived"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out2, "gone") {
		t.Errorf("missing archived row; out=%q", out2)
	}
	if !strings.Contains(out2, "(archived)") {
		t.Errorf("missing archived marker; out=%q", out2)
	}
}

func TestCmdListProjectsStatusFilter(t *testing.T) {
	root, db := showListEditDB(t)
	insertProject(t, db, "active-p", "A", filepath.Join(root, "x"), "high")
	insertProject(t, db, "done-p", "D", filepath.Join(root, "y"), "low")
	if _, err := db.Exec(`UPDATE projects SET status = 'done' WHERE slug = ?`, "done-p"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"projects", "--status", "active"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "active-p") || strings.Contains(out, "done-p") {
		t.Errorf("status filter failed; out=%q", out)
	}
}

func TestCmdListBadSub(t *testing.T) {
	_, _ = showListEditDB(t)
	if rc := cmdList(nil); rc != 2 {
		t.Errorf("rc=%d, want 2", rc)
	}
	if rc := cmdList([]string{"nope"}); rc != 2 {
		t.Errorf("rc=%d, want 2", rc)
	}
}

// parseSince unit tests exercise the date logic directly without needing
// to stand up a DB. Kept here next to the command that uses it.
func TestParseSince(t *testing.T) {
	// Fixed "now" on a Wednesday: 2026-04-15 14:00 UTC.
	now := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)

	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"today", time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), false},
		{"monday", time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC), false}, // Mon same week
		{"7d", now.AddDate(0, 0, -7), false},
		{"0d", now, false},
		{"2020-01-01", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{"wat", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseSince(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSince(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSince(%q): unexpected error %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseSince(%q): got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestListPlaybooks_FormatJSON(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "alpha", Name: "Alpha", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}

	out := captureShowStdout(t, func() {
		rc := cmdList([]string{"playbooks", "--format", "json"})
		if rc != 0 {
			t.Fatalf("list playbooks --format json: rc=%d", rc)
		}
	})
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput was:\n%s", err, out)
	}
	if len(rows) != 1 || rows[0]["slug"] != "alpha" {
		t.Errorf("rows = %v, want one row with slug alpha", rows)
	}
}

func TestCmdListPlaybooks(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "alpha", Name: "Alpha", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "beta", Name: "Beta", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}

	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"playbooks"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("expected both playbooks, got:\n%s", out)
	}
}

func TestCmdListPlaybooksFiltersByProject(t *testing.T) {
	setupFlowRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "P", "--slug", "p1", "--work-dir", wd}); rc != 0 {
		t.Fatal()
	}
	db := openFlowDB(t)
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{
		Slug:        "in-p1",
		Name:        "In",
		WorkDir:     wd,
		ProjectSlug: sql.NullString{String: "p1", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "floating", Name: "F", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}

	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"playbooks", "--project", "p1"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "in-p1") {
		t.Errorf("expected in-p1 playbook, got:\n%s", out)
	}
	if strings.Contains(out, "floating") {
		t.Errorf("floating playbook should be filtered out, got:\n%s", out)
	}
}

func TestListTasksDefaultExcludesPlaybookRuns(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()

	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "pb", Name: "PB", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	insertTask(t, db, "regular-1", "Regular 1", "in-progress", "high", wd, nil)
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
		 VALUES ('pb--2026-04-30-10-30', 'pb run', 'in-progress', 'playbook_run', 'pb', 'medium', ?, ?, ?)`,
		wd, now, now,
	); err != nil {
		t.Fatal(err)
	}

	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "regular-1") {
		t.Errorf("regular task missing:\n%s", out)
	}
	if strings.Contains(out, "pb--2026-04-30-10-30") {
		t.Errorf("playbook run should be hidden by default:\n%s", out)
	}
}

func TestListTasksKindOverride(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "pb", Name: "PB", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
		 VALUES ('pb--2026-04-30-10-30', 'r', 'in-progress', 'playbook_run', 'pb', 'medium', ?, ?, ?)`,
		wd, now, now,
	); err != nil {
		t.Fatal(err)
	}
	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--kind", "playbook_run"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "pb--2026-04-30-10-30") {
		t.Errorf("--kind playbook_run should show runs:\n%s", out)
	}
}

func TestListTasksKindAll(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "pb", Name: "PB", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	insertTask(t, db, "regular-1", "Regular 1", "in-progress", "high", wd, nil)
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
		 VALUES ('pb--run', 'r', 'in-progress', 'playbook_run', 'pb', 'medium', ?, ?, ?)`,
		wd, now, now,
	); err != nil {
		t.Fatal(err)
	}
	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"tasks", "--kind", "all"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "regular-1") || !strings.Contains(out, "pb--run") {
		t.Errorf("--kind all should show both:\n%s", out)
	}
}

func TestCmdListRuns(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "p1", Name: "P1", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "p2", Name: "P2", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	now := flowdb.NowISO()
	for _, slug := range []string{"p1--2026-04-30-10-30", "p1--2026-04-30-11-00"} {
		if _, err := db.Exec(
			`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
			 VALUES (?, ?, 'in-progress', 'playbook_run', 'p1', 'medium', ?, ?, ?)`,
			slug, slug, wd, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
		 VALUES ('p2--2026-04-30-10-30', 'p2-r', 'done', 'playbook_run', 'p2', 'medium', ?, ?, ?)`,
		wd, now, now,
	); err != nil {
		t.Fatal(err)
	}

	// All runs
	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"runs"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "p1--") || !strings.Contains(out, "p2--") {
		t.Errorf("expected all runs:\n%s", out)
	}

	// Filtered by playbook
	out = captureShowStdout(t, func() {
		if rc := cmdList([]string{"runs", "p1"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "p1--") {
		t.Errorf("expected p1 runs:\n%s", out)
	}
	if strings.Contains(out, "p2--") {
		t.Errorf("p2 runs should be filtered out:\n%s", out)
	}
}

func TestCmdListRunsByStatus(t *testing.T) {
	setupFlowRoot(t)
	db := openFlowDB(t)
	wd := t.TempDir()
	if err := flowdb.UpsertPlaybook(db, &flowdb.Playbook{Slug: "p", Name: "P", WorkDir: wd}); err != nil {
		t.Fatal(err)
	}
	now := flowdb.NowISO()
	for _, r := range []struct{ slug, status string }{
		{"p--ip", "in-progress"},
		{"p--dn", "done"},
	} {
		if _, err := db.Exec(
			`INSERT INTO tasks (slug, name, status, kind, playbook_slug, priority, work_dir, created_at, updated_at)
			 VALUES (?, ?, ?, 'playbook_run', 'p', 'medium', ?, ?, ?)`,
			r.slug, r.slug, r.status, wd, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}
	out := captureShowStdout(t, func() {
		if rc := cmdList([]string{"runs", "--status", "done"}); rc != 0 {
			t.Fatal()
		}
	})
	if !strings.Contains(out, "p--dn") {
		t.Errorf("expected done run:\n%s", out)
	}
	if strings.Contains(out, "p--ip") {
		t.Errorf("in-progress run should be filtered out:\n%s", out)
	}
}

func TestEnsureUpdatesDir(t *testing.T) {
	// Coverage for the helper used by tests and future commands.
	dir, err := ensureUpdatesDir(t.TempDir(), "tasks", "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir missing: %v", err)
	}
}

func TestCmdListTasksAgeColumn(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "aged", "Aged", "in-progress", "high", filepath.Join(root, "x"), nil)
	// Set status_changed_at to 7 days ago.
	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET status_changed_at = ? WHERE slug = ?`, sevenDaysAgo, "aged"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "7d") {
		t.Errorf("expected age column with 7d; out=%q", out)
	}
}

func TestCmdListTasksDueColumn(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "due-soon", "DS", "backlog", "high", filepath.Join(root, "x"), nil)
	due := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	if _, err := db.Exec(`UPDATE tasks SET due_date = ? WHERE slug = ?`, due, "due-soon"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "due 3d") {
		t.Errorf("expected due 3d; out=%q", out)
	}
}

func TestCmdListTasksOverdue(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "overdue-t", "OD", "in-progress", "high", filepath.Join(root, "x"), nil)
	due := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	if _, err := db.Exec(`UPDATE tasks SET due_date = ? WHERE slug = ?`, due, "overdue-t"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "overdue 2d") {
		t.Errorf("expected overdue marker; out=%q", out)
	}
}

func TestCmdListTasksDueToday(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "due-now", "DN", "in-progress", "high", filepath.Join(root, "x"), nil)
	due := time.Now().Format("2006-01-02")
	if _, err := db.Exec(`UPDATE tasks SET due_date = ? WHERE slug = ?`, due, "due-now"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out, "due today") {
		t.Errorf("expected due today marker; out=%q", out)
	}
}

func TestCmdListTasksConfigurableStaleness(t *testing.T) {
	root, db := showListEditDB(t)
	insertTask(t, db, "cfg-list", "CL", "in-progress", "high", filepath.Join(root, "x"), nil)
	// Updated 2 days ago — below default threshold of 3.
	twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE tasks SET updated_at = ? WHERE slug = ?`, twoDaysAgo, "cfg-list"); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if strings.Contains(out, "⚠ stale") {
		t.Errorf("should not be stale at default threshold; out=%q", out)
	}

	t.Setenv("FOCUSFLOW_STALE_DAYS", "1")
	out2 := captureStdout(t, func() {
		if rc := cmdList([]string{"tasks"}); rc != 0 {
			t.Errorf("rc=%d", rc)
		}
	})
	if !strings.Contains(out2, "⚠ stale") {
		t.Errorf("should be stale with threshold 1; out=%q", out2)
	}
}
