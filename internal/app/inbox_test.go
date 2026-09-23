package app

import (
	"strings"
	"testing"
	"time"

	"focusflow/internal/flowdb"
)

func TestCmdInboxPop_ConsumesOldestFirst(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)

	for _, body := range []string{"first", "second", "third"} {
		if rc := cmdMessage([]string{"user/worker", body}); rc != 0 {
			t.Fatalf("cmdMessage(%q) rc=%d", body, rc)
		}
	}

	bindSession(t, "worker", "44444444-4444-4444-8444-444444444444")

	for _, want := range []string{"first", "second", "third"} {
		out := captureStdout(t, func() {
			if rc := cmdInboxPop(nil); rc != 0 {
				t.Fatalf("cmdInboxPop rc=%d", rc)
			}
		})
		if !strings.Contains(out, want) {
			t.Errorf("pop output = %q, want to contain %q", out, want)
		}
	}

	// Mailbox now empty; a non-waiting pop returns cleanly with no message.
	out := captureStdout(t, func() {
		if rc := cmdInboxPop(nil); rc != 0 {
			t.Fatalf("cmdInboxPop rc=%d", rc)
		}
	})
	if !strings.Contains(out, "no messages") {
		t.Errorf("expected 'no messages', got %q", out)
	}
}

func TestCmdInboxPop_MarksMessageRead(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)
	if rc := cmdMessage([]string{"user/worker", "hello"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}

	bindSession(t, "worker", "55555555-5555-4555-8555-555555555555")
	if rc := cmdInboxPop([]string{"--json"}); rc != 0 {
		t.Fatalf("cmdInboxPop rc=%d", rc)
	}

	db := openFlowDB(t)
	msgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{TaskSlug: "worker"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Mail != "read" {
		t.Errorf("Mail = %q, want read", msgs[0].Mail)
	}
	unreadOnly, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{TaskSlug: "worker"}, false)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(unreadOnly) != 0 {
		t.Errorf("got %d unread messages, want 0", len(unreadOnly))
	}
}

func TestCmdInboxPop_KeepUnreadPeeksWithoutConsuming(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)
	if rc := cmdMessage([]string{"user/worker", "peek me"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}

	bindSession(t, "worker", "66666666-6666-4666-8666-666666666666")
	if rc := cmdInboxPop([]string{"--keep-unread"}); rc != 0 {
		t.Fatalf("cmdInboxPop rc=%d", rc)
	}
	// Popping again with --keep-unread should see the SAME message, still unread.
	out := captureStdout(t, func() {
		if rc := cmdInboxPop([]string{"--keep-unread", "--json"}); rc != 0 {
			t.Fatalf("cmdInboxPop rc=%d", rc)
		}
	})
	if !strings.Contains(out, "peek me") {
		t.Errorf("expected message still present, got %q", out)
	}

	db := openFlowDB(t)
	unread, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{TaskSlug: "worker"}, false)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("got %d unread, want 1 (peek must not consume)", len(unread))
	}
}

func TestCmdInboxPop_WaitTimesOutSilentlyWithJSON(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	bindSession(t, "worker", "77777777-7777-4777-8777-777777777777")

	old := inboxPollInterval
	inboxPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { inboxPollInterval = old })

	start := time.Now()
	out := captureStdout(t, func() {
		if rc := cmdInboxPop([]string{"--wait", "--timeout", "1", "--json"}); rc != 0 {
			t.Fatalf("cmdInboxPop rc=%d", rc)
		}
	})
	elapsed := time.Since(start)
	if out != "" {
		t.Errorf("expected silent timeout with --json, got %q", out)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("returned after %v, expected to actually wait ~1s", elapsed)
	}
}

func TestCmdInboxPop_WaitReturnsAsSoonAsMessageArrives(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	bindSession(t, "worker", "88888888-8888-4888-8888-888888888888")

	old := inboxPollInterval
	inboxPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { inboxPollInterval = old })

	done := make(chan string, 1)
	go func() {
		out := captureStdout(t, func() {
			cmdInboxPop([]string{"--wait", "--timeout", "5", "--json"})
		})
		done <- out
	}()

	time.Sleep(50 * time.Millisecond)
	unbindSession(t)
	if rc := cmdMessage([]string{"user/worker", "arrived"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}

	select {
	case out := <-done:
		if !strings.Contains(out, "arrived") {
			t.Errorf("got %q, want to contain 'arrived'", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cmdInboxPop --wait did not return after message arrived")
	}
}

func TestCmdInboxRead_MarksByID(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	bindSession(t, "worker", "99999999-9999-4999-8999-999999999999")
	if rc := cmdMessage([]string{"user", "reply text"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}
	unbindSession(t)

	out := captureStdout(t, func() {
		if rc := cmdInboxList([]string{"--as", "user", "--json"}); rc != 0 {
			t.Fatalf("cmdInboxList rc=%d", rc)
		}
	})
	id := extractJSONID(t, out)

	if rc := cmdInboxRead([]string{id}); rc != 0 {
		t.Fatalf("cmdInboxRead rc=%d", rc)
	}

	db := openFlowDB(t)
	m, err := flowdb.GetBusMessage(db, id)
	if err != nil {
		t.Fatalf("GetBusMessage: %v", err)
	}
	if m.Mail != "read" {
		t.Errorf("Mail = %q, want read", m.Mail)
	}
}

func TestCmdInboxRead_UnknownIDErrors(t *testing.T) {
	setupFlowRoot(t)
	if rc := cmdInboxRead([]string{"deadbeef"}); rc == 0 {
		t.Error("expected error for unknown id, got rc=0")
	}
}

func TestCmdInbox_DispatchesSubcommands(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)
	if rc := cmdMessage([]string{"user/worker", "hi"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}
	bindSession(t, "worker", "10101010-1010-4101-8101-101010101010")

	// Bare `inbox` == list.
	out := captureStdout(t, func() {
		if rc := cmdInbox(nil); rc != 0 {
			t.Fatalf("cmdInbox rc=%d", rc)
		}
	})
	if !strings.Contains(out, "hi") {
		t.Errorf("bare inbox should list, got %q", out)
	}

	// `inbox pop` consumes it.
	if rc := cmdInbox([]string{"pop"}); rc != 0 {
		t.Fatalf("cmdInbox pop rc=%d", rc)
	}

	// stats is explicitly out of scope.
	if rc := cmdInbox([]string{"stats"}); rc == 0 {
		t.Error("expected inbox stats to be rejected as out of scope")
	}

	// unknown subcommand errors instead of silently listing.
	if rc := cmdInbox([]string{"bogus"}); rc == 0 {
		t.Error("expected error for unknown inbox subcommand")
	}
}

// extractJSONID pulls the first "id" field out of a JSON array like
// `focusflow inbox --json` prints.
func extractJSONID(t *testing.T, out string) string {
	t.Helper()
	idx := strings.Index(out, `"id":"`)
	if idx == -1 {
		t.Fatalf("no id field found in %q", out)
	}
	rest := out[idx+len(`"id":"`):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		t.Fatalf("malformed id field in %q", out)
	}
	return rest[:end]
}
