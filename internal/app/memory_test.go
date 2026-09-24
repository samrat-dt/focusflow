package app

import (
	"encoding/json"
	"strings"
	"testing"

	"focusflow/internal/store"
)

// seedProjectTasks makes project "web" with tasks "pricing" and "launch".
func seedProjectTasks(t *testing.T) {
	t.Helper()
	wd := t.TempDir()
	if rc := cmdAdd([]string{"project", "Web", "--slug", "web", "--work-dir", wd}); rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	for _, s := range []string{"pricing", "launch"} {
		if rc := cmdAdd([]string{"task", s, "--slug", s, "--project", "web"}); rc != 0 {
			t.Fatalf("add task %s rc=%d", s, rc)
		}
	}
}

func memoriesFor(t *testing.T, f store.MemoryFilter) []*store.Memory {
	t.Helper()
	db := openTestDB(t)
	list, err := store.ListMemories(db, f)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestMemory_HumanAnswerToAgentQuestionIsRemembered(t *testing.T) {
	setupRoot(t)
	seedProjectTasks(t)

	// The agent (bound to pricing) asks the human something.
	bindSession(t, "pricing", "11111111-1111-4111-8111-111111111111")
	captureStdout(t, func() {
		if rc := cmdMessage([]string{"user", "Annual or monthly first?"}); rc != 0 {
			t.Fatalf("agent message rc=%d", rc)
		}
	})
	if got := memoriesFor(t, store.MemoryFilter{}); len(got) != 0 {
		t.Fatalf("an agent's message must not become a memory, got %d", len(got))
	}

	// The human answers from the dashboard (with its delivery cue), then gives an instruction.
	unbindSession(t)
	captureStdout(t, func() {
		cmdMessage([]string{"user/pricing", `Monthly first (via focusflow: reply with focusflow message user "<answer>" --reply-to <this message's id>)`})
		cmdMessage([]string{"user/pricing", "Also add a FAQ below the table"})
	})
	got := memoriesFor(t, store.MemoryFilter{TaskSlug: "pricing"})
	if len(got) != 2 {
		t.Fatalf("got %d memories, want 2", len(got))
	}
	instr, ans := got[0], got[1] // newest first
	if ans.Kind != "answer" || ans.Question.String != "Annual or monthly first?" || ans.Body != "Monthly first" {
		t.Errorf("answer = %+v", ans)
	}
	if ans.ProjectSlug.String != "web" || ans.Scope != "task" || ans.Source != "message" {
		t.Errorf("answer scope = %+v", ans)
	}
	if instr.Kind != "instruction" || instr.Question.Valid {
		t.Errorf("second message should be an instruction, got %+v", instr)
	}
}

func TestMemory_RecalledAtSessionStartAcrossTheProject(t *testing.T) {
	setupRoot(t)
	seedProjectTasks(t)
	unbindSession(t)
	captureStdout(t, func() {
		cmdMessage([]string{"user/pricing", "Use GBP everywhere"})
		if rc := cmdRemember([]string{"Never push to main directly", "--global"}); rc != 0 {
			t.Fatalf("remember --global rc=%d", rc)
		}
		if rc := cmdRemember([]string{"Brand colour is forest green", "--project", "web"}); rc != 0 {
			t.Fatalf("remember --project rc=%d", rc)
		}
	})

	hookFor := func(slug string) string {
		t.Setenv("FOCUSFLOW_TASK", slug)
		return captureStdout(t, func() {
			if rc := cmdHookSessionStart(nil); rc != 0 {
				t.Fatalf("hook rc=%d", rc)
			}
		})
	}
	own := hookFor("pricing")
	for _, want := range []string{"WHAT YOU ALREADY KNOW", "Never push to main directly", "Brand colour is forest green", "Use GBP everywhere", "This task"} {
		if !strings.Contains(own, want) {
			t.Errorf("pricing session context missing %q", want)
		}
	}
	// A sibling task sees project and global memories, but not pricing's instructions.
	sib := hookFor("launch")
	if !strings.Contains(sib, "Brand colour is forest green") || !strings.Contains(sib, "Never push to main directly") {
		t.Errorf("launch should see project and global memories:\n%s", sib)
	}
	if strings.Contains(sib, "Use GBP everywhere") {
		t.Errorf("another task's instruction must not leak into launch")
	}
}

func TestMemory_ListPinPromoteForget(t *testing.T) {
	setupRoot(t)
	seedProjectTasks(t)
	unbindSession(t)
	captureStdout(t, func() { cmdMessage([]string{"user/pricing", "Monthly first"}) })
	m := memoriesFor(t, store.MemoryFilter{TaskSlug: "pricing"})[0]

	out := captureStdout(t, func() {
		if rc := cmdMemories([]string{"--project", "web", "--json"}); rc != 0 {
			t.Fatalf("memories rc=%d", rc)
		}
	})
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0]["body"] != "Monthly first" {
		t.Fatalf("memories --json = %s (%v)", out, err)
	}

	captureStdout(t, func() {
		if rc := cmdMemory([]string{"promote", m.ID}); rc != 0 {
			t.Fatalf("promote rc=%d", rc)
		}
	})
	t.Setenv("FOCUSFLOW_TASK", "launch")
	if ctx := captureStdout(t, func() { cmdHookSessionStart(nil) }); !strings.Contains(ctx, "Monthly first") {
		t.Errorf("a promoted memory applies to the whole project")
	}

	captureStdout(t, func() {
		if rc := cmdForget([]string{m.ID}); rc != 0 {
			t.Fatalf("forget rc=%d", rc)
		}
	})
	if ctx := captureStdout(t, func() { cmdHookSessionStart(nil) }); strings.Contains(ctx, "WHAT YOU ALREADY KNOW") {
		t.Errorf("a forgotten memory is not recalled")
	}
	if got := memoriesFor(t, store.MemoryFilter{IncludeGone: true}); len(got) != 1 || got[0].Status != "forgotten" {
		t.Errorf("forgotten memories are kept, marked forgotten: %+v", got)
	}
}

func TestMemory_RememberNeedsAScopeOutsideASession(t *testing.T) {
	setupRoot(t)
	seedProjectTasks(t)
	unbindSession(t)
	if rc := cmdRemember([]string{"floating fact"}); rc != 2 {
		t.Errorf("remember without scope outside a session: rc=%d, want 2", rc)
	}
	if rc := cmdRemember([]string{"x", "--task", "pricing", "--global"}); rc != 2 {
		t.Errorf("two scopes: rc=%d, want 2", rc)
	}
	// Inside a bound session the task is implied and the source is the agent.
	bindSession(t, "launch", "22222222-2222-4222-8222-222222222222")
	captureStdout(t, func() {
		if rc := cmdRemember([]string{"Launch copy stays under 80 words"}); rc != 0 {
			t.Fatalf("remember in session rc=%d", rc)
		}
	})
	got := memoriesFor(t, store.MemoryFilter{TaskSlug: "launch"})
	if len(got) != 1 || got[0].Source != "agent" || got[0].Kind != "decision" {
		t.Errorf("got %+v", got)
	}
}

func TestMemory_CleanHumanTextStripsCues(t *testing.T) {
	for in, want := range map[string]string{
		`Yes (via focusflow: reply with focusflow message user "<answer>" --reply-to <this message's id>)`:                                                                                      "Yes",
		`Go ahead (via focusflow: reply with focusflow message user "<answer>". You were started headlessly; do the request, reply, and do not run focusflow done unless the task is complete)`: "Go ahead",
		"  two\n lines  ": "two lines",
	} {
		if got := store.CleanHumanText(in); got != want {
			t.Errorf("CleanHumanText(%q) = %q, want %q", in, got, want)
		}
	}
}
