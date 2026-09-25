package app

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"focusflow/internal/store"
)

func TestBackupRestoreRoundTrip(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "keepme")
	os.WriteFile(filepath.Join(root, "tasks/keepme/updates/2026-01-01-n.md"), []byte("note\n"), 0o600)

	if rc := cmdBackup(nil); rc != 0 {
		t.Fatalf("backup rc=%d", rc)
	}
	archives, _ := filepath.Glob(filepath.Join(root, "backups", backupPrefix+"*.tar.gz"))
	if len(archives) != 1 {
		t.Fatalf("archives = %v", archives)
	}
	if m := mode(t, archives[0]); m != privFile {
		t.Errorf("archive mode %v, want 0600", m)
	}

	// Mutate after the backup, then restore.
	seedTask(t, "added-later")
	os.Remove(filepath.Join(root, "tasks/keepme/updates/2026-01-01-n.md"))

	if rc := cmdRestore([]string{archives[0]}); rc != 2 {
		t.Errorf("restore without --force rc=%d, want 2", rc)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks/added-later")); err != nil {
		t.Fatal("restore without --force must not touch data")
	}
	if rc := cmdRestore([]string{"--force", archives[0]}); rc != 0 {
		t.Fatalf("restore rc=%d", rc)
	}

	if b, err := os.ReadFile(filepath.Join(root, "tasks/keepme/updates/2026-01-01-n.md")); err != nil || string(b) != "note\n" {
		t.Errorf("update file not restored: %q %v", b, err)
	}
	db := openTestDB(t)
	var n int
	db.QueryRow("SELECT COUNT(*) FROM tasks WHERE slug='added-later'").Scan(&n)
	if n != 0 {
		t.Error("post-backup task survived restore")
	}
	db.QueryRow("SELECT COUNT(*) FROM tasks WHERE slug='keepme'").Scan(&n)
	if n != 1 {
		t.Error("backed-up task missing after restore")
	}
	aside, _ := filepath.Glob(filepath.Join(root, "backups", "pre-restore-*", "tasks", "added-later"))
	if len(aside) != 1 {
		t.Errorf("previous data not kept aside: %v", aside)
	}
}

func TestBackupExcludesBackupsDirAndIsConsistent(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "a")
	for i := 0; i < 2; i++ {
		if rc := cmdBackup(nil); rc != 0 {
			t.Fatalf("backup %d rc=%d", i, rc)
		}
	}
	archives, _ := filepath.Glob(filepath.Join(root, "backups", "*.tar.gz"))
	if len(archives) != 2 {
		t.Fatalf("want 2 distinct archives, got %v", archives)
	}
	stage := t.TempDir()
	if err := extractBackup(archives[1], stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "backups")); err == nil {
		t.Error("archive nests the backups directory")
	}
	for _, f := range []string{"focusflow.db-wal", "focusflow.db-shm"} {
		if _, err := os.Stat(filepath.Join(stage, f)); err == nil {
			t.Errorf("archive contains raw %s", f)
		}
	}
}

func TestPruneBackupsKeepsNewestOnly(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"20260101T000000", "20260102T000000", "20260103T000000"} {
		os.WriteFile(filepath.Join(dir, backupPrefix+n+".tar.gz"), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("x"), 0o600)
	n, err := pruneBackups(dir, 2)
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, backupPrefix+"20260101T000000.tar.gz")); err == nil {
		t.Error("oldest not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Error("pruned an unrelated file")
	}
}

func TestExtractBackupRejectsPathTraversal(t *testing.T) {
	arc := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, _ := os.Create(arc)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := "pwn"
	tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	f.Close()

	dst := t.TempDir()
	err := extractBackup(arc, dst)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("err = %v, want unsafe path", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); err == nil {
		t.Error("file escaped the destination")
	}
}

func TestRestoreRejectsCorruptArchive(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "safe")
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	os.WriteFile(bad, []byte("not an archive"), 0o600)
	if rc := cmdRestore([]string{"--force", bad}); rc == 0 {
		t.Error("corrupt archive must fail")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks/safe")); err != nil {
		t.Error("data disturbed by a failed restore")
	}
}

func TestBackupScheduleInstallUninstall(t *testing.T) {
	setupRoot(t)
	var calls [][]string
	old := launchctl
	launchctl = func(args ...string) ([]byte, error) { calls = append(calls, args); return nil, nil }
	t.Cleanup(func() { launchctl = old })

	if rc := cmdBackup([]string{"schedule", "install"}); rc != 0 {
		t.Fatalf("install rc=%d", rc)
	}
	plist, _ := backupPlistPath()
	b, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	for _, want := range []string{backupSchedulerLabel, "<string>backup</string>", "<key>Hour</key><integer>2</integer>", "backup.log"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("plist missing %q", want)
		}
	}
	boot := false
	for _, c := range calls {
		boot = boot || c[0] == "bootstrap"
	}
	if !boot {
		t.Errorf("launchctl calls: %v", calls)
	}
	out := captureStdout(t, func() { cmdBackup([]string{"schedule", "status"}) })
	if !strings.Contains(out, "installed and loaded") {
		t.Errorf("status = %q", out)
	}
	if rc := cmdBackup([]string{"schedule", "uninstall"}); rc != 0 {
		t.Fatal("uninstall")
	}
	if _, err := os.Stat(plist); err == nil {
		t.Error("plist not removed")
	}
	if cmdBackup([]string{"schedule", "bogus"}) != 2 {
		t.Error("unknown schedule subcommand must be a usage error")
	}
}

// A backup taken while another writer is active must still produce an
// intact, restorable database.
func TestBackupWhileDBInUse(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "base")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	failed := make(chan int, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if rc := cmdAdd([]string{"task", fmt.Sprintf("busy-%d", i)}); rc != 0 {
				select {
				case failed <- rc:
				default:
				}
				return
			}
		}
	}()

	var archives []string
	for i := 0; i < 3; i++ {
		a, err := createBackup(root, filepath.Join(root, "backups"))
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("backup %d during writes: %v", i, err)
		}
		archives = append(archives, a)
	}
	close(stop)
	wg.Wait()
	select {
	case rc := <-failed:
		t.Fatalf("concurrent writer failed rc=%d", rc)
	default:
	}

	for _, a := range archives {
		stage := t.TempDir()
		if err := extractBackup(a, stage); err != nil {
			t.Fatal(err)
		}
		db, err := store.OpenDB(filepath.Join(stage, "focusflow.db"))
		if err != nil {
			t.Fatalf("open %s: %v", a, err)
		}
		var res string
		db.QueryRow("PRAGMA integrity_check").Scan(&res)
		var n int
		db.QueryRow("SELECT COUNT(*) FROM tasks WHERE slug='base'").Scan(&n)
		db.Close()
		if res != "ok" || n != 1 {
			t.Errorf("%s: integrity=%q base=%d", a, res, n)
		}
	}
}
