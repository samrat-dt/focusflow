package app

import (
	"focusflow/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdUpdateTaskWorkDir(t *testing.T) {
	setupRoot(t)
	seedTask(t, "ut-wd")

	newDir := filepath.Join(t.TempDir(), "new-spot")
	if rc := cmdUpdate([]string{"task", "ut-wd", "--work-dir", newDir, "--mkdir"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "ut-wd")
	if task.WorkDir != newDir {
		t.Errorf("work_dir = %q, want %q", task.WorkDir, newDir)
	}
}

func TestCmdUpdateTaskWorkDirMissingNoMkdir(t *testing.T) {
	setupRoot(t)
	seedTask(t, "ut-nomkdir")

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if rc := cmdUpdate([]string{"task", "ut-nomkdir", "--work-dir", missing}); rc != 1 {
		t.Errorf("rc=%d, want 1 when path is missing without --mkdir", rc)
	}
}

func TestCmdUpdateTaskBothFields(t *testing.T) {
	setupRoot(t)
	seedTask(t, "ut-both")

	newDir := filepath.Join(t.TempDir(), "combo")
	if rc := cmdUpdate([]string{"task", "ut-both",
		"--priority", "high", "--work-dir", newDir, "--mkdir"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "ut-both")
	if task.Priority != "high" {
		t.Errorf("priority = %q, want high", task.Priority)
	}
	if task.WorkDir != newDir {
		t.Errorf("work_dir = %q, want %q", task.WorkDir, newDir)
	}
}

func TestCmdUpdateTaskRequiresFlag(t *testing.T) {
	setupRoot(t)
	seedTask(t, "ut-noop")

	if rc := cmdUpdate([]string{"task", "ut-noop"}); rc != 2 {
		t.Errorf("rc=%d, want 2 when no field-changing flag is given", rc)
	}
}

func TestCmdUpdateTaskUnknownTask(t *testing.T) {
	setupRoot(t)
	if rc := cmdUpdate([]string{"task", "nope", "--priority", "high"}); rc != 1 {
		t.Errorf("rc=%d, want 1 for unknown task", rc)
	}
}

func TestCmdUpdateUnknownTarget(t *testing.T) {
	setupRoot(t)
	if rc := cmdUpdate([]string{"playbook", "foo"}); rc != 2 {
		t.Errorf("rc=%d, want 2 for unknown update target", rc)
	}
}

func TestCmdUpdateTaskRejectsLeadingDashAsRef(t *testing.T) {
	setupRoot(t)
	rc := cmdUpdate([]string{"task", "--help"})
	if rc != 2 {
		t.Errorf("update task --help: rc=%d, want 2 (usage error)", rc)
	}
}

func TestCmdUpdateProjectRejectsLeadingDashAsRef(t *testing.T) {
	setupRoot(t)
	rc := cmdUpdate([]string{"project", "--help"})
	if rc != 2 {
		t.Errorf("update project --help: rc=%d, want 2 (usage error)", rc)
	}
}

func TestCmdUpdateProjectPriority(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "Proj", "--slug", "proj", "--work-dir", wd}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}

	if rc := cmdUpdate([]string{"project", "proj", "--priority", "high"}); rc != 0 {
		t.Fatalf("update project --priority rc=%d", rc)
	}

	db := openTestDB(t)
	p, err := store.GetProject(db, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if p.Priority != "high" {
		t.Errorf("priority = %q, want high", p.Priority)
	}
}

func TestCmdUpdateProjectRejectsBadPriority(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "Proj2", "--slug", "proj2", "--work-dir", wd}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	if rc := cmdUpdate([]string{"project", "proj2", "--priority", "urgent"}); rc != 2 {
		t.Errorf("rc=%d, want 2 for invalid priority", rc)
	}
}

func TestCmdUpdateProjectRequiresFlag(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "Proj3", "--slug", "proj3", "--work-dir", wd}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	if rc := cmdUpdate([]string{"project", "proj3"}); rc != 2 {
		t.Errorf("rc=%d, want 2 when no field-changing flag is given", rc)
	}
}

func TestCmdUpdateProjectUnknown(t *testing.T) {
	setupRoot(t)
	if rc := cmdUpdate([]string{"project", "nope", "--priority", "high"}); rc != 1 {
		t.Errorf("rc=%d, want 1 for unknown project", rc)
	}
}

func TestUpdateTask_StatusChangeSetsStatusChangedAt(t *testing.T) {
	setupRoot(t)
	seedTask(t, "status-change-task")

	db := openTestDB(t)
	const oldTimestamp = "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`UPDATE tasks SET status_changed_at = ? WHERE slug = ?`, oldTimestamp, "status-change-task"); err != nil {
		t.Fatal(err)
	}

	if rc := cmdUpdate([]string{"task", "status-change-task", "--status", "in-progress"}); rc != 0 {
		t.Fatalf("update task --status rc=%d", rc)
	}

	after, err := store.GetTask(db, "status-change-task")
	if err != nil {
		t.Fatal(err)
	}
	if !after.StatusChangedAt.Valid || after.StatusChangedAt.String == oldTimestamp {
		t.Errorf("status_changed_at should advance past the pinned old value on a real status transition, still %+v", after.StatusChangedAt)
	}

	// Setting the SAME status again should not bump status_changed_at —
	// only an actual transition counts (matches do.go's own
	// `CASE WHEN status != 'in-progress' THEN ? ELSE status_changed_at END` logic).
	unchanged := after.StatusChangedAt
	if rc := cmdUpdate([]string{"task", "status-change-task", "--status", "in-progress"}); rc != 0 {
		t.Fatalf("update task --status (same value) rc=%d", rc)
	}
	stillSame, err := store.GetTask(db, "status-change-task")
	if err != nil {
		t.Fatal(err)
	}
	if stillSame.StatusChangedAt != unchanged {
		t.Errorf("status_changed_at changed on a same-value --status update: %+v -> %+v", unchanged, stillSame.StatusChangedAt)
	}
}

func TestUpdateTask_StatusPriorityAssigneeDueWaitingTags(t *testing.T) {
	setupRoot(t)
	seedTask(t, "sample-task")

	rc := cmdUpdate([]string{"task", "sample-task",
		"--status", "in-progress",
		"--priority", "high",
		"--assignee", "sam",
		"--due-date", "2026-12-25",
		"--waiting", "design review",
		"--tag", "urgent",
	})
	if rc != 0 {
		t.Fatalf("update task rc=%d", rc)
	}

	db := openTestDB(t)
	task, err := store.GetTask(db, "sample-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != "in-progress" {
		t.Errorf("Status = %q, want in-progress", task.Status)
	}
	if task.Priority != "high" {
		t.Errorf("Priority = %q, want high", task.Priority)
	}
	if !task.Assignee.Valid || task.Assignee.String != "sam" {
		t.Errorf("Assignee = %+v, want sam", task.Assignee)
	}
	if !task.DueDate.Valid || task.DueDate.String != "2026-12-25" {
		t.Errorf("DueDate = %+v, want 2026-12-25", task.DueDate)
	}
	if !task.WaitingOn.Valid || task.WaitingOn.String != "design review" {
		t.Errorf("WaitingOn = %+v, want 'design review'", task.WaitingOn)
	}
	tags, _ := store.ListTagsForTask(db, "sample-task")
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("tags = %v, want [urgent]", tags)
	}

	// --clear-waiting, --clear-due, --clear-assignee, --remove-tag
	rc = cmdUpdate([]string{"task", "sample-task",
		"--clear-waiting", "--clear-due", "--clear-assignee", "--remove-tag", "urgent",
	})
	if rc != 0 {
		t.Fatalf("update task (clears) rc=%d", rc)
	}
	task, _ = store.GetTask(db, "sample-task")
	if task.WaitingOn.Valid {
		t.Errorf("WaitingOn should be cleared, got %+v", task.WaitingOn)
	}
	if task.DueDate.Valid {
		t.Errorf("DueDate should be cleared, got %+v", task.DueDate)
	}
	if task.Assignee.Valid {
		t.Errorf("Assignee should be cleared, got %+v", task.Assignee)
	}
	tags, _ = store.ListTagsForTask(db, "sample-task")
	if len(tags) != 0 {
		t.Errorf("tags after --remove-tag urgent should be empty, got %v", tags)
	}
}

func TestUpdateTask_SessionIDFlagNoLongerExists(t *testing.T) {
	setupRoot(t)
	seedTask(t, "sample-task-2")
	rc := cmdUpdate([]string{"task", "sample-task-2", "--session-id", "11111111-1111-4111-8111-111111111111"})
	if rc != 2 {
		t.Errorf("update task --session-id: rc=%d, want 2 (unknown flag, usage error)", rc)
	}
}

func TestUpdateTask_WaitingRejectsProjectRef(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "Proj", "--slug", "proj", "--work-dir", wd}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	rc := cmdUpdate([]string{"task", "proj", "--waiting", "someone"})
	if rc == 0 {
		t.Error("update task --waiting on a project ref should fail, got rc=0")
	}
}

func TestUpdateTaskRenameAndMoveBetweenProjects(t *testing.T) {
	setupRoot(t)
	for _, slug := range []string{"proj-a", "proj-b"} {
		if rc := cmdAdd([]string{"project", strings.ToUpper(slug), "--slug", slug, "--work-dir", t.TempDir()}); rc != 0 {
			t.Fatalf("add project %s rc=%d", slug, rc)
		}
	}
	if rc := cmdAdd([]string{"task", "Original name", "--slug", "mover", "--project", "proj-a"}); rc != 0 {
		t.Fatal("add task")
	}
	db := openTestDB(t)

	if rc := cmdUpdate([]string{"task", "mover", "--name", "Better name", "--project", "proj-b"}); rc != 0 {
		t.Fatalf("update rc=%d", rc)
	}
	task, _ := store.GetTask(db, "mover")
	if task.Name != "Better name" || task.Slug != "mover" || task.ProjectSlug.String != "proj-b" {
		t.Errorf("after update: %+v", task)
	}

	if rc := cmdUpdate([]string{"task", "mover", "--clear-project"}); rc != 0 {
		t.Fatalf("clear-project rc=%d", rc)
	}
	if task, _ = store.GetTask(db, "mover"); task.ProjectSlug.Valid {
		t.Errorf("project not cleared: %+v", task.ProjectSlug)
	}

	for name, args := range map[string][]string{
		"unknown project":  {"task", "mover", "--project", "nope"},
		"blank name":       {"task", "mover", "--name", "  "},
		"conflicting flag": {"task", "mover", "--project", "proj-a", "--clear-project"},
	} {
		if rc := cmdUpdate(args); rc == 0 {
			t.Errorf("%s: expected failure", name)
		}
	}
	if task, _ = store.GetTask(db, "mover"); task.Name != "Better name" || task.ProjectSlug.Valid {
		t.Errorf("failed updates must not change anything: %+v", task)
	}
}

func TestUpdateProjectRename(t *testing.T) {
	setupRoot(t)
	if rc := cmdAdd([]string{"project", "Old", "--slug", "p", "--work-dir", t.TempDir()}); rc != 0 {
		t.Fatal("add project")
	}
	if rc := cmdUpdate([]string{"project", "p", "--name", "New name"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	p, _ := store.GetProject(openTestDB(t), "p")
	if p.Name != "New name" || p.Slug != "p" {
		t.Errorf("project = %+v", p)
	}
}
