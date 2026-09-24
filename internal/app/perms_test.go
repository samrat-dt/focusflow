package app

import (
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestHardenRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(root, "tasks/t/updates"), 0o755)
	os.WriteFile(filepath.Join(root, "focusflow.db"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "tasks/t/brief.md"), []byte("x"), 0o664)
	os.WriteFile(filepath.Join(outside, "victim"), []byte("x"), 0o644)
	os.Symlink(filepath.Join(outside, "victim"), filepath.Join(root, "link"))

	n, err := hardenRoot(root)
	if err != nil || n == 0 {
		t.Fatalf("hardenRoot = %d, %v", n, err)
	}
	for _, p := range []string{root, filepath.Join(root, "tasks"), filepath.Join(root, "tasks/t/updates")} {
		if m := mode(t, p); m != privDir {
			t.Errorf("%s = %v, want %v", p, m, privDir)
		}
	}
	for _, p := range []string{filepath.Join(root, "focusflow.db"), filepath.Join(root, "tasks/t/brief.md")} {
		if m := mode(t, p); m != privFile {
			t.Errorf("%s = %v, want %v", p, m, privFile)
		}
	}
	if m := mode(t, filepath.Join(outside, "victim")); m != 0o644 {
		t.Errorf("symlink target outside the tree was modified: %v", m)
	}
	if n2, _ := hardenRoot(root); n2 != 0 {
		t.Errorf("second run changed %d entries, want 0 (idempotent)", n2)
	}
}

func TestInitAndAddCreatePrivateTree(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "secret")
	for _, p := range []string{root, filepath.Join(root, "kb"), filepath.Join(root, "tasks/secret"), filepath.Join(root, "tasks/secret/updates")} {
		if m := mode(t, p); m != privDir {
			t.Errorf("dir %s = %v", p, m)
		}
	}
	for _, p := range []string{filepath.Join(root, "focusflow.db"), filepath.Join(root, "kb/user.md"), filepath.Join(root, "tasks/secret/brief.md")} {
		if m := mode(t, p); m != privFile {
			t.Errorf("file %s = %v", p, m)
		}
	}
}

func TestEnsureRootPrivateFixesLegacyInstall(t *testing.T) {
	root := setupRoot(t)
	os.Chmod(root, 0o755)
	os.Chmod(filepath.Join(root, "focusflow.db"), 0o644)
	ensureRootPrivate()
	if mode(t, root) != privDir || mode(t, filepath.Join(root, "focusflow.db")) != privFile {
		t.Error("legacy tree not hardened")
	}
}
