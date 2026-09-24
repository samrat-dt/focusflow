package app

import (
	"database/sql"
	"encoding/json"
	"focusflow/internal/store"
	"strings"
	"testing"
	"time"
)

func TestWatchAddListRemove(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "src", "Src")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("FOCUSFLOW_TASK", "")

	if rc := cmdWatch([]string{"src"}); rc != 0 {
		t.Fatalf("watch rc=%d", rc)
	}
	out := captureStdout(t, func() { cmdWatch([]string{"--list"}) })
	if strings.TrimSpace(out) != "src" {
		t.Errorf("--list = %q, want src", out)
	}
	if rc := cmdWatch([]string{"--rm", "src"}); rc != 0 {
		t.Fatalf("rm rc=%d", rc)
	}
	out = captureStdout(t, func() { cmdWatch([]string{"--list"}) })
	if !strings.Contains(out, "user watches nothing") {
		t.Errorf("--list after rm = %q", out)
	}
	if rc := cmdWatch([]string{"--rm", "src"}); rc == 0 {
		t.Error("removing a missing watch should fail")
	}
	if rc := cmdWatch([]string{}); rc != 2 {
		t.Errorf("no-arg rc=%d, want 2", rc)
	}
	if rc := cmdWatch([]string{"user"}); rc != 2 {
		t.Errorf("self-watch rc=%d, want 2", rc)
	}
}

func TestBroadcastFansOutToTaskAndAssigneeWatchers(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "sender", "Sender")
	insertTaskForHookTest(t, db, "listener", "Listener")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("FOCUSFLOW_TASK", "")
	if err := store.Watch(db, "listener", "sender"); err != nil {
		t.Fatal(err)
	}
	if err := store.Watch(db, "someone", "sender"); err != nil { // assignee-level watcher
		t.Fatal(err)
	}
	if err := store.Watch(db, "user", "sender"); err != nil { // human already gets the original
		t.Fatal(err)
	}

	t.Setenv("FOCUSFLOW_TASK", "sender")
	if rc := cmdBroadcast([]string{"shipped"}); rc != 0 {
		t.Fatalf("broadcast rc=%d", rc)
	}

	human, _ := store.ListBusMessages(db, store.MailboxFilter{Assignee: "user"}, false)
	if len(human) != 1 || !human[0].Broadcast {
		t.Errorf("human mailbox = %+v, want the single original", human)
	}
	task, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: "listener"}, false)
	if len(task) != 1 || task[0].Body != "shipped" || !task[0].Broadcast {
		t.Errorf("listener mailbox = %+v", task)
	}
	asg, _ := store.ListBusMessages(db, store.MailboxFilter{Assignee: "someone"}, false)
	if len(asg) != 1 {
		t.Errorf("assignee watcher mailbox = %+v", asg)
	}
}

func TestInboxStats(t *testing.T) {
	db := hookTestDB(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("FOCUSFLOW_TASK", "")
	mk := func(id string, broadcast bool, mail, created string, read any) {
		t.Helper()
		b := 0
		if broadcast {
			b = 1
		}
		if _, err := db.Exec(`INSERT INTO bus_messages (id, from_assignee, to_assignee, body, broadcast, mail, created_at, read_at)
			VALUES (?, 'user', 'user', 'x', ?, ?, ?, ?)`, id, b, mail, created, read); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", false, "read", "2026-01-01T00:00:00Z", "2026-01-01T00:00:10Z")
	mk("b", false, "read", "2026-01-01T00:00:00Z", "2026-01-01T00:00:30Z")
	mk("c", false, "unread", "2026-01-01T00:00:00Z", nil)
	mk("d", true, "unread", "2026-01-01T00:00:00Z", nil)

	s, err := store.InboxStatsFor(db, "user")
	if err != nil {
		t.Fatal(err)
	}
	if s.Answered != 2 || s.Pending != 1 || s.Broadcasts != 1 ||
		s.Average != 20*time.Second || s.Worst != 30*time.Second || s.Median != 30*time.Second {
		t.Errorf("stats = %+v", s)
	}
	out := captureStdout(t, func() {
		if rc := cmdInbox([]string{"stats"}); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	for _, want := range []string{"answered messages : 2", "pending messages  : 1", "broadcasts on bus : 1", "average wait      : 20s", "worst wait        : 30s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHookStopNudgesUnsurfacedBroadcastOnceThenBacksOff(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "worker", "Worker")
	insertTaskForHookTest(t, db, "src", "Src")
	t.Setenv("FOCUSFLOW_TASK", "worker")
	if err := store.InsertBusMessage(db, &store.BusMessage{
		ID: "bc1", FromAssignee: "user", ToAssignee: "user", ToTaskSlug: sql.NullString{String: "worker", Valid: true},
		FromTaskSlug: sql.NullString{String: "src", Valid: true}, Body: "src is done", Broadcast: true, CreatedAt: store.NowISO(),
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdoutWithStdin(t, "", func() { cmdHookStop(nil) })
	var parsed struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(out), &parsed); err != nil || parsed.Decision != "block" ||
		!strings.Contains(parsed.Reason, "bc1") || !strings.Contains(parsed.Reason, "src is done") {
		t.Fatalf("first stop = %q (%v)", out, err)
	}
	// Surfaced now: no repeat, even though the message is still unread.
	out = captureStdoutWithStdin(t, "", func() { cmdHookStop(nil) })
	if strings.TrimSpace(out) != "" {
		t.Errorf("second stop should be silent, got %q", out)
	}
	st, _ := store.GetNudgeState(db, "worker")
	if st.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", st.Attempts)
	}
}

func TestNudgeBackoffGrows(t *testing.T) {
	if !(nudgeBackoffFor(0) == 0 && nudgeBackoffFor(1) < nudgeBackoffFor(2) && nudgeBackoffFor(2) < nudgeBackoffFor(3)) {
		t.Error("backoff should grow with attempts")
	}
}
