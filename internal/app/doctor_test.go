package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDoctorCtx(t *testing.T) doctorCtx {
	t.Helper()
	c, err := defaultDoctorCtx()
	if err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	c.launchdDashboard = ""
	c.pathBinary = func() string { return self }
	c.binVersion = func(string) string { return Version }
	return c
}

func level(fs []finding, check string) string {
	for _, f := range fs {
		if f.Check == check {
			return f.Level
		}
	}
	return "absent"
}

func TestDoctorHealthyInstall(t *testing.T) {
	setupRoot(t)
	seedTask(t, "fine")
	fs := runDoctor(testDoctorCtx(t))
	for _, f := range fs {
		if f.Level == "fail" {
			t.Errorf("unexpected failure: %+v", f)
		}
	}
	for check, want := range map[string]string{"data root": "ok", "database integrity": "ok", "hooks": "ok", "skill": "ok", "tasks": "ok", "backups": "warn"} {
		if got := level(fs, check); got != want {
			t.Errorf("%s = %s, want %s", check, got, want)
		}
	}
}

func TestDoctorFlagsAndFixesMissingHooksAndPerms(t *testing.T) {
	root := setupRoot(t)
	c := testDoctorCtx(t)
	settings, _ := userSettingsPath()
	os.Remove(settings)
	os.Chmod(root, 0o755)

	fs := runDoctor(c)
	if level(fs, "hooks") != "fail" || level(fs, "data root") != "warn" {
		t.Fatalf("expected hooks=fail data root=warn, got %+v", fs)
	}
	applyDoctorFixes(c)
	fs = runDoctor(c)
	if level(fs, "hooks") != "ok" || level(fs, "data root") != "ok" {
		t.Errorf("fix did not resolve: %+v", fs)
	}
}

func TestDoctorTaskProblems(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "gone")
	db := openTestDB(t)
	db.Exec(`UPDATE tasks SET work_dir='/definitely/not/here', status='in-progress' WHERE slug='gone'`)
	os.MkdirAll(filepath.Join(root, "tasks", "orphan"), 0o700)

	fs := runDoctor(testDoctorCtx(t))
	for _, want := range []string{"work_dir exists", "session bound", "orphan task dirs"} {
		if level(fs, want) != "warn" {
			t.Errorf("%s should warn: %+v", want, fs)
		}
	}
}

func TestDoctorMissingDatabaseFails(t *testing.T) {
	root := initTempRoot(t)
	os.MkdirAll(root, 0o700)
	fs := runDoctor(testDoctorCtx(t))
	if level(fs, "database") != "fail" {
		t.Errorf("missing db should fail: %+v", fs)
	}
}

func TestDoctorRecentBackupIsOK(t *testing.T) {
	setupRoot(t)
	if rc := cmdBackup(nil); rc != 0 {
		t.Fatal("backup failed")
	}
	c := testDoctorCtx(t)
	if got := level(runDoctor(c), "backups"); got != "warn" {
		t.Errorf("recent backup without a schedule = %s, want warn", got)
	}
	plist, _ := backupPlistPath()
	os.MkdirAll(filepath.Dir(plist), 0o755)
	os.WriteFile(plist, []byte("<plist/>"), 0o644)
	if got := level(runDoctor(c), "backups"); got != "ok" {
		t.Errorf("recent backup with a schedule = %s, want ok", got)
	}
	c.now = time.Now().Add(10 * 24 * time.Hour)
	if got := level(runDoctor(c), "backups"); got != "warn" {
		t.Errorf("old backup = %s, want warn", got)
	}
}
