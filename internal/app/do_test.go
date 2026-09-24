package app

import (
	"focusflow/internal/iterm"
	"focusflow/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// stubITerm replaces iterm.Runner with a counter + captured-script
// recorder. Returns the counter pointer and a function that reads the
// most recent AppleScript argument passed to osascript.
func stubITerm(t *testing.T) (*int64, func() string) {
	t.Helper()
	var count int64
	var mu sync.Mutex
	var lastScript string
	restore := iterm.Stub(func(args []string) (string, error) {
		atomic.AddInt64(&count, 1)
		mu.Lock()
		if len(args) >= 2 {
			lastScript = args[1]
		}
		mu.Unlock()
		return "stub-tab-ref", nil
	})
	t.Cleanup(restore)
	return &count, func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastScript
	}
}

// seedTask creates a minimal task row (floating, workspace work_dir).
func seedTask(t *testing.T, slug string) {
	t.Helper()
	if rc := cmdAdd([]string{"task", slug}); rc != 0 {
		t.Fatalf("seed task rc=%d", rc)
	}
}

// TestCmdDoFreshAllocatesSessionID verifies the pre-allocation contract:
// a fresh task gets a UUID written to tasks.session_id and spawns
// `claude --session-id <uuid> "<prompt>"` so the jsonl file claude creates
// lands at the deterministic path keyed on that UUID.
func TestDo_Here_BindsCurrentSessionWithoutSpawningTab(t *testing.T) {
	setupRoot(t)
	seedTask(t, "bindable")
	spawnCount, _ := stubITerm(t)

	t.Setenv("CLAUDE_CODE_SESSION_ID", "44444444-4444-4444-8444-444444444444")
	rc := cmdDo([]string{"bindable", "--here"})
	if rc != 0 {
		t.Fatalf("do --here rc=%d", rc)
	}
	if *spawnCount != 0 {
		t.Errorf("do --here must not spawn an iTerm tab, got %d spawns", *spawnCount)
	}

	db := openTestDB(t)
	task, err := store.GetTask(db, "bindable")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !task.SessionID.Valid || task.SessionID.String != "44444444-4444-4444-8444-444444444444" {
		t.Errorf("SessionID = %+v, want the CLAUDE_CODE_SESSION_ID value", task.SessionID)
	}
	if task.Status != "in-progress" {
		t.Errorf("Status = %q, want in-progress", task.Status)
	}
}

func TestDo_Here_RejectsAlreadyBoundTask(t *testing.T) {
	setupRoot(t)
	seedTask(t, "already-bound")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "55555555-5555-4555-8555-555555555555")
	if rc := cmdDo([]string{"already-bound", "--here"}); rc != 0 {
		t.Fatalf("first do --here rc=%d", rc)
	}

	seedTask(t, "second-task")
	rc := cmdDo([]string{"second-task", "--here"})
	if rc == 0 {
		t.Error("do --here on a second task from the same process should fail (session_id already bound elsewhere)")
	}
}

func TestDo_Auto_SpawnsDetachedProcessAndRecordsPID(t *testing.T) {
	setupRoot(t)
	seedTask(t, "auto-task")

	origExecCommand := execCommand
	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string{name}, args...)
		// Use a real, near-instant command so Start()/Wait() behave like a
		// real (if trivial) process without depending on `claude` existing
		// in the test environment.
		return exec.Command("true")
	}
	defer func() { execCommand = origExecCommand }()

	rc := cmdDo([]string{"auto-task", "--auto"})
	if rc != 0 {
		t.Fatalf("do --auto rc=%d", rc)
	}
	if len(capturedArgs) < 4 || capturedArgs[0] != "/bin/sh" || capturedArgs[3] != "claude" {
		t.Errorf("expected claude run through the exit-recording wrapper, got %v", capturedArgs)
	}
	found := false
	for _, a := range capturedArgs {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	if !found {
		t.Error("--auto must imply --dangerously-skip-permissions")
	}

	db := openTestDB(t)
	task, err := store.GetTask(db, "auto-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !task.AutoRunStatus.Valid || task.AutoRunStatus.String != "running" {
		t.Errorf("AutoRunStatus = %+v, want running", task.AutoRunStatus)
	}
	if !task.AutoRunPID.Valid || task.AutoRunPID.Int64 == 0 {
		t.Errorf("AutoRunPID = %+v, want a nonzero pid", task.AutoRunPID)
	}
}

func TestCmdDoRejectsLeadingDashAsRef(t *testing.T) {
	setupRoot(t)
	rc := cmdDo([]string{"--help"})
	if rc != 2 {
		t.Errorf("do --help: rc=%d, want 2 (usage error)", rc)
	}
}

func TestDo_Auto_PromptTellsSessionToSelfComplete(t *testing.T) {
	setupRoot(t)
	seedTask(t, "auto-self-complete")

	origExecCommand := execCommand
	var args []string
	execCommand = func(name string, a ...string) *exec.Cmd {
		args = append([]string{name}, a...)
		return exec.Command("true")
	}
	defer func() { execCommand = origExecCommand }()

	if rc := cmdDo([]string{"auto-self-complete", "--auto"}); rc != 0 {
		t.Fatalf("do --auto rc=%d", rc)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "focusflow done") {
		t.Errorf("headless prompt must instruct the session it is unattended and must call `focusflow done` itself when finished; got args: %v", args)
	}
}

func TestDo_Auto_UsesPrintModeSoItRunsWithoutATTY(t *testing.T) {
	setupRoot(t)
	seedTask(t, "auto-fresh")

	origExecCommand := execCommand
	var freshArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		freshArgs = append([]string{name}, args...)
		return exec.Command("true")
	}
	defer func() { execCommand = origExecCommand }()

	if rc := cmdDo([]string{"auto-fresh", "--auto"}); rc != 0 {
		t.Fatalf("do --auto (fresh) rc=%d", rc)
	}
	if !containsArg(freshArgs, "-p") && !containsArg(freshArgs, "--print") {
		t.Errorf("fresh headless spawn must pass -p/--print (without it claude starts its interactive TUI, which fails immediately with no TTY): %v", freshArgs)
	}

	// Resume path: give the task an existing session_id directly (as if
	// a prior `do` had already bootstrapped it), so this --auto call
	// takes the resume branch, not the fresh-bootstrap branch.
	seedTask(t, "auto-resume")
	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE tasks SET session_id = 'existing-session-id' WHERE slug = 'auto-resume'`); err != nil {
		t.Fatal(err)
	}
	var resumeArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		resumeArgs = append([]string{name}, args...)
		return exec.Command("true")
	}
	if rc := cmdDo([]string{"auto-resume", "--auto"}); rc != 0 {
		t.Fatalf("do --auto (resume) rc=%d", rc)
	}
	if !containsArg(resumeArgs, "-p") && !containsArg(resumeArgs, "--print") {
		t.Errorf("resume headless spawn must pass -p/--print: %v", resumeArgs)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestDo_Auto_CannotCombineWithHere(t *testing.T) {
	setupRoot(t)
	seedTask(t, "both-flags")
	rc := cmdDo([]string{"both-flags", "--auto", "--here"})
	if rc != 2 {
		t.Errorf("do --auto --here: rc=%d, want 2 (usage error)", rc)
	}
}

func TestRefreshAutoRunStatus_DetectsDeadPID(t *testing.T) {
	setupRoot(t)
	seedTask(t, "will-die")

	db := openTestDB(t)

	// A pid that is guaranteed not to be running: spawn `true`, wait for
	// it to exit, then use its now-dead pid.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start throwaway process: %v", err)
	}
	deadPID := cmd.Process.Pid
	cmd.Wait()

	now := store.NowISO()
	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=?, auto_run_started=? WHERE slug='will-die'`,
		deadPID, now,
	); err != nil {
		t.Fatalf("seed running auto_run: %v", err)
	}

	task, err := store.GetTask(db, "will-die")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if err := refreshAutoRunStatus(db, task); err != nil {
		t.Fatalf("refreshAutoRunStatus: %v", err)
	}

	task, _ = store.GetTask(db, "will-die")
	if task.AutoRunStatus.String != "dead" {
		t.Errorf("AutoRunStatus = %q, want dead (pid %d is no longer alive)", task.AutoRunStatus.String, deadPID)
	}
}

func TestCmdDoFreshAllocatesSessionID(t *testing.T) {
	setupRoot(t)
	seedTask(t, "fresh-task")
	_, getScript := stubITerm(t)

	const pinnedSID = "11111111-2222-3333-4444-555555555555"
	oldNewUUID := newUUID
	newUUID = func() (string, error) { return pinnedSID, nil }
	t.Cleanup(func() { newUUID = oldNewUUID })

	if rc := cmdDo([]string{"fresh-task"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}

	db := openTestDB(t)
	task, err := store.GetTask(db, "fresh-task")
	if err != nil {
		t.Fatal(err)
	}
	if !task.SessionID.Valid || task.SessionID.String != pinnedSID {
		t.Errorf("session_id after fresh spawn: got %+v, want %s", task.SessionID, pinnedSID)
	}
	if !task.SessionStarted.Valid {
		t.Error("session_started should be set after fresh spawn")
	}
	if task.Status != "in-progress" {
		t.Errorf("status: got %q, want in-progress", task.Status)
	}

	script := getScript()
	if strings.Contains(script, "--resume") {
		t.Errorf("fresh spawn should not use --resume: %s", script)
	}
	if !strings.Contains(script, "--session-id "+pinnedSID) {
		t.Errorf("fresh spawn should pass --session-id %s: %s", pinnedSID, script)
	}
	if !strings.Contains(script, "fresh-task") {
		t.Errorf("spawn script missing task slug: %s", script)
	}
}

func TestCmdDoResumesExistingSession(t *testing.T) {
	setupRoot(t)
	seedTask(t, "old-task")

	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE tasks SET session_id='existing-sid', session_started=? WHERE slug='old-task'`, store.NowISO()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, getScript := stubITerm(t)
	if rc := cmdDo([]string{"old-task"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	db = openTestDB(t)
	task, _ := store.GetTask(db, "old-task")
	if task.SessionID.String != "existing-sid" {
		t.Errorf("session_id got %q, want existing-sid", task.SessionID.String)
	}
	if !task.SessionLastResumed.Valid {
		t.Error("session_last_resumed should be set on resume")
	}
	script := getScript()
	if !strings.Contains(script, "--resume existing-sid") {
		t.Errorf("resume spawn should use --resume: %s", script)
	}
}

// TestCmdDoFreshRotatesStaleSession verifies --fresh overwrites an
// existing session_id with a newly-allocated UUID and spawns with that
// UUID via --session-id (not --resume).
func TestCmdDoFreshRotatesStaleSession(t *testing.T) {
	setupRoot(t)
	seedTask(t, "stale-task")

	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE tasks SET session_id='stale-uuid', session_started=? WHERE slug='stale-task'`, store.NowISO()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	const pinnedSID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	oldNewUUID := newUUID
	newUUID = func() (string, error) { return pinnedSID, nil }
	t.Cleanup(func() { newUUID = oldNewUUID })

	_, getScript := stubITerm(t)
	if rc := cmdDo([]string{"stale-task", "--fresh"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	db = openTestDB(t)
	task, _ := store.GetTask(db, "stale-task")
	if task.SessionID.String != pinnedSID {
		t.Errorf("session_id after --fresh: got %q, want %s", task.SessionID.String, pinnedSID)
	}
	script := getScript()
	if strings.Contains(script, "--resume") {
		t.Errorf("--fresh should not spawn --resume: %s", script)
	}
	if !strings.Contains(script, "--session-id "+pinnedSID) {
		t.Errorf("--fresh should spawn with --session-id %s: %s", pinnedSID, script)
	}
}

func TestCmdDoDoneTaskRefused(t *testing.T) {
	setupRoot(t)
	seedTask(t, "closed-task")

	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE tasks SET status='done', updated_at=? WHERE slug='closed-task'`, store.NowISO()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	spawns, _ := stubITerm(t)
	if rc := cmdDo([]string{"closed-task"}); rc != 1 {
		t.Errorf("rc=%d, want 1 for done task", rc)
	}
	if atomic.LoadInt64(spawns) != 0 {
		t.Errorf("done task should not spawn iTerm: got %d spawns", *spawns)
	}
}

func TestCmdDoFuzzyAmbiguous(t *testing.T) {
	setupRoot(t)
	seedTask(t, "auth fix")
	seedTask(t, "auth refactor")

	spawns, _ := stubITerm(t)
	if rc := cmdDo([]string{"auth"}); rc != 1 {
		t.Errorf("rc=%d, want 1 for ambiguous ref", rc)
	}
	if atomic.LoadInt64(spawns) != 0 {
		t.Errorf("ambiguous ref should not spawn: %d", *spawns)
	}
}

func TestCmdDoFuzzyExactWins(t *testing.T) {
	setupRoot(t)
	seedTask(t, "auth")
	seedTask(t, "auth fix")

	stubITerm(t)
	if rc := cmdDo([]string{"auth"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "auth")
	if task.Status != "in-progress" {
		t.Errorf("status=%q, want in-progress", task.Status)
	}
}

// TestCmdDoSpawnsClaudeNotFlowde pins the post-flowde contract: `focusflow do`
// shells out to `claude` directly (no wrapper) for both the fresh
// bootstrap and the resume paths. Skill freshness is now an explicit
// `focusflow skill update` step, not an implicit per-launch refresh.
func TestCmdDoSpawnsClaudeNotFlowde(t *testing.T) {
	setupRoot(t)
	seedTask(t, "wrap-fresh")

	_, getScript := stubITerm(t)
	if rc := cmdDo([]string{"wrap-fresh"}); rc != 0 {
		t.Fatalf("fresh rc=%d", rc)
	}
	script := getScript()
	if !strings.Contains(script, " claude --session-id ") {
		t.Errorf("fresh spawn must invoke claude --session-id, got:\n%s", script)
	}
	// Guard against accidental reintroduction of the flowde wrapper.
	if strings.Contains(script, "flowde") {
		t.Errorf("fresh spawn should not invoke flowde, got:\n%s", script)
	}

	// Now the resume path.
	db := openTestDB(t)
	if _, err := db.Exec(`UPDATE tasks SET session_id='resume-sid', session_started=? WHERE slug='wrap-fresh'`, store.NowISO()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if rc := cmdDo([]string{"wrap-fresh"}); rc != 0 {
		t.Fatalf("resume rc=%d", rc)
	}
	script = getScript()
	if !strings.Contains(script, " claude --resume resume-sid") {
		t.Errorf("resume spawn must invoke claude --resume <uuid>, got:\n%s", script)
	}
	if strings.Contains(script, "flowde") {
		t.Errorf("resume spawn should not invoke flowde, got:\n%s", script)
	}
}

// TestCmdDoConcurrentFreshTasks verifies two concurrent cmdDo calls on a
// fresh task don't corrupt DB state. The BEGIN IMMEDIATE lock serializes
// the txs: the winner allocates a UUID and writes it; the loser sees
// session_id already set and falls through to the resume path (spawning
// `claude --resume <winner-uuid>`). Both tabs end up pointing at the same
// session — pre-existing documented race outcome, no lost UUIDs.
func TestCmdDoConcurrentFreshTasks(t *testing.T) {
	setupRoot(t)
	seedTask(t, "race-task")
	spawns, _ := stubITerm(t)

	var wg sync.WaitGroup
	results := make([]int, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = cmdDo([]string{"race-task"}) }()
	go func() { defer wg.Done(); results[1] = cmdDo([]string{"race-task"}) }()
	wg.Wait()

	for i, rc := range results {
		if rc != 0 {
			t.Errorf("goroutine %d rc=%d", i, rc)
		}
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "race-task")
	if !task.SessionID.Valid || task.SessionID.String == "" {
		t.Errorf("session_id should be populated after races (got %+v)", task.SessionID)
	}
	if n := atomic.LoadInt64(spawns); n != 2 {
		t.Errorf("iTerm spawn count=%d, want 2", n)
	}
}

func TestBuildBootstrapPromptMentionsOther(t *testing.T) {
	got := buildBootstrapPrompt("foo")
	if !strings.Contains(got, "other:") {
		t.Errorf("expected prompt to mention other:, got:\n%s", got)
	}
	if !strings.Contains(got, "load on demand") {
		t.Errorf("expected prompt to clarify lazy loading, got:\n%s", got)
	}
}

func TestBuildBootstrapPromptForPlaybookRun(t *testing.T) {
	got := buildBootstrapPromptForKind("p--2026-04-30-10-30", "playbook_run", "p")
	if !strings.Contains(got, "playbook `p`") {
		t.Errorf("expected playbook reference, got:\n%s", got)
	}
	if !strings.Contains(got, "focusflow show playbook p") {
		t.Errorf("expected focusflow show playbook command, got:\n%s", got)
	}
	if !strings.Contains(got, "snapshotted from the playbook") {
		t.Errorf("expected snapshot framing, got:\n%s", got)
	}
	if !strings.Contains(got, "other:") {
		t.Errorf("expected mention of other:, got:\n%s", got)
	}
}

func TestBuildBootstrapPromptForRegularTask(t *testing.T) {
	got := buildBootstrapPromptForKind("foo", "regular", "")
	if strings.Contains(got, "playbook") {
		t.Errorf("regular task prompt shouldn't mention playbook:\n%s", got)
	}
	if !strings.Contains(got, "focusflow show task") {
		t.Errorf("regular task prompt should mention focusflow show task:\n%s", got)
	}
}

func TestBuildBootstrapPromptForKindWithEmptyKind(t *testing.T) {
	// Defensive: an empty kind string (legacy rows that somehow didn't
	// migrate) should fall through to the regular-task variant.
	got := buildBootstrapPromptForKind("foo", "", "")
	if strings.Contains(got, "playbook") {
		t.Errorf("empty kind should default to regular, got:\n%s", got)
	}
}

func TestBuildPlaybookRunBootstrapPromptFirstRun(t *testing.T) {
	got := buildPlaybookRunBootstrapPrompt("p--2026-04-30-10-30", "p", true)
	for _, want := range []string{
		"FIRST RUN OF THIS PLAYBOOK",
		"crystallizes",
		"Add to playbook brief",
		"Save as sidecar file",
		"Capture anything from this run back to the playbook before closing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("first-run prompt missing %q; got:\n%s", want, got)
		}
	}
}

func TestBuildPlaybookRunBootstrapPromptNotFirstRun(t *testing.T) {
	got := buildPlaybookRunBootstrapPrompt("p--2026-04-30-10-30", "p", false)
	if strings.Contains(got, "FIRST RUN OF THIS PLAYBOOK") {
		t.Errorf("non-first-run prompt should NOT have first-run banner; got:\n%s", got)
	}
	// Still has the persist-adjustments paragraph (not first-run-specific).
	if !strings.Contains(got, "adjusts the playbook") {
		t.Errorf("base playbook prompt missing persist-adjustments para")
	}
}

func TestCmdDoSetsFirstRunBannerForFirstPlaybookRun(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"playbook", "Triage", "--slug", "tri-fr", "--work-dir", wd}); rc != 0 {
		t.Fatal()
	}
	_, lastScript := stubITerm(t)
	if rc := cmdRun([]string{"playbook", "tri-fr"}); rc != 0 {
		t.Fatal()
	}
	script := lastScript()
	if !strings.Contains(script, "FIRST RUN OF THIS PLAYBOOK") {
		t.Errorf("expected first-run banner in spawn script, got:\n%s", script)
	}
}

func TestCmdDoOmitsFirstRunBannerForSecondPlaybookRun(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"playbook", "Triage 2", "--slug", "tri-2", "--work-dir", wd}); rc != 0 {
		t.Fatal()
	}
	_, lastScript := stubITerm(t)
	// First run.
	if rc := cmdRun([]string{"playbook", "tri-2"}); rc != 0 {
		t.Fatal()
	}
	if !strings.Contains(lastScript(), "FIRST RUN OF THIS PLAYBOOK") {
		t.Fatal("expected first-run banner on first invocation")
	}
	// Second run.
	if rc := cmdRun([]string{"playbook", "tri-2"}); rc != 0 {
		t.Fatal()
	}
	if strings.Contains(lastScript(), "FIRST RUN OF THIS PLAYBOOK") {
		t.Errorf("second run should NOT have first-run banner; got:\n%s", lastScript())
	}
}

func TestCmdDoEmitsPlaybookVariantForPlaybookRun(t *testing.T) {
	setupRoot(t)
	wd := t.TempDir()
	if rc := cmdAdd([]string{"playbook", "Triage", "--slug", "tri", "--work-dir", wd}); rc != 0 {
		t.Fatal()
	}

	_, lastScript := stubITerm(t)

	// Use cmdRun to create the run-task (it uses cmdDo internally).
	if rc := cmdRun([]string{"playbook", "tri"}); rc != 0 {
		t.Fatal()
	}

	script := lastScript()
	if !strings.Contains(script, "playbook `tri`") {
		t.Errorf("expected playbook prompt variant in spawn script, got:\n%s", script)
	}
	if !strings.Contains(script, "focusflow show playbook tri") {
		t.Errorf("expected 'focusflow show playbook tri' in spawn script, got:\n%s", script)
	}
}

// A task whose session is already being served refuses a second `do`
// unless --force is passed.
func TestDoRefusesLiveSessionWithoutForce(t *testing.T) {
	setupRoot(t)
	seedTask(t, "livetask")
	stubITerm(t)
	if rc := cmdDo([]string{"livetask"}); rc != 0 {
		t.Fatalf("first do rc=%d", rc)
	}
	db := openTestDB(t)
	task, err := store.GetTask(db, "livetask")
	if err != nil || !task.SessionID.Valid {
		t.Fatalf("expected a bound session, got %+v (%v)", task, err)
	}
	old := psLines
	t.Cleanup(func() { psLines = old })
	psLines = func() ([]string, error) {
		return []string{"claude --resume " + task.SessionID.String}, nil
	}

	if rc := cmdDo([]string{"livetask"}); rc == 0 {
		t.Error("expected refusal for a live session")
	}
	if rc := cmdDo([]string{"livetask", "--force"}); rc != 0 {
		t.Errorf("--force rc=%d, want 0", rc)
	}
}

func TestDoCodexHarnessOnlyWithHere(t *testing.T) {
	setupRoot(t)
	seedTask(t, "cx")
	spawns, _ := stubITerm(t)

	if rc := cmdDo([]string{"cx", "--harness", "codex"}); rc != 2 {
		t.Errorf("--harness codex without --here rc=%d, want 2", rc)
	}
	if *spawns != 0 {
		t.Error("nothing may be spawned for an unsupported harness")
	}

	t.Setenv("CODEX_THREAD_ID", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if rc := cmdDo([]string{"cx", "--here"}); rc != 0 {
		t.Fatalf("bind codex session rc=%d", rc)
	}
	task, _ := store.GetTask(openTestDB(t), "cx")
	if task.Harness.String != "codex" {
		t.Fatalf("harness = %q", task.Harness.String)
	}
	// Resuming a codex-bound task from focusflow would run claude on a
	// codex session id — refuse.
	if rc := cmdDo([]string{"cx"}); rc == 0 {
		t.Error("spawning a codex-bound task must fail")
	}
	if *spawns != 0 {
		t.Error("no spawn for a codex-bound task")
	}
}

// fakeClaudeScript writes an executable that stands in for claude and ignores its arguments.
func fakeClaudeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runWrapped starts a headless run whose "claude" is script, and returns the task as recorded.
func runWrapped(t *testing.T, slug, script string) *store.Task {
	t.Helper()
	seedTask(t, slug)
	orig := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		args[2] = script // the wrapper's $0: run the fake instead of claude
		return exec.Command(name, args...)
	}
	t.Cleanup(func() { execCommand = orig })
	if rc := cmdDo([]string{slug, "--auto"}); rc != 0 {
		t.Fatalf("do --auto rc=%d", rc)
	}
	task, err := store.GetTask(openTestDB(t), slug)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitForFile(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", p)
	return ""
}

func waitForExit(t *testing.T, pid int64) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if syscall.Kill(int(pid), 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pid %d is still running", pid)
}

func TestDo_Auto_CleanExitIsFinishedNotDead(t *testing.T) {
	setupRoot(t)
	task := runWrapped(t, "clean-run", fakeClaudeScript(t, "echo working; exit 0"))
	if got := waitForFile(t, exitFileFor(task.AutoRunLog.String)); got != "0" {
		t.Fatalf("exit file = %q, want 0", got)
	}
	waitForExit(t, task.AutoRunPID.Int64)
	db := openTestDB(t)
	if err := refreshAutoRunStatus(db, task); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetTask(db, "clean-run")
	if after.AutoRunStatus.String != "finished" {
		t.Errorf("a run that exits 0 must be finished, got %q", after.AutoRunStatus.String)
	}
}

func TestDo_Auto_FailingExitIsDead(t *testing.T) {
	setupRoot(t)
	task := runWrapped(t, "bad-run", fakeClaudeScript(t, "exit 3"))
	if got := waitForFile(t, exitFileFor(task.AutoRunLog.String)); got != "3" {
		t.Fatalf("exit file = %q, want 3", got)
	}
	waitForExit(t, task.AutoRunPID.Int64)
	db := openTestDB(t)
	refreshAutoRunStatus(db, task)
	after, _ := store.GetTask(db, "bad-run")
	if after.AutoRunStatus.String != "dead" {
		t.Errorf("a run that exits non-zero must be dead, got %q", after.AutoRunStatus.String)
	}
}

func TestDo_Auto_StoppingTheRunStopsClaudeToo(t *testing.T) {
	setupRoot(t)
	marker := filepath.Join(t.TempDir(), "child.pid")
	task := runWrapped(t, "long-run", fakeClaudeScript(t, "echo $$ > "+marker+"; exec sleep 30"))
	child, _ := strconv.Atoi(waitForFile(t, marker))
	if err := syscall.Kill(int(task.AutoRunPID.Int64), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if got := waitForFile(t, exitFileFor(task.AutoRunLog.String)); got == "0" {
		t.Errorf("a stopped run must not record success")
	}
	waitForExit(t, int64(child))
}
