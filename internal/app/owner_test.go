package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"focusflow/internal/store"
)

func TestParseEvery(t *testing.T) {
	ok := map[string]time.Duration{"30m": 30 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour, "1h30m": 90 * time.Minute}
	for in, want := range ok {
		if got, err := parseEvery(in); err != nil || got != want {
			t.Errorf("parseEvery(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "5", "30s", "abc", "0m", "100d", "-1h"} {
		if _, err := parseEvery(bad); err == nil {
			t.Errorf("parseEvery(%q) should fail", bad)
		}
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"2026-09-25 14:30":     time.Date(2026, 9, 25, 14, 30, 0, 0, time.Local),
		"2026-09-25":           time.Date(2026, 9, 25, 9, 0, 0, 0, time.Local),
		"tomorrow":             time.Date(2026, 9, 25, 9, 0, 0, 0, time.Local),
		"tomorrow 07:15":       time.Date(2026, 9, 25, 7, 15, 0, 0, time.Local),
		"today 18:00":          time.Date(2026, 9, 24, 18, 0, 0, 0, time.Local),
		"2026-09-25T08:00:00Z": time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := parseWhen(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "someday", "tomorrow 25:00", "tomorrow 9am"} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("parseWhen(%q) should fail", bad)
		}
	}
}

func newOwner(t *testing.T, slug, every string) string {
	t.Helper()
	wd := t.TempDir()
	if rc := cmdAdd([]string{"owner", "Inbox Zero", "--slug", slug, "--work-dir", wd, "--every", every}); rc != 0 {
		t.Fatalf("add owner rc=%d", rc)
	}
	return wd
}

func TestAddOwnerCreatesPausedOwnerWithCharter(t *testing.T) {
	root := setupRoot(t)
	wd := newOwner(t, "inbox", "2h")
	o, err := store.GetOwner(openTestDB(t), "inbox")
	if err != nil || o.Status != "paused" || o.Every != "2h" || o.WorkDir != wd {
		t.Fatalf("owner = %+v, %v", o, err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "owners/inbox/brief.md")); err != nil || !strings.Contains(string(b), "Inbox Zero") {
		t.Errorf("charter missing: %v", err)
	}
	for name, args := range map[string][]string{
		"no every":     {"owner", "X", "--work-dir", t.TempDir()},
		"bad every":    {"owner", "X", "--work-dir", t.TempDir(), "--every", "5"},
		"no work dir":  {"owner", "X", "--every", "1h"},
		"bad project":  {"owner", "X", "--work-dir", t.TempDir(), "--every", "1h", "--project", "ghost"},
		"missing name": {"owner"},
	} {
		if rc := cmdAdd(args); rc == 0 {
			t.Errorf("%s: expected failure", name)
		}
	}
}

func TestOwnerLifecycleStartPauseRetireDelete(t *testing.T) {
	root := setupRoot(t)
	newOwner(t, "o1", "1h")
	db := openTestDB(t)
	status := func() string { o, _ := store.GetOwner(db, "o1"); return o.Status }

	if cmdOwner([]string{"start", "o1"}) != 0 || status() != "active" {
		t.Fatalf("start: %s", status())
	}
	if o, _ := store.GetOwner(db, "o1"); !o.NextWakeAt.Valid {
		t.Error("start must schedule an immediate first tick")
	}
	if cmdOwner([]string{"pause", "o1"}) != 0 || status() != "paused" {
		t.Fatalf("pause: %s", status())
	}
	if cmdOwner([]string{"retire", "o1"}) != 0 || status() != "retired" {
		t.Fatalf("retire: %s", status())
	}
	if _, err := os.Stat(filepath.Join(root, "owners/o1/brief.md")); err != nil {
		t.Error("retire must preserve files")
	}
	if rc := cmdOwner([]string{"tick", "o1"}); rc == 0 {
		t.Error("a retired owner must not tick")
	}
	if cmdOwner([]string{"start", "o1"}) != 0 || status() != "active" {
		t.Fatal("start must reactivate a retired owner")
	}
	if o, _ := store.GetOwner(db, "o1"); o.ArchivedAt.Valid {
		t.Error("reactivation must clear archived_at")
	}
	if cmdOwner([]string{"retire", "o1", "--delete"}) != 0 {
		t.Fatal("retire --delete")
	}
	if _, err := store.GetOwner(db, "o1"); err == nil {
		t.Error("row not deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "owners/o1")); err == nil {
		t.Error("directory not deleted")
	}
	if cmdOwner([]string{"show", "o1"}) == 0 || cmdOwner([]string{"bogus"}) != 2 {
		t.Error("show of a missing owner / unknown subcommand must fail")
	}
}

func TestOwnerNext(t *testing.T) {
	setupRoot(t)
	newOwner(t, "n1", "1h")
	db := openTestDB(t)
	if cmdOwner([]string{"next", "n1", "--in", "3h"}) != 0 {
		t.Fatal("next --in")
	}
	o, _ := store.GetOwner(db, "n1")
	when, _ := time.Parse(time.RFC3339, o.NextWakeAt.String)
	if d := time.Until(when); d < 179*time.Minute || d > 181*time.Minute {
		t.Errorf("next wake in %v, want ~3h", d)
	}
	for name, args := range map[string][]string{
		"neither":  {"next", "n1"},
		"both":     {"next", "n1", "--in", "1h", "--at", "tomorrow"},
		"past":     {"next", "n1", "--at", "2001-01-01"},
		"bad in":   {"next", "n1", "--in", "soon"},
		"no owner": {"next", "ghost", "--in", "1h"},
	} {
		if cmdOwner(args) == 0 {
			t.Errorf("%s: expected failure", name)
		}
	}
}

func TestOwnerTickCreatesTaggedTaskWithCharterAndSpawnsTab(t *testing.T) {
	root := setupRoot(t)
	wd := newOwner(t, "t1", "2h")
	os.WriteFile(filepath.Join(root, "owners/t1/brief.md"), []byte("# Charter\nWatch the inbox for invoices\n"), 0o600)
	spawns, _ := stubITerm(t)

	if rc := cmdOwner([]string{"tick", "t1"}); rc != 0 {
		t.Fatalf("tick rc=%d", rc)
	}
	if *spawns != 1 {
		t.Errorf("spawns = %d, want 1", *spawns)
	}
	db := openTestDB(t)
	var slug string
	if err := db.QueryRow(`SELECT t.slug FROM tasks t JOIN task_tags g ON g.task_slug=t.slug WHERE g.tag='owner:t1'`).Scan(&slug); err != nil {
		t.Fatalf("no tagged tick task: %v", err)
	}
	if !strings.HasPrefix(slug, "t1-tick-") {
		t.Errorf("tick slug = %q", slug)
	}
	task, _ := store.GetTask(db, slug)
	if task.WorkDir != wd || task.Status != "in-progress" {
		t.Errorf("tick task = %+v", task)
	}
	brief, _ := os.ReadFile(filepath.Join(root, "tasks", slug, "brief.md"))
	for _, want := range []string{"Watch the inbox for invoices", "focusflow owner next t1", "focusflow done " + slug, "owner:t1", filepath.Join(root, "owners/t1/updates")} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("tick brief missing %q", want)
		}
	}
	o, _ := store.GetOwner(db, "t1")
	if !o.LastTickAt.Valid || o.LastTickStatus.String != "running" {
		t.Errorf("tick not recorded: %+v", o)
	}
	next, _ := time.Parse(time.RFC3339, o.NextWakeAt.String)
	if d := time.Until(next); d < 119*time.Minute || d > 121*time.Minute {
		t.Errorf("fallback next wake in %v, want ~2h", d)
	}
}

func stubExec(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = old })
	return &calls
}

func TestOwnerTickDueRunsOnlyDueActiveOwnersHeadless(t *testing.T) {
	setupRoot(t)
	stubITerm(t)
	calls := stubExec(t)
	for _, s := range []string{"due", "later", "paused"} {
		newOwner(t, s, "1h")
	}
	db := openTestDB(t)
	past := time.Now().Add(-time.Minute).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	store.SetOwnerStatus(db, "due", "active", past)
	store.SetOwnerStatus(db, "later", "active", future)
	// "paused" stays paused

	out := captureStdout(t, func() {
		if rc := cmdOwner([]string{"tick-due"}); rc != 0 {
			t.Fatalf("tick-due rc=%d", rc)
		}
	})
	if !strings.Contains(out, "dispatched 1 owner tick(s)") {
		t.Errorf("output: %q", out)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0], "claude") {
		t.Errorf("expected exactly one headless claude run, got %v", *calls)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM task_tags WHERE tag IN ('owner:later','owner:paused')`).Scan(&n)
	if n != 0 {
		t.Error("non-due/paused owners must not tick")
	}
	o, _ := store.GetOwner(db, "due")
	if next, _ := time.Parse(time.RFC3339, o.NextWakeAt.String); !next.After(time.Now()) {
		t.Errorf("due owner must be rescheduled into the future, got %s", o.NextWakeAt.String)
	}
}

func TestOwnerTickDueDoesNotStackRunningTicks(t *testing.T) {
	setupRoot(t)
	stubITerm(t)
	stubExec(t)
	newOwner(t, "busy", "1h")
	db := openTestDB(t)
	store.SetOwnerStatus(db, "busy", "active", time.Now().Add(-time.Minute).Format(time.RFC3339))
	if rc := cmdOwner([]string{"tick", "busy", "--auto"}); rc != 0 {
		t.Fatal("first tick")
	}
	// Make the previous run look alive, and the owner due again.
	db.Exec(`UPDATE tasks SET auto_run_status='running', auto_run_pid=?  WHERE slug LIKE 'busy-tick-%'`, os.Getpid())
	store.SetOwnerNextWake(db, "busy", time.Now().Add(-time.Minute).Format(time.RFC3339))

	out := captureStdout(t, func() { cmdOwner([]string{"tick-due"}) })
	if !strings.Contains(out, "dispatched 0") {
		t.Errorf("a still-running tick must block a new one: %q", out)
	}
}

func TestOwnerListShowAndSchedulerPlist(t *testing.T) {
	setupRoot(t)
	if out := captureStdout(t, func() { cmdList([]string{"owners"}) }); !strings.Contains(out, "No owners") {
		t.Errorf("empty list: %q", out)
	}
	newOwner(t, "l1", "30m")
	out := captureStdout(t, func() { cmdList([]string{"owners"}) })
	if !strings.Contains(out, "l1") || !strings.Contains(out, "paused") {
		t.Errorf("list: %q", out)
	}
	show := captureStdout(t, func() { cmdShow([]string{"owner", "l1"}) })
	if !strings.Contains(show, "every:       30m") || !strings.Contains(show, "in flight:   (nothing)") {
		t.Errorf("show: %q", show)
	}

	p := schedulerPlist("/bin/focusflow", "/data")
	for _, want := range []string{schedulerLabel, "<string>owner</string><string>tick-due</string>", "<integer>60</integer>", "/data/owner-scheduler.log"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestOwnerSchedulerInstallUninstallUsesLaunchctl(t *testing.T) {
	setupRoot(t)
	var calls [][]string
	old := launchctl
	launchctl = func(args ...string) ([]byte, error) { calls = append(calls, args); return nil, nil }
	t.Cleanup(func() { launchctl = old })

	if rc := ownerScheduler([]string{"install"}); rc != 0 {
		t.Fatalf("install rc=%d", rc)
	}
	plist, _ := schedulerPlistPath()
	if _, err := os.Stat(plist); err != nil {
		t.Fatalf("plist not written into the sandbox HOME: %v", err)
	}
	sawBootstrap := false
	for _, c := range calls {
		if c[0] == "bootstrap" {
			sawBootstrap = true
		}
	}
	if !sawBootstrap {
		t.Errorf("launchctl calls: %v", calls)
	}
	if rc := ownerScheduler([]string{"uninstall"}); rc != 0 {
		t.Fatal("uninstall")
	}
	if _, err := os.Stat(plist); err == nil {
		t.Error("plist not removed")
	}
	if ownerScheduler([]string{"bogus"}) != 2 {
		t.Error("unknown scheduler subcommand must be a usage error")
	}
}
