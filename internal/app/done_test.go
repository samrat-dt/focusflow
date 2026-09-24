package app

import (
	"errors"
	"focusflow/internal/store"
	"testing"
)

// stubClaudeRunner replaces claudeRunner with a capturing stub that returns
// the supplied error. Returns a *call counter and a *captured-args record so
// tests can assert how the runner was invoked.
type capturedClaudeCall struct {
	slug   string
	prompt string
}

func stubClaudeRunner(t *testing.T, retErr error) *[]capturedClaudeCall {
	t.Helper()
	old := claudeRunner
	calls := &[]capturedClaudeCall{}
	claudeRunner = func(slug, prompt string) error {
		*calls = append(*calls, capturedClaudeCall{slug: slug, prompt: prompt})
		return retErr
	}
	t.Cleanup(func() { claudeRunner = old })
	return calls
}

func TestCmdDone_MarksAutoRunCompleted(t *testing.T) {
	setupRoot(t)
	stubClaudeRunner(t, nil)
	seedTask(t, "auto-done-task")

	db := openTestDB(t)
	now := store.NowISO()
	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=1, auto_run_started=? WHERE slug='auto-done-task'`,
		now,
	); err != nil {
		t.Fatalf("seed running auto_run: %v", err)
	}

	if rc := cmdDone([]string{"auto-done-task"}); rc != 0 {
		t.Fatalf("done rc=%d", rc)
	}

	task, err := store.GetTask(db, "auto-done-task")
	if err != nil {
		t.Fatal(err)
	}
	if !task.AutoRunStatus.Valid || task.AutoRunStatus.String != "completed" {
		t.Errorf("AutoRunStatus = %+v, want completed", task.AutoRunStatus)
	}
	if !task.AutoRunFinished.Valid {
		t.Error("AutoRunFinished should be set")
	}
}

func TestCmdDoneRejectsLeadingDashAsRef(t *testing.T) {
	setupRoot(t)
	rc := cmdDone([]string{"--help"})
	if rc != 2 {
		t.Errorf("done --help: rc=%d, want 2 (usage error)", rc)
	}
}

func TestCmdDoneHappyPath(t *testing.T) {
	setupRoot(t)
	stubClaudeRunner(t, nil) // no session, won't fire — but safe
	if rc := cmdAdd([]string{"task", "Some Task"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	if rc := cmdDone([]string{"some-task"}); rc != 0 {
		t.Fatalf("done rc=%d", rc)
	}
	db := openTestDB(t)
	task, err := store.GetTask(db, "some-task")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Errorf("status: got %q, want done", task.Status)
	}
}

func TestCmdDoneUnknownRef(t *testing.T) {
	setupRoot(t)
	stubClaudeRunner(t, nil)
	if rc := cmdDone([]string{"nope"}); rc == 0 {
		t.Error("expected rc!=0 for unknown task")
	}
}

func TestCmdDoneIdempotent(t *testing.T) {
	setupRoot(t)
	stubClaudeRunner(t, nil)
	if rc := cmdAdd([]string{"task", "Idem"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	if rc := cmdDone([]string{"idem"}); rc != 0 {
		t.Fatalf("first done rc=%d", rc)
	}
	// After it's done, findTask still resolves it (exact match returns
	// archived-aware result). A second done should either succeed (status
	// already done → UPDATE is a no-op writing same value) or be rejected
	// cleanly. Our implementation allows re-marking — it's idempotent in
	// effect.
	if rc := cmdDone([]string{"idem"}); rc != 0 {
		t.Errorf("second done rc=%d, want 0 (idempotent)", rc)
	}
}

func TestCmdDoneNoArgs(t *testing.T) {
	if rc := cmdDone(nil); rc != 2 {
		t.Errorf("rc=%d, want 2", rc)
	}
}

// TestCmdDoneSkipsSweepWhenNoSession verifies that a task with no
// session_id does not trigger a sweep. Done flips status and returns
// immediately; the runner is never called.
func TestCmdDoneSkipsSweepWhenNoSession(t *testing.T) {
	setupRoot(t)
	calls := stubClaudeRunner(t, errors.New("should not be called"))
	if rc := cmdAdd([]string{"task", "No Session Task"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	if rc := cmdDone([]string{"no-session-task"}); rc != 0 {
		t.Fatalf("done rc=%d", rc)
	}
	if len(*calls) != 0 {
		t.Errorf("expected 0 sweep calls, got %d", len(*calls))
	}
}

// TestCmdDoneRunsSweepWhenSessionExists verifies that done invokes the
// claude runner exactly once with the task slug and a sweep prompt
// when the task has a session_id, and returns rc=0 on success.
func TestCmdDoneRunsSweepWhenSessionExists(t *testing.T) {
	setupRoot(t)
	calls := stubClaudeRunner(t, nil)
	if rc := cmdAdd([]string{"task", "Has Session"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	// Manually populate session_id so the sweep gate fires.
	db := openTestDB(t)
	if _, err := db.Exec(
		`UPDATE tasks SET session_id=?, session_started=? WHERE slug=?`,
		"deadbeef-uuid", store.NowISO(), "has-session",
	); err != nil {
		t.Fatalf("seed session_id: %v", err)
	}
	db.Close()

	if rc := cmdDone([]string{"has-session"}); rc != 0 {
		t.Fatalf("done rc=%d, want 0", rc)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 sweep call, got %d", len(*calls))
	}
	got := (*calls)[0]
	if got.slug != "has-session" {
		t.Errorf("call slug = %q, want has-session", got.slug)
	}
	if got.prompt == "" {
		t.Error("call prompt is empty")
	}
	// Sanity-check the prompt mentions key behavior so a regression in
	// buildKBSweepPrompt that drops the skill load or the transcript
	// step gets caught here.
	for _, want := range []string{"focusflow skill", "focusflow transcript has-session", "kb/"} {
		if !contains(got.prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

// TestCmdDoneSweepFailureStillSucceeds verifies that a non-zero exit
// from the sweep runner does NOT fail the done command — the status
// flip is the durability boundary, the sweep is best-effort.
func TestCmdDoneSweepFailureStillSucceeds(t *testing.T) {
	setupRoot(t)
	stubClaudeRunner(t, errors.New("exec: claude: executable file not found in $PATH"))
	if rc := cmdAdd([]string{"task", "Sweep Fail"}); rc != 0 {
		t.Fatalf("add rc=%d", rc)
	}
	db := openTestDB(t)
	if _, err := db.Exec(
		`UPDATE tasks SET session_id=?, session_started=? WHERE slug=?`,
		"sf-uuid", store.NowISO(), "sweep-fail",
	); err != nil {
		t.Fatalf("seed session_id: %v", err)
	}
	db.Close()

	if rc := cmdDone([]string{"sweep-fail"}); rc != 0 {
		t.Errorf("done rc=%d, want 0 even when sweep fails", rc)
	}
	// Status must still be flipped despite the sweep failure.
	db = openTestDB(t)
	task, err := store.GetTask(db, "sweep-fail")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "done" {
		t.Errorf("status = %q, want done", task.Status)
	}
}

// contains is a tiny strings.Contains shim so done_test.go doesn't need
// a strings import just for this.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
