package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"focusflow/internal/iterm"
	"focusflow/internal/store"
)

func projectFixture(t *testing.T) string {
	t.Helper()
	root := setupRoot(t)
	if rc := cmdAdd([]string{"project", "Resume", "--slug", "resume", "--work-dir", t.TempDir()}); rc != 0 {
		t.Fatal("add project")
	}
	for _, s := range []string{"a", "b", "c"} {
		if rc := cmdAdd([]string{"task", "Task " + s, "--slug", s, "--project", "resume"}); rc != 0 {
			t.Fatal("add task")
		}
	}
	return root
}

func TestProjectAgentIsCreatedOnceAndHidden(t *testing.T) {
	root := projectFixture(t)
	db := openTestDB(t)
	out1 := captureStdout(t, func() { cmdProject([]string{"agent", "resume"}) })
	out2 := captureStdout(t, func() { cmdProject([]string{"agent", "resume"}) })
	lastLine := func(s string) string { l := strings.Split(strings.TrimSpace(s), "\n"); return l[len(l)-1] }
	if lastLine(out1) != "resume-agent" || lastLine(out2) != "resume-agent" || strings.Contains(out2, "created") {
		t.Fatalf("agent slug output %q / %q", out1, out2)
	}
	p, _ := store.GetProject(db, "resume")
	if p.AgentTaskSlug.String != "resume-agent" {
		t.Errorf("project not linked: %+v", p.AgentTaskSlug)
	}
	agent, _ := store.GetTask(db, "resume-agent")
	if agent.Kind != KindProjectAgent || agent.ProjectSlug.String != "resume" {
		t.Errorf("agent = %+v", agent)
	}
	brief, _ := os.ReadFile(filepath.Join(root, "tasks/resume-agent/brief.md"))
	for _, want := range []string{"permanent coordinator", "focusflow add task", "focusflow message user/<task>", "never run focusflow done on yourself"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("coordinator brief missing %q", want)
		}
	}

	// Hidden from default listings, counts, and the project view.
	list := captureStdout(t, func() { cmdList([]string{"tasks"}) })
	if strings.Contains(list, "resume-agent") {
		t.Errorf("agent leaked into list tasks: %q", list)
	}
	counts, _ := projectTaskCounts(db, "resume")
	if counts.total != 3 {
		t.Errorf("project total = %d, want 3 (agent excluded)", counts.total)
	}
	if show := captureStdout(t, func() { cmdShow([]string{"project", "resume"}) }); !strings.Contains(show, "3 total") {
		t.Errorf("show project counts the agent: %q", show)
	}
	if all := captureStdout(t, func() { cmdList([]string{"tasks", "--kind", "all"}) }); !strings.Contains(all, "resume-agent") {
		t.Errorf("--kind all must include the agent: %q", all)
	}
	if cmdDone([]string{"resume-agent"}) == 0 {
		t.Error("a coordinator is permanent and can never be marked done")
	}
	if cmdProject([]string{"agent", "ghost"}) == 0 || cmdProject([]string{"bogus", "x"}) != 2 || cmdProject(nil) != 2 {
		t.Error("error handling")
	}
}

func TestProjectMessageDoesNotFanOutUnlessAsked(t *testing.T) {
	projectFixture(t)
	db := openTestDB(t)
	cmdAdd([]string{"task", "finished", "--slug", "fin", "--project", "resume"})
	cmdUpdate([]string{"task", "fin", "--status", "done"})

	if rc := cmdProject([]string{"message", "resume", "make B do the thing"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	agentMsgs, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: "resume-agent"}, false)
	if len(agentMsgs) != 1 || agentMsgs[0].Body != "make B do the thing" {
		t.Fatalf("coordinator mailbox = %+v", agentMsgs)
	}
	if m, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: "b"}, false); len(m) != 0 {
		t.Error("no fan-out was requested")
	}

	out := captureStdout(t, func() { cmdProject([]string{"message", "resume", "heads up", "--fanout", "--urgent"}) })
	if !strings.Contains(out, "Fanned out to 3 open subtask(s)") {
		t.Errorf("output %q", out)
	}
	for _, slug := range []string{"a", "b", "c"} {
		m, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: slug}, false)
		if len(m) != 1 || !m[0].Urgent || !strings.Contains(m[0].Body, "[from project resume] heads up") {
			t.Errorf("%s mailbox = %+v", slug, m)
		}
	}
	if m, _ := store.ListBusMessages(db, store.MailboxFilter{TaskSlug: "fin"}, false); len(m) != 0 {
		t.Error("done tasks must not receive fan-out")
	}
	if cmdProject([]string{"message", "resume"}) != 2 || cmdProject([]string{"message", "resume", "  "}) != 2 {
		t.Error("usage errors")
	}
}

func TestProjectScheduleCreatesAnActiveOwnerAndUnscheduleParksIt(t *testing.T) {
	projectFixture(t)
	db := openTestDB(t)
	if rc := cmdProject([]string{"schedule", "resume", "--every", "12h"}); rc != 0 {
		t.Fatalf("schedule rc=%d", rc)
	}
	o, err := store.GetOwner(db, "resume-agent-owner")
	if err != nil || o.Status != "active" || o.Every != "12h" || o.ProjectSlug.String != "resume" {
		t.Fatalf("owner = %+v, %v", o, err)
	}
	if cmdProject([]string{"schedule", "resume"}) == 0 {
		t.Error("a second schedule must be refused")
	}
	if cmdProject([]string{"schedule", "resume", "--every", "5"}) == 0 {
		t.Error("bad cadence")
	}
	if cmdProject([]string{"unschedule", "resume"}) != 0 {
		t.Fatal("unschedule")
	}
	if o, _ = store.GetOwner(db, "resume-agent-owner"); o.Status != "paused" {
		t.Errorf("status = %s", o.Status)
	}
}

func TestDoFreshNeedsForceOnlyWhenARealConversationExists(t *testing.T) {
	setupRoot(t)
	seedTask(t, "one")
	stubITerm(t)
	if cmdDo([]string{"one"}) != 0 {
		t.Fatal("first do")
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "one")

	// Stale session (no transcript on disk): --fresh just rotates it.
	if rc := cmdDo([]string{"one", "--fresh"}); rc != 0 {
		t.Errorf("--fresh on a stale session rc=%d", rc)
	}

	// A real transcript exists: one session per task, forever.
	task, _ = store.GetTask(db, "one")
	home, _ := os.UserHomeDir()
	jsonl := filepath.Join(home, ".claude", "projects", EncodeCwdForClaude(task.WorkDir), task.SessionID.String+".jsonl")
	os.MkdirAll(filepath.Dir(jsonl), 0o755)
	os.WriteFile(jsonl, []byte("{}\n"), 0o644)
	if rc := cmdDo([]string{"one", "--fresh"}); rc != 2 {
		t.Errorf("--fresh over a real conversation rc=%d, want 2", rc)
	}
	if rc := cmdDo([]string{"one", "--fresh", "--force"}); rc != 0 {
		t.Errorf("--fresh --force rc=%d", rc)
	}
}

func TestDoRemembersTerminalTabAndFocusesItInsteadOfOpeningAnother(t *testing.T) {
	setupRoot(t)
	seedTask(t, "tab")
	spawns, _ := stubITerm(t)
	if cmdDo([]string{"tab"}) != 0 {
		t.Fatal("first do")
	}
	db := openTestDB(t)
	task, _ := store.GetTask(db, "tab")
	if task.TerminalRef.String != "iTerm2:stub-tab-ref" {
		t.Fatalf("terminal_ref = %q", task.TerminalRef.String)
	}
	if *spawns != 1 {
		t.Fatalf("spawns = %d", *spawns)
	}

	// The session is live: `do` focuses the remembered tab, no second window.
	old := psLines
	psLines = func() ([]string, error) { return []string{"claude --resume " + task.SessionID.String}, nil }
	t.Cleanup(func() { psLines = old })
	var scripts []string
	t.Cleanup(iterm.Stub(func(args []string) (string, error) { scripts = append(scripts, args[1]); return "found", nil }))

	out := captureStdout(t, func() {
		if rc := cmdDo([]string{"tab"}); rc != 0 {
			t.Fatalf("second do rc=%d", rc)
		}
	})
	if !strings.Contains(out, "Focused tab") || len(scripts) != 1 || !strings.Contains(scripts[0], "select t") {
		t.Errorf("out=%q scripts=%v", out, scripts)
	}

	// Tab closed but process still around: refuse (old behavior) unless --force.
	t.Cleanup(iterm.Stub(func(args []string) (string, error) { return "missing", nil }))
	if rc := cmdDo([]string{"tab"}); rc == 0 {
		t.Error("with the tab gone the live-session guard must refuse")
	}
}
