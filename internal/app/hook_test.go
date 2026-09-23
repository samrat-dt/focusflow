package app

import (
	"database/sql"
	"encoding/json"
	"focusflow/internal/flowdb"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateHookEnv points every path cmdHookSessionStart's new mirror side
// effect (mirrorFromFlow) could touch at a throwaway temp tree, and clears
// the harness session-id env vars it reads. Without this, running these
// tests under an actual Claude Code / Codex session (as happens during
// interactive development) would have mirrorFromFlow read the real
// ~/.flow/flow.db and write into the real ~/.focusflow/focusflow.db —
// exactly the isolation CLAUDE.md requires ("nothing touches real
// ~/.focusflow/ or ~/.claude/").
func isolateHookEnv(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("FOCUSFLOW_ROOT", filepath.Join(tmp, ".focusflow"))
	t.Setenv("FLOW_ROOT", filepath.Join(tmp, ".flow-does-not-exist"))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
}

// TestHookSessionStartNoFlowTaskEmitsAmbientHint pins the contract for
// ad-hoc sessions (e.g. bare `claude` with no FOCUSFLOW_TASK): the hook must
// emit a one-liner pointing at §4.14 of the focusflow skill. The skill —
// not the hook — owns the substantive-work detection logic and offers
// the three-choice prompt; the hook is only the one-shot trigger.
func TestHookSessionStartNoFlowTaskEmitsAmbientHint(t *testing.T) {
	isolateHookEnv(t)
	t.Setenv("FOCUSFLOW_TASK", "")
	out := captureStdout(t, func() {
		if rc := cmdHookSessionStart(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	var parsed struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out)
	}
	if parsed.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", parsed.HookSpecificOutput.HookEventName)
	}
	ctx := parsed.HookSpecificOutput.AdditionalContext
	// Must point at §4.14 of the focusflow skill (skill owns the logic).
	if !strings.Contains(ctx, "4.14") {
		t.Errorf("ambient hint must reference §4.14, got:\n%s", ctx)
	}
	// Must communicate that the check is ongoing, not one-shot.
	if !strings.Contains(ctx, "ongoing, not one-shot") {
		t.Errorf("ambient hint must say %q, got:\n%s", "ongoing, not one-shot", ctx)
	}
	// Must NOT include task-specific instructions (register-session,
	// reading the brief) since there is no task bound to this session.
	if strings.Contains(ctx, "focusflow register-session") {
		t.Errorf("ambient hint should not instruct register-session (no FOCUSFLOW_TASK bound):\n%s", ctx)
	}
}

// TestHookSessionStartRequiresSkillInvocation pins the invariant that
// the injected additionalContext explicitly instructs the session to
// invoke the focusflow skill via the Skill tool as its first action, and
// mentions the task slug so the agent has something anchor-visible.
func TestHookSessionStartRequiresSkillInvocation(t *testing.T) {
	isolateHookEnv(t)
	t.Setenv("FOCUSFLOW_TASK", "some-slug")
	out := captureStdout(t, func() {
		if rc := cmdHookSessionStart(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})

	var parsed struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out)
	}
	if parsed.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q, want SessionStart", parsed.HookSpecificOutput.HookEventName)
	}
	ctx := parsed.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "Skill tool") {
		t.Errorf("additionalContext must instruct Skill tool invocation, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "`focusflow` skill") {
		t.Errorf("additionalContext must name the `focusflow` skill, got:\n%s", ctx)
	}
	// Self-registration is gone — the UUID is pre-allocated by `focusflow do`.
	// Make sure we don't regress by re-introducing it here.
	if strings.Contains(ctx, "register-session") {
		t.Errorf("additionalContext should not mention register-session (pre-allocated by focusflow do):\n%s", ctx)
	}
	if !strings.Contains(ctx, "some-slug") {
		t.Errorf("additionalContext should mention the task slug, got:\n%s", ctx)
	}
}

// TestHookSessionStartArmsMessageBusListener pins the message-bus
// listener discipline for a bound session: since the Stop hook
// (cmdHookStop) is only a backstop, the bootstrap instructions must
// tell the agent to arm a persistent inbox-watching loop so replies are
// noticed promptly rather than relying on the backstop alone.
func TestHookSessionStartArmsMessageBusListener(t *testing.T) {
	isolateHookEnv(t)
	t.Setenv("FOCUSFLOW_TASK", "some-slug")
	out := captureStdout(t, func() {
		if rc := cmdHookSessionStart(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	var parsed struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out)
	}
	ctx := parsed.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"focusflow inbox pop", "Monitor", "persistent"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("additionalContext missing %q (message-bus listener discipline), got:\n%s", want, ctx)
		}
	}
}

// captureStdoutWithStdin is captureStdout plus feeding stdin content —
// used for cmdHookStop, which reads Claude Code's Stop-hook JSON input
// (notably stop_hook_active) from stdin.
func captureStdoutWithStdin(t *testing.T, stdin string, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(stdin); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
	oldIn := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldIn })
	return captureStdout(t, f)
}

// hookTestDB extends isolateHookEnv with an actually-initialized
// focusflow.db, for tests that need to seed task/message rows before
// invoking a hook command.
func hookTestDB(t *testing.T) *sql.DB {
	t.Helper()
	isolateHookEnv(t)
	root, err := flowRoot()
	if err != nil {
		t.Fatalf("flowRoot: %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	db, err := flowdb.OpenDB(filepath.Join(root, "focusflow.db"))
	if err != nil {
		t.Fatalf("open focusflow.db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestHookUserPromptSubmitUnboundSessionIsSilent pins the no-op case: a
// session with no bound task (neither a session_id match nor
// $FOCUSFLOW_TASK set) must produce no output at all — the ambient
// §4.14 nudge is SessionStart's job, not this hook's.
func TestHookUserPromptSubmitUnboundSessionIsSilent(t *testing.T) {
	hookTestDB(t)
	out := captureStdout(t, func() {
		if rc := cmdHookUserPromptSubmit(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output for an unbound session, got:\n%s", out)
	}
}

// TestHookUserPromptSubmitBoundSessionEmitsAnchor pins the primary
// contract, matching real flow's observed UserPromptSubmit hook shape:
// a one-line anchor naming the task and citing §4.11/§4.7 so scope-creep
// detection and close-out offers stay live every turn.
func TestHookUserPromptSubmitBoundSessionEmitsAnchor(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "anchor-task", "Anchor Task")
	t.Setenv("FOCUSFLOW_TASK", "anchor-task")

	out := captureStdout(t, func() {
		if rc := cmdHookUserPromptSubmit(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})

	var parsed struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out)
	}
	if parsed.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q, want UserPromptSubmit", parsed.HookSpecificOutput.HookEventName)
	}
	ctx := parsed.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"Anchor Task", "anchor-task", "4.11", "4.7"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("additionalContext missing %q, got:\n%s", want, ctx)
		}
	}
}

// insertTaskForHookTest creates a minimal native focusflow task so
// resolveCurrentTaskSlug / flowdb.GetTask can resolve it by slug.
func insertTaskForHookTest(t *testing.T, db *sql.DB, slug, name string) {
	t.Helper()
	now := flowdb.NowISO()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, priority, work_dir, source, created_at, updated_at) VALUES (?, ?, 'in-progress', 'medium', '/tmp/x', 'native', ?, ?)`,
		slug, name, now, now,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

// TestHookStopUnboundSessionIsSilent mirrors the UserPromptSubmit no-op
// case for the Stop hook.
func TestHookStopUnboundSessionIsSilent(t *testing.T) {
	hookTestDB(t)
	out := captureStdoutWithStdin(t, "", func() {
		if rc := cmdHookStop(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output for an unbound session, got:\n%s", out)
	}
}

// TestHookStopNoOutstandingMessageIsSilent pins the common case: a bound
// task that never sent `message user` has nothing to nudge about.
func TestHookStopNoOutstandingMessageIsSilent(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "quiet-task", "Quiet Task")
	t.Setenv("FOCUSFLOW_TASK", "quiet-task")

	out := captureStdoutWithStdin(t, "", func() {
		if rc := cmdHookStop(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output when there's no outstanding message, got:\n%s", out)
	}
}

// TestHookStopUnreadReplyBlocksWithNudge pins the primary contract,
// reconstructed from real flow's observed strings ("flow-bus: the user
// has just returned... your message [id] (...) was answered after
// ..."): when a reply has arrived to a question this task asked the
// user and that reply is still unread, the Stop hook blocks ending the
// turn and surfaces a nudge naming the message id and elapsed wait.
func TestHookStopUnreadReplyBlocksWithNudge(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "waiting-task", "Waiting Task")
	t.Setenv("FOCUSFLOW_TASK", "waiting-task")

	asked := &flowdb.BusMessage{
		ID: "msg-asked", FromAssignee: "user", ToAssignee: "user",
		FromTaskSlug: sql.NullString{String: "waiting-task", Valid: true},
		Body:         "should I proceed?", Mail: "unread", CreatedAt: "2026-01-01T00:00:00Z",
	}
	answered := &flowdb.BusMessage{
		ID: "msg-answered", FromAssignee: "user", ToAssignee: "waiting-task",
		ToTaskSlug: sql.NullString{String: "waiting-task", Valid: true},
		Body:       "yes go ahead", Mail: "unread", CreatedAt: "2026-01-01T00:03:00Z",
		ReplyTo: sql.NullString{String: "msg-asked", Valid: true},
	}
	for _, m := range []*flowdb.BusMessage{asked, answered} {
		if _, err := flowdb.InsertBusMessageIfAbsent(db, m); err != nil {
			t.Fatalf("seed message %s: %v", m.ID, err)
		}
	}

	out := captureStdoutWithStdin(t, "", func() {
		if rc := cmdHookStop(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})

	var parsed struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse hook output: %v\nraw: %s", err, out)
	}
	if parsed.Decision != "block" {
		t.Errorf("decision = %q, want block", parsed.Decision)
	}
	for _, want := range []string{"msg-asked", "3m"} {
		if !strings.Contains(parsed.Reason, want) {
			t.Errorf("reason missing %q, got:\n%s", want, parsed.Reason)
		}
	}
}

// TestHookStopAlreadyReadReplyIsSilent pins the "nudge until consumed,
// then stop" guarantee: once the reply is marked read (e.g. the
// assistant already popped it via `focusflow inbox pop`), the Stop hook
// must not nudge again.
func TestHookStopAlreadyReadReplyIsSilent(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "resolved-task", "Resolved Task")
	t.Setenv("FOCUSFLOW_TASK", "resolved-task")

	asked := &flowdb.BusMessage{
		ID: "msg-asked2", FromAssignee: "user", ToAssignee: "user",
		FromTaskSlug: sql.NullString{String: "resolved-task", Valid: true},
		Body:         "ok?", Mail: "read", CreatedAt: "2026-01-01T00:00:00Z",
	}
	answered := &flowdb.BusMessage{
		ID: "msg-answered2", FromAssignee: "user", ToAssignee: "resolved-task",
		ToTaskSlug: sql.NullString{String: "resolved-task", Valid: true},
		Body:       "yep", Mail: "read", CreatedAt: "2026-01-01T00:01:00Z",
		ReplyTo: sql.NullString{String: "msg-asked2", Valid: true},
	}
	for _, m := range []*flowdb.BusMessage{asked, answered} {
		if _, err := flowdb.InsertBusMessageIfAbsent(db, m); err != nil {
			t.Fatalf("seed message %s: %v", m.ID, err)
		}
	}

	out := captureStdoutWithStdin(t, "", func() {
		if rc := cmdHookStop(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output once the reply is already read, got:\n%s", out)
	}
}

// TestHookStopRespectsStopHookActive pins the loop guard: Claude Code
// sets stop_hook_active=true on stdin when this is already a forced
// continuation from a prior block, so blocking again would loop forever.
func TestHookStopRespectsStopHookActive(t *testing.T) {
	db := hookTestDB(t)
	insertTaskForHookTest(t, db, "loop-task", "Loop Task")
	t.Setenv("FOCUSFLOW_TASK", "loop-task")

	asked := &flowdb.BusMessage{
		ID: "msg-asked3", FromAssignee: "user", ToAssignee: "user",
		FromTaskSlug: sql.NullString{String: "loop-task", Valid: true},
		Body:         "ok?", Mail: "unread", CreatedAt: "2026-01-01T00:00:00Z",
	}
	answered := &flowdb.BusMessage{
		ID: "msg-answered3", FromAssignee: "user", ToAssignee: "loop-task",
		ToTaskSlug: sql.NullString{String: "loop-task", Valid: true},
		Body:       "yep", Mail: "unread", CreatedAt: "2026-01-01T00:01:00Z",
		ReplyTo: sql.NullString{String: "msg-asked3", Valid: true},
	}
	for _, m := range []*flowdb.BusMessage{asked, answered} {
		if _, err := flowdb.InsertBusMessageIfAbsent(db, m); err != nil {
			t.Fatalf("seed message %s: %v", m.ID, err)
		}
	}

	out := captureStdoutWithStdin(t, `{"stop_hook_active": true}`, func() {
		if rc := cmdHookStop(nil); rc != 0 {
			t.Fatalf("rc=%d", rc)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no output when stop_hook_active=true, got:\n%s", out)
	}
}

// TestBuildBootstrapPromptInvokesSkill pins the same invariant for the
// fresh-spawn prompt used by `focusflow do` (the hook only covers resume).
func TestBuildBootstrapPromptInvokesSkill(t *testing.T) {
	prompt := buildBootstrapPrompt("task-x")
	if !strings.Contains(prompt, "focusflow skill") && !strings.Contains(prompt, "`focusflow` skill") {
		t.Errorf("bootstrap prompt must name the focusflow skill:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Skill tool") {
		t.Errorf("bootstrap prompt must instruct Skill tool invocation:\n%s", prompt)
	}
	if strings.Contains(prompt, "register-session") {
		t.Errorf("bootstrap prompt should not mention register-session (pre-allocated by focusflow do):\n%s", prompt)
	}
	if !strings.Contains(prompt, "task-x") {
		t.Errorf("bootstrap prompt must mention the task slug")
	}
}
