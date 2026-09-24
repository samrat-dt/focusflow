package app

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"focusflow/internal/store"
)

func TestStageMovesNextReviewAndDone(t *testing.T) {
	setupRoot(t)
	seedTask(t, "st")
	stubITerm(t) // NEVER call cmdDo without stubbing the terminal: it would open real tabs
	db := openTestDB(t)
	status := func() string { tk, _ := store.GetTask(db, "st"); return tk.Status }

	if cmdSetStage(store.StatusNext, []string{"st"}) != 0 || status() != "next" {
		t.Fatalf("next: %s", status())
	}
	if cmdDo([]string{"st"}) != 0 || status() != "in-progress" {
		t.Fatalf("do must move a next task to in-progress, got %s", status())
	}
	out := captureStdout(t, func() { cmdSetStage(store.StatusReview, []string{"st"}) })
	if status() != "review" || !strings.Contains(out, "approval") {
		t.Fatalf("review: %s %q", status(), out)
	}
	// Resuming work from review goes back to in-progress.
	if cmdDo([]string{"st"}) != 0 || status() != "in-progress" {
		t.Errorf("do from review = %s", status())
	}
	if cmdDone([]string{"st"}) != 0 || status() != "done" {
		t.Errorf("done: %s", status())
	}
	if cmdSetStage(store.StatusNext, []string{"st"}) == 0 {
		t.Error("a done task cannot be moved to next without reopening")
	}
	if cmdSetStage("next", nil) != 2 || cmdSetStage("next", []string{"--x"}) != 2 {
		t.Error("usage errors")
	}
}

func TestUpdateAcceptsAllStages(t *testing.T) {
	setupRoot(t)
	seedTask(t, "u")
	for _, st := range store.Statuses {
		if rc := cmdUpdate([]string{"task", "u", "--status", st}); rc != 0 {
			t.Errorf("--status %s rc=%d", st, rc)
		}
	}
	if cmdUpdate([]string{"task", "u", "--status", "bogus"}) != 2 {
		t.Error("bogus status must be a usage error")
	}
	if cmdList([]string{"tasks", "--status", "bogus"}) != 2 {
		t.Error("list --status bogus must be a usage error")
	}
}

func TestPauseStopsHeadlessRunAndSetsStatus(t *testing.T) {
	setupRoot(t)
	seedTask(t, "hl")
	db := openTestDB(t)
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sleeper.Wait() }()
	t.Cleanup(func() { sleeper.Process.Kill() })
	db.Exec(`UPDATE tasks SET status='in-progress', auto_run_status='running', auto_run_pid=? WHERE slug='hl'`, sleeper.Process.Pid)

	out := captureStdout(t, func() {
		if rc := cmdPause([]string{"hl"}); rc != 0 {
			t.Fatalf("pause rc=%d", rc)
		}
	})
	if !strings.Contains(out, "stopped the headless run") {
		t.Errorf("output %q", out)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("headless process was not terminated")
	}
	tk, _ := store.GetTask(db, "hl")
	if tk.Status != "paused" || tk.AutoRunStatus.String != "stopped" {
		t.Errorf("task = status %s auto_run %s", tk.Status, tk.AutoRunStatus.String)
	}
}

func TestPauseAsksLiveTerminalToWrapUpAndKeepsItOpen(t *testing.T) {
	setupRoot(t)
	seedTask(t, "tty")
	db := openTestDB(t)
	db.Exec(`UPDATE tasks SET status='in-progress', session_id='sess-live' WHERE slug='tty'`)
	old := psLines
	psLines = func() ([]string, error) { return []string{"claude --resume sess-live"}, nil }
	t.Cleanup(func() { psLines = old })

	out := captureStdout(t, func() { cmdPause([]string{"tty"}) })
	if !strings.Contains(out, "asked the open terminal session to wrap up") {
		t.Errorf("output %q", out)
	}
	tk, _ := store.GetTask(db, "tty")
	if tk.Status != "paused" {
		t.Errorf("status = %s", tk.Status)
	}
	msgs, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: "tty"}, false)
	if len(msgs) != 1 || !msgs[0].Urgent || !strings.Contains(msgs[0].Body, "Pause requested") {
		t.Errorf("wrap-up message = %+v", msgs)
	}
	// Two independent states: paused status, session still live.
	if !isSessionLive("sess-live") {
		t.Error("the terminal must be untouched")
	}
	if cmdPause([]string{"nope"}) == 0 {
		t.Error("pausing a missing task must fail")
	}
}

func TestPauseProjectPausesRunningSubtasksOnly(t *testing.T) {
	setupRoot(t)
	cmdAdd([]string{"project", "P", "--slug", "p", "--work-dir", t.TempDir()})
	for _, s := range []string{"a", "b", "c"} {
		cmdAdd([]string{"task", s, "--slug", s, "--project", "p"})
	}
	db := openTestDB(t)
	db.Exec(`UPDATE tasks SET status='in-progress' WHERE slug='a'`)
	db.Exec(`UPDATE tasks SET status='next' WHERE slug='b'`)
	if rc := cmdPause([]string{"project/p"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	for slug, want := range map[string]string{"a": "paused", "b": "paused", "c": "backlog"} {
		if tk, _ := store.GetTask(db, slug); tk.Status != want {
			t.Errorf("%s = %s, want %s", slug, tk.Status, want)
		}
	}
}

func TestDeliveryReceiptViaInboxAndHook(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "rx", "Rx")
	t.Setenv("FOCUSFLOW_TASK", "rx")
	store.InsertBusMessage(db, &store.BusMessage{ID: "m1", FromAssignee: "user", ToAssignee: "user",
		ToTaskSlug: nullStr("rx"), Body: "hello", CreatedAt: store.NowISO()})

	// Sent but not yet seen.
	if m, _ := store.GetBusMessage(db, "m1"); m.DeliveredAt.Valid {
		t.Fatal("a fresh message must not be delivered yet")
	}
	// The next prompt in that session delivers it (and says so).
	out := captureStdoutWithStdin(t, "", func() { cmdHookUserPromptSubmit(nil) })
	if !strings.Contains(out, "1 unread message") {
		t.Errorf("hook context = %q", out)
	}
	m, _ := store.GetBusMessage(db, "m1")
	if !m.DeliveredAt.Valid || m.Mail != "unread" {
		t.Errorf("after hook: %+v", m)
	}
	first := m.DeliveredAt.String
	// Delivery time is kept; popping marks it read.
	time.Sleep(1100 * time.Millisecond)
	popped, _ := store.PopOldestUnread(db, store.MailboxFilter{TaskSlug: "rx"}, false)
	if popped.DeliveredAt.String != first || popped.Mail != "read" {
		t.Errorf("popped = %+v (delivered_at must keep its first value)", popped)
	}

	// JSON exposes delivered_at.
	store.InsertBusMessage(db, &store.BusMessage{ID: "m2", FromAssignee: "user", ToAssignee: "user", ToTaskSlug: nullStr("rx"), Body: "again", CreatedAt: store.NowISO()})
	captureStdout(t, func() { cmdInbox([]string{"--json"}) })
	m2, _ := store.GetBusMessage(db, "m2")
	if !m2.DeliveredAt.Valid {
		t.Error("listing a task's own inbox is a delivery")
	}
	var payload []map[string]any
	out = captureStdout(t, func() { cmdInbox([]string{"--json"}) })
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload) == 0 || payload[0]["delivered_at"] == nil {
		t.Errorf("inbox --json = %q (%v)", out, err)
	}
}

func TestStatsJSON(t *testing.T) {
	setupRoot(t)
	seedTask(t, "s1")
	cmdSetStage(store.StatusReview, []string{"s1"})
	out := captureStdout(t, func() { cmdStats([]string{"--json"}) })
	var j statsJSON
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		t.Fatalf("json %q: %v", out, err)
	}
	if j.Tasks["review"] != 1 || j.Tasks["backlog"] != 0 || j.Projects == nil {
		t.Errorf("stats json = %+v", j)
	}
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
