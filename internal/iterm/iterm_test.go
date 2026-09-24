package iterm

import (
	"strings"
	"testing"
)

func capture(t *testing.T) *[]string {
	t.Helper()
	var got []string
	t.Cleanup(Stub(func(args []string) (string, error) { got = args; return "REF123", nil }))
	return &got
}

func TestSelectBackend(t *testing.T) {
	cases := []struct {
		focus   string
		want    Backend
		wantErr bool
	}{
		{"", ITerm2, false},
		{"iTerm2", ITerm2, false},
		{"Terminal.app", TerminalApp, false},
		{"terminal", TerminalApp, false},
		{"warpish", "", true},
	}
	for _, c := range cases {
		t.Setenv("FOCUSFLOW_TERM", c.focus)
		got, err := SelectBackend()
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("focus=%q: got %q, %v", c.focus, got, err)
		}
	}
}

func TestSpawnTabITerm2HandlesNoWindow(t *testing.T) {
	t.Setenv("FOCUSFLOW_TERM", "")
	got := capture(t)
	ref, err := SpawnTab("my task", "/w d", "claude --resume x", map[string]string{"FOCUSFLOW_TASK": "t"})
	if err != nil || ref != "iTerm2:REF123" {
		t.Fatalf("ref = %q, %v", ref, err)
	}
	script := (*got)[1]
	for _, want := range []string{`tell application "iTerm2"`, "count of windows) = 0", "create window with default profile", "create tab with default profile", `FOCUSFLOW_TASK='t'`, `cd '/w d'`} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}

func TestSpawnTabTerminalApp(t *testing.T) {
	t.Setenv("FOCUSFLOW_TERM", "Terminal.app")
	got := capture(t)
	ref, err := SpawnTab(`a "quoted" title`, "/w", "claude", nil)
	if err != nil || ref != "Terminal.app:REF123" {
		t.Fatalf("ref = %q, %v", ref, err)
	}
	script := (*got)[1]
	if !strings.Contains(script, `tell application "Terminal"`) || !strings.Contains(script, "do script") ||
		!strings.Contains(script, `custom title of newTab to "a \"quoted\" title"`) {
		t.Errorf("unexpected Terminal.app script:\n%s", script)
	}
}

func TestSpawnTabRejectsUnknownTerminal(t *testing.T) {
	t.Setenv("FOCUSFLOW_TERM", "nope")
	got := capture(t)
	if _, err := SpawnTab("t", "/w", "c", nil); err == nil || len(*got) != 0 {
		t.Errorf("expected error without spawning, got err=%v args=%v", err, *got)
	}
}

func TestFocusTab(t *testing.T) {
	t.Setenv("FOCUSFLOW_TERM", "")
	var script string
	answer := "found"
	t.Cleanup(Stub(func(args []string) (string, error) { script = args[1]; return answer, nil }))

	if ok, err := FocusTab("iTerm2:ABC-123"); err != nil || !ok {
		t.Fatalf("found = %v, %v", ok, err)
	}
	if !strings.Contains(script, `unique id of s) is "ABC-123"`) || !strings.Contains(script, "select t") {
		t.Errorf("script:\n%s", script)
	}
	answer = "missing"
	if ok, _ := FocusTab("iTerm2:GONE"); ok {
		t.Error("a closed tab must report not found")
	}
	// A ref from another terminal app is never focused with this one.
	script = ""
	if ok, _ := FocusTab("Terminal.app:/dev/ttys004"); ok || script != "" {
		t.Errorf("cross-backend ref must not run a script (ok=%v script=%q)", ok, script)
	}
	if ok, _ := FocusTab("garbage"); ok {
		t.Error("malformed ref")
	}
	t.Setenv("FOCUSFLOW_TERM", "Terminal.app")
	answer = "found"
	if ok, _ := FocusTab("Terminal.app:/dev/ttys004"); !ok || !strings.Contains(script, `(tty of t) is "/dev/ttys004"`) {
		t.Errorf("terminal focus script:\n%s", script)
	}
}
