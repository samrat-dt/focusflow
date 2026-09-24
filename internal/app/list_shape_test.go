package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// list projects shape: slug, name, priority, status,
// total, in_progress, backlog, done, updated (archived only when true).
func TestListProjectsJSONAndTSVMatchFlowShape(t *testing.T) {
	setupRoot(t)
	if rc := cmdAdd([]string{"project", "Proj A", "--slug", "proj-a", "--work-dir", t.TempDir()}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	if rc := cmdAdd([]string{"task", "T1", "--slug", "t1", "--project", "proj-a"}); rc != 0 {
		t.Fatalf("add task rc=%d", rc)
	}

	out := captureStdout(t, func() { cmdList([]string{"projects", "--format", "json"}) })
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("json = %q (%v)", out, err)
	}
	for _, k := range []string{"slug", "name", "priority", "status", "total", "in_progress", "backlog", "next", "paused", "review", "done", "updated"} {
		if _, ok := rows[0][k]; !ok {
			t.Errorf("missing key %q: %v", k, rows[0])
		}
	}
	if len(rows[0]) != 12 {
		t.Errorf("want exactly 12 keys (archived omitted when false), got %v", rows[0])
	}
	if rows[0]["total"] != float64(1) || rows[0]["backlog"] != float64(1) {
		t.Errorf("counts = %v", rows[0])
	}

	tsv := captureStdout(t, func() { cmdList([]string{"projects", "--format", "tsv"}) })
	lines := strings.Split(strings.TrimRight(tsv, "\n"), "\n")
	if lines[0] != "slug\tpriority\tstatus\ttotal\tin_progress\tbacklog\tnext\tpaused\treview\tdone\tarchived" {
		t.Errorf("tsv header = %q", lines[0])
	}
	if lines[1] != "proj-a\tmedium\tactive\t1\t0\t1\t0\t0\t0\t0\t" {
		t.Errorf("tsv row = %q", lines[1])
	}
}

// TSV booleans are empty when false, "true" when set — .
func TestListTasksTSVBlankFalseBooleans(t *testing.T) {
	setupRoot(t)
	seedTask(t, "plain")
	tsv := captureStdout(t, func() { cmdList([]string{"tasks", "--format", "tsv"}) })
	row := strings.Split(strings.Split(strings.TrimRight(tsv, "\n"), "\n")[1], "\t")
	// columns: ... stale(7) ... live(11) ... archived(14) tags(15)
	for _, i := range []int{7, 11, 14} {
		if row[i] != "" {
			t.Errorf("column %d = %q, want empty", i, row[i])
		}
	}
}

func TestShowTaskPrintsTagsLine(t *testing.T) {
	setupRoot(t)
	if rc := cmdAdd([]string{"task", "Tagged", "--slug", "tagged", "--tag", "ui", "--tag", "fork"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	out := captureStdout(t, func() { cmdShow([]string{"task", "tagged"}) })
	if !strings.Contains(out, "tags:          #fork #ui") {
		t.Errorf("show task missing tags line:\n%s", out)
	}
}
