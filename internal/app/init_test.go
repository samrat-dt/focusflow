package app

import (
	"focusflow/internal/store"
	"os"
	"path/filepath"
	"testing"
)

// initTempRoot points FOCUSFLOW_ROOT at a tempdir AND redirects $HOME so
// skill install lands inside the tempdir as well. Isolates every init test
// from the real ~/.focusflow/ and ~/.claude/skills/. Named with an `init` prefix
// to avoid colliding with withTempRoot defined in cmd_show_test.go
// (which has a different signature because it returns (root, db)).
func initTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()

	oldRoot := os.Getenv("FOCUSFLOW_ROOT")
	oldHome := os.Getenv("HOME")
	os.Setenv("FOCUSFLOW_ROOT", root)
	os.Setenv("HOME", home)
	t.Cleanup(func() {
		os.Setenv("FOCUSFLOW_ROOT", oldRoot)
		os.Setenv("HOME", oldHome)
	})
	// Tests may run from inside a live focusflow/Claude session; never let
	// its identity leak into "am I bound to a task?" lookups.
	for _, k := range []string{"FOCUSFLOW_TASK", "FOCUSFLOW_PROJECT", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID"} {
		t.Setenv(k, "")
	}
	return root
}

func TestCmdInitCreatesTree(t *testing.T) {
	root := initTempRoot(t)

	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("cmdInit rc=%d", rc)
	}
	for _, sub := range []string{"projects", "tasks", "kb"} {
		p := filepath.Join(root, sub)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing dir %s: %v", p, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", p)
		}
	}
}

func TestCmdInitCreatesDBQueryable(t *testing.T) {
	root := initTempRoot(t)
	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("cmdInit rc=%d", rc)
	}
	dbPath := filepath.Join(root, "focusflow.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("focusflow.db missing: %v", err)
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	// Confirm a core table exists.
	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='tasks'").Scan(&name); err != nil {
		t.Errorf("tasks table missing: %v", err)
	}
}

func TestCmdInitIdempotent(t *testing.T) {
	root := initTempRoot(t)
	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("first init rc=%d", rc)
	}
	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("second init rc=%d", rc)
	}
	// Tree and DB still there.
	if _, err := os.Stat(filepath.Join(root, "focusflow.db")); err != nil {
		t.Errorf("focusflow.db missing after second init: %v", err)
	}
}

func TestCmdInitInstallsSkill(t *testing.T) {
	initTempRoot(t)
	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("cmdInit rc=%d", rc)
	}
	skillPath := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "focusflow", "SKILL.md")
	info, err := os.Stat(skillPath)
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("SKILL.md is empty")
	}
}

func TestCmdInitSkipsSkillIfAlreadyPresent(t *testing.T) {
	initTempRoot(t)
	home := os.Getenv("HOME")
	skillPath := filepath.Join(home, ".claude", "skills", "focusflow", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("existing content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := cmdInit(nil); rc != 0 {
		t.Fatalf("cmdInit rc=%d", rc)
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing content" {
		t.Errorf("init overwrote existing skill: %q", string(data))
	}
}

func TestCmdInitRejectsExtraArgs(t *testing.T) {
	initTempRoot(t)
	if rc := cmdInit([]string{"extra"}); rc != 2 {
		t.Errorf("expected rc=2 for extra args, got %d", rc)
	}
}
