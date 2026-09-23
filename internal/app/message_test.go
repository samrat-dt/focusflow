package app

import (
	"encoding/json"
	"strings"
	"testing"

	"focusflow/internal/flowdb"
)

// bindSession sets CLAUDE_CODE_SESSION_ID and binds it to slug via
// `do --here`, simulating an execution session running inside that task.
func bindSession(t *testing.T, slug, sessionID string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", sessionID)
	if rc := cmdDo([]string{slug, "--here"}); rc != 0 {
		t.Fatalf("do --here rc=%d", rc)
	}
}

// unbindSession simulates a dispatch/control-center context: no harness
// session id in the environment at all.
func unbindSession(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
}

func TestParseMessageAddress(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantAssign string
		wantSlug   string
		wantErr    bool
	}{
		{"bare user", "user", "user", "", false},
		{"user slash task", "user/worker", "user", "worker", false},
		{"empty", "", "", "", true},
		{"empty assignee", "/worker", "", "", true},
		{"empty task slug", "user/", "", "", true},
		{"double slash", "user/worker/extra", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMessageAddress(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Assignee != tc.wantAssign || got.TaskSlug != tc.wantSlug {
				t.Errorf("got %+v, want assignee=%q slug=%q", got, tc.wantAssign, tc.wantSlug)
			}
		})
	}
}

func TestParseMessageArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantTarget string
		wantBody   string
		wantUrgent bool
		wantReply  string
		wantErr    bool
	}{
		{"positional body", []string{"user/worker", "hello there"}, "user/worker", "hello there", false, "", false},
		{"body flag", []string{"user/worker", "--body", "hello there"}, "user/worker", "hello there", false, "", false},
		{"flags any order before", []string{"--urgent", "--reply-to", "abc12345", "user/worker", "hi"}, "user/worker", "hi", true, "abc12345", false},
		{"flags any order after", []string{"user/worker", "hi", "--urgent"}, "user/worker", "hi", true, "", false},
		{"equals form", []string{"user/worker", "--body=hi there", "--reply-to=abc12345"}, "user/worker", "hi there", false, "abc12345", false},
		{"missing target", []string{}, "", "", false, "", true},
		{"help-like leading flag is not swallowed as target", []string{"--help"}, "", "", false, "", true},
		{"body both positional and flag", []string{"user/worker", "hi", "--body", "there"}, "", "", false, "", true},
		{"empty body", []string{"user/worker"}, "", "", false, "", true},
		{"body starting with dash rejected as unknown flag", []string{"user/worker", "-1 not a flag"}, "", "", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMessageArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Target != tc.wantTarget || got.Body != tc.wantBody || got.Urgent != tc.wantUrgent || got.ReplyTo != tc.wantReply {
				t.Errorf("got %+v, want target=%q body=%q urgent=%v replyTo=%q", got, tc.wantTarget, tc.wantBody, tc.wantUrgent, tc.wantReply)
			}
		})
	}
}

// TestCmdMessage_DispatchToTaskMailbox verifies the exact call
// control-center makes: `message user/<slug> "<text>"` from an unbound
// (dispatch) context lands in that task's own mailbox, not the human's.
func TestCmdMessage_DispatchToTaskMailbox(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)

	out := captureStdout(t, func() {
		if rc := cmdMessage([]string{"user/worker", "please look at this"}); rc != 0 {
			t.Fatalf("cmdMessage rc=%d", rc)
		}
	})
	if !strings.Contains(out, "worker") {
		t.Errorf("expected output to mention target, got %q", out)
	}

	db := openFlowDB(t)
	msgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{TaskSlug: "worker"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages in worker's mailbox, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Body != "please look at this" {
		t.Errorf("Body = %q", m.Body)
	}
	if m.Mail != "unread" {
		t.Errorf("Mail = %q, want unread", m.Mail)
	}
	if m.FromTaskSlug.Valid {
		t.Errorf("FromTaskSlug = %+v, want NULL (sender was unbound)", m.FromTaskSlug)
	}
	if !m.ToTaskSlug.Valid || m.ToTaskSlug.String != "worker" {
		t.Errorf("ToTaskSlug = %+v, want worker", m.ToTaskSlug)
	}

	// The human's own mailbox must be untouched by this send.
	humanMsgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{Assignee: "user"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages(user): %v", err)
	}
	if len(humanMsgs) != 0 {
		t.Errorf("got %d messages in human mailbox, want 0", len(humanMsgs))
	}
}

// TestCmdMessage_TaskRepliesToUser verifies a bound session's `message
// user "<reply>"` lands in the human's aggregate mailbox tagged with its
// own task slug, matching how control-center's poller attributes replies.
func TestCmdMessage_TaskRepliesToUser(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	bindSession(t, "worker", "11111111-1111-4111-8111-111111111111")

	if rc := cmdMessage([]string{"user", "here's the answer"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}

	unbindSession(t)
	db := openFlowDB(t)
	msgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{Assignee: "user"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if !m.FromTaskSlug.Valid || m.FromTaskSlug.String != "worker" {
		t.Errorf("FromTaskSlug = %+v, want worker", m.FromTaskSlug)
	}
	if m.ToTaskSlug.Valid {
		t.Errorf("ToTaskSlug = %+v, want NULL (human mailbox)", m.ToTaskSlug)
	}
}

func TestCmdMessage_RejectsSelfAddress(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")

	t.Run("bound task messaging its own mailbox", func(t *testing.T) {
		bindSession(t, "worker", "22222222-2222-4222-8222-222222222222")
		if rc := cmdMessage([]string{"user/worker", "hi"}); rc == 0 {
			t.Error("expected self-address rejection, got rc=0")
		}
	})

	t.Run("dispatch messaging the human while itself unbound", func(t *testing.T) {
		unbindSession(t)
		if rc := cmdMessage([]string{"user", "hi"}); rc == 0 {
			t.Error("expected self-address rejection, got rc=0")
		}
	})
}

func TestCmdMessage_UnknownTaskErrors(t *testing.T) {
	setupFlowRoot(t)
	unbindSession(t)
	if rc := cmdMessage([]string{"user/does-not-exist", "hi"}); rc == 0 {
		t.Error("expected error for unknown task slug, got rc=0")
	}
}

func TestCmdMessage_UnknownReplyToErrors(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)
	if rc := cmdMessage([]string{"user/worker", "hi", "--reply-to", "deadbeef"}); rc == 0 {
		t.Error("expected error for unknown --reply-to id, got rc=0")
	}
}

func TestCmdMessage_ReplyToChainsCorrectly(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	unbindSession(t)

	out := captureStdout(t, func() {
		if rc := cmdMessage([]string{"user/worker", "first"}); rc != 0 {
			t.Fatalf("cmdMessage rc=%d", rc)
		}
	})
	id := extractBracketedID(t, out)

	if rc := cmdMessage([]string{"user/worker", "second", "--reply-to", id}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}

	db := openFlowDB(t)
	msgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{TaskSlug: "worker"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	second := msgs[1]
	if !second.ReplyTo.Valid || second.ReplyTo.String != id {
		t.Errorf("ReplyTo = %+v, want %q", second.ReplyTo, id)
	}
}

func TestCmdBroadcast_LandsInHumanMailbox(t *testing.T) {
	setupFlowRoot(t)
	unbindSession(t)

	if rc := cmdBroadcast([]string{"heads up, deploying soon"}); rc != 0 {
		t.Fatalf("cmdBroadcast rc=%d", rc)
	}

	db := openFlowDB(t)
	msgs, err := flowdb.ListBusMessages(db, flowdb.MailboxFilter{Assignee: "user"}, true)
	if err != nil {
		t.Fatalf("ListBusMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if !msgs[0].Broadcast {
		t.Error("Broadcast = false, want true")
	}
}

func TestCmdBroadcast_RequiresNonEmptyBody(t *testing.T) {
	setupFlowRoot(t)
	unbindSession(t)
	if rc := cmdBroadcast([]string{}); rc == 0 {
		t.Error("expected error for missing body, got rc=0")
	}
	if rc := cmdBroadcast([]string{"   "}); rc == 0 {
		t.Error("expected error for whitespace-only body, got rc=0")
	}
}

// extractBracketedID pulls the "[xxxxxxxx]" id out of `focusflow message`'s
// stdout, mirroring control-center's parseFlowId regex.
func extractBracketedID(t *testing.T, out string) string {
	t.Helper()
	start := strings.Index(out, "[")
	end := strings.Index(out, "]")
	if start == -1 || end == -1 || end < start {
		t.Fatalf("no bracketed id found in output %q", out)
	}
	return out[start+1 : end]
}

// TestControlCenterInboxJSONShape locks in the exact JSON shape
// control-center's lib/inbox.js parseRow expects: id, body, created_at,
// from.task_slug (nullable), reply_to (nullable), urgent, mail.
func TestControlCenterInboxJSONShape(t *testing.T) {
	setupFlowRoot(t)
	seedTask(t, "worker")
	bindSession(t, "worker", "33333333-3333-4333-8333-333333333333")
	if rc := cmdMessage([]string{"user", "the reply", "--urgent"}); rc != 0 {
		t.Fatalf("cmdMessage rc=%d", rc)
	}
	unbindSession(t)

	out := captureStdout(t, func() {
		if rc := cmdInboxList([]string{"--all", "--as", "user", "--json"}); rc != 0 {
			t.Fatalf("cmdInboxList rc=%d", rc)
		}
	})

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", out, err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]
	for _, key := range []string{"id", "body", "created_at", "from", "reply_to", "urgent", "mail"} {
		if _, ok := row[key]; !ok {
			t.Errorf("row missing key %q: %v", key, row)
		}
	}
	from, ok := row["from"].(map[string]any)
	if !ok {
		t.Fatalf("row[from] = %v, want object", row["from"])
	}
	if from["task_slug"] != "worker" {
		t.Errorf("from.task_slug = %v, want worker", from["task_slug"])
	}
	if row["urgent"] != true {
		t.Errorf("urgent = %v, want true", row["urgent"])
	}
	if row["mail"] != "unread" {
		t.Errorf("mail = %v, want unread", row["mail"])
	}
}

func TestResolveCurrentTaskSlug_FallsBackToEnvVar(t *testing.T) {
	setupFlowRoot(t)
	unbindSession(t)
	t.Setenv("FOCUSFLOW_TASK", "whatever")
	db := openFlowDB(t)
	if got := resolveCurrentTaskSlug(db); got != "whatever" {
		t.Errorf("resolveCurrentTaskSlug() = %q, want %q", got, "whatever")
	}
}
