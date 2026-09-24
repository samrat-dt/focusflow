package iterm

import (
	"strings"
	"testing"
)

func capture(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := Runner
	Runner = func(args []string) error { got = args; return nil }
	t.Cleanup(func() { Runner = old })
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
	if err := SpawnTab("my task", "/w d", "claude --resume x", map[string]string{"FOCUSFLOW_TASK": "t"}); err != nil {
		t.Fatal(err)
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
	if err := SpawnTab(`a "quoted" title`, "/w", "claude", nil); err != nil {
		t.Fatal(err)
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
	if err := SpawnTab("t", "/w", "c", nil); err == nil || len(*got) != 0 {
		t.Errorf("expected error without spawning, got err=%v args=%v", err, *got)
	}
}
