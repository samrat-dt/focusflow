package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

func TestSyncInitCommitsNotesButNeverTheDatabase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := setupRoot(t)
	seedTask(t, "notes")
	os.WriteFile(filepath.Join(root, "tasks/notes/updates/2026-01-01-x.md"), []byte("hello\n"), 0o600)

	if rc := cmdSync([]string{"init"}); rc != 0 {
		t.Fatalf("init rc=%d", rc)
	}
	tracked := gitOut(t, root, "ls-files")
	if !strings.Contains(tracked, "tasks/notes/brief.md") || !strings.Contains(tracked, "tasks/notes/updates/2026-01-01-x.md") || !strings.Contains(tracked, "kb/user.md") {
		t.Errorf("notes not tracked:\n%s", tracked)
	}
	for _, banned := range []string{"focusflow.db", "backups/", "workspace/"} {
		if strings.Contains(tracked, banned) {
			t.Errorf("%s must not be tracked:\n%s", banned, tracked)
		}
	}
	if out, err := runGit(root, "config", "--local", "--get", "user.name"); err == nil {
		t.Errorf("sync must not write a git identity into the repo config, found %q", out)
	}
}

func TestSyncPushAndPullRoundTripViaBareRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	root := setupRoot(t)
	seedTask(t, "a")
	if rc := cmdSync([]string{"init", "--remote", remote}); rc != 0 {
		t.Fatalf("init rc=%d", rc)
	}
	os.WriteFile(filepath.Join(root, "tasks/a/updates/2026-02-02-y.md"), []byte("second\n"), 0o600)
	if rc := cmdSync(nil); rc != 0 {
		t.Fatalf("sync rc=%d", rc)
	}

	// A second machine clones and sees the notes; its edit syncs back.
	other := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-b", "main", remote, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v %s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(other, "tasks/a/updates/2026-02-02-y.md")); err != nil || string(b) != "second\n" {
		t.Fatalf("clone missing note: %v", err)
	}
	os.WriteFile(filepath.Join(other, "kb/org.md"), []byte("- 2026-03-03 — from the other machine\n"), 0o600)
	gitOut(t, other, "add", "-A")
	gitOut(t, other, "commit", "-m", "other")
	gitOut(t, other, "push", "origin", "main")

	if rc := cmdSync(nil); rc != 0 {
		t.Fatalf("second sync rc=%d", rc)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "kb/org.md")); !strings.Contains(string(b), "other machine") {
		t.Errorf("pull did not bring the remote change:\n%s", b)
	}
}

func TestSyncWithoutInitFailsClearly(t *testing.T) {
	setupRoot(t)
	if rc := cmdSync(nil); rc == 0 {
		t.Error("sync before init must fail")
	}
	if rc := cmdSync([]string{"bogus"}); rc != 2 {
		t.Errorf("unknown subcommand rc=%d, want 2", rc)
	}
}
