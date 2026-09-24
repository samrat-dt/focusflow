package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"focusflow/internal/iterm"
	"focusflow/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type finding struct {
	Level  string `json:"level"` // ok | warn | fail
	Check  string `json:"check"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// doctorCtx carries what the checks need, so tests can point every path
// at a sandbox.
type doctorCtx struct {
	root       string
	home       string
	launchdCC  string // path of tend's launchd plist ("" = skip)
	pathBinary func() string
	binVersion func(bin string) string
	now        time.Time
}

func defaultDoctorCtx() (doctorCtx, error) {
	root, err := dataRoot()
	if err != nil {
		return doctorCtx{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return doctorCtx{}, err
	}
	return doctorCtx{
		root:      root,
		home:      home,
		launchdCC: filepath.Join(home, "Library", "LaunchAgents", "local.tend.plist"),
		pathBinary: func() string {
			p, _ := exec.LookPath("focusflow")
			return p
		},
		binVersion: func(bin string) string {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(c, bin, "version").Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		},
		now: time.Now(),
	}, nil
}

func ok(check, detail string) finding { return finding{"ok", check, detail, ""} }
func warn(check, detail, fix string) finding {
	return finding{"warn", check, detail, fix}
}
func fail(check, detail, fix string) finding {
	return finding{"fail", check, detail, fix}
}

// runDoctor executes every check and returns the findings in a stable order.
func runDoctor(c doctorCtx) []finding {
	var out []finding
	out = append(out, checkRoot(c)...)
	db, dbFindings := checkDB(c)
	out = append(out, dbFindings...)
	if db != nil {
		defer db.Close()
		out = append(out, checkTasks(c, db)...)
	}
	out = append(out, checkHooksAndSkill(c)...)
	out = append(out, checkBinary(c)...)
	out = append(out, checkTerminal()...)
	out = append(out, checkBackups(c)...)
	out = append(out, checkControlCenter(c)...)
	return out
}

func checkRoot(c doctorCtx) []finding {
	fi, err := os.Stat(c.root)
	if err != nil {
		return []finding{fail("data root", fmt.Sprintf("%s missing", c.root), "focusflow init")}
	}
	if fi.Mode().Perm() != privDir {
		return []finding{warn("data root", fmt.Sprintf("%s is %v, should be private (0700)", c.root, fi.Mode().Perm()), "focusflow doctor --fix")}
	}
	return []finding{ok("data root", c.root+" (private)")}
}

func checkDB(c doctorCtx) (*sql.DB, []finding) {
	path := filepath.Join(c.root, "focusflow.db")
	if _, err := os.Stat(path); err != nil {
		return nil, []finding{fail("database", "focusflow.db missing", "focusflow init, or focusflow restore <archive> --force")}
	}
	db, err := store.OpenDB(path)
	if err != nil {
		return nil, []finding{fail("database", err.Error(), "upgrade the binary (make install) or restore a backup")}
	}
	var out []finding
	var res string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&res); err != nil || res != "ok" {
		out = append(out, fail("database integrity", fmt.Sprintf("%v %s", err, res), "focusflow restore <latest backup> --force"))
	} else {
		out = append(out, ok("database integrity", "integrity_check ok"))
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	bad := 0
	if err == nil {
		for rows.Next() {
			bad++
		}
		rows.Close()
	}
	if bad > 0 {
		out = append(out, fail("foreign keys", fmt.Sprintf("%d violation(s)", bad), "inspect with sqlite3 'PRAGMA foreign_key_check'"))
	} else {
		out = append(out, ok("foreign keys", "no violations"))
	}
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		out = append(out, warn("journal mode", mode+" (expected wal)", "any focusflow command upgrades this"))
	}
	return db, out
}

func checkTasks(c doctorCtx, db *sql.DB) []finding {
	rows, err := db.Query(`SELECT ` + store.TaskCols + ` FROM tasks WHERE archived_at IS NULL`)
	if err != nil {
		return []finding{fail("tasks", err.Error(), "")}
	}
	var tasks []*store.Task
	for rows.Next() {
		if t, err := store.ScanTask(rows); err == nil {
			tasks = append(tasks, t)
		}
	}
	rows.Close()

	known := map[string]bool{}
	var missingDir, noSession, noJSONL, noBrief []string
	for _, t := range tasks {
		known[t.Slug] = true
		if t.Status == "done" {
			continue
		}
		if _, err := os.Stat(t.WorkDir); err != nil {
			missingDir = append(missingDir, t.Slug)
		}
		if t.Status == "in-progress" && !t.SessionID.Valid {
			noSession = append(noSession, t.Slug)
		}
		if t.Status == "in-progress" && t.SessionID.Valid && !isLiveOrHasJSONL(t) {
			noJSONL = append(noJSONL, t.Slug)
		}
		if _, err := os.Stat(filepath.Join(c.root, "tasks", t.Slug, "brief.md")); err != nil {
			noBrief = append(noBrief, t.Slug)
		}
	}
	var out []finding
	report := func(name string, slugs []string, lvl, detail, fix string) {
		if len(slugs) == 0 {
			return
		}
		f := finding{lvl, name, fmt.Sprintf("%s: %s", detail, strings.Join(slugs, ", ")), fix}
		out = append(out, f)
	}
	report("work_dir exists", missingDir, "warn", "work_dir missing on disk", "focusflow update task <slug> --work-dir <path>")
	report("session bound", noSession, "warn", "in-progress with no session", "focusflow do <slug>")
	report("session transcript", noJSONL, "warn", "session_id set but its transcript is gone", "focusflow do <slug> --fresh")
	report("brief present", noBrief, "warn", "no brief.md", "focusflow edit <slug>")

	// Task directories with no DB row.
	entries, _ := os.ReadDir(filepath.Join(c.root, "tasks"))
	var orphans []string
	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = ?`, e.Name()).Scan(&n)
		if n == 0 {
			orphans = append(orphans, e.Name())
		}
	}
	report("orphan task dirs", orphans, "warn", "directory without a task", "remove or re-add the task")
	if len(out) == 0 {
		out = append(out, ok("tasks", fmt.Sprintf("%d open task(s) consistent", len(tasks))))
	}
	return out
}

// isLiveOrHasJSONL: a session is fine if its transcript file exists.
func isLiveOrHasJSONL(t *store.Task) bool {
	_, err := sessionJSONLPath(t)
	return err == nil
}

func checkHooksAndSkill(c doctorCtx) []finding {
	var out []finding
	settingsPath := filepath.Join(c.home, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	var settings map[string]any
	if err == nil {
		json.Unmarshal(raw, &settings)
	}
	var missing []string
	for _, spec := range managedHooks {
		if settings == nil || !hookInstalled(settings, spec) {
			missing = append(missing, spec.Event)
		}
	}
	if len(missing) > 0 {
		out = append(out, fail("hooks", "missing in settings.json: "+strings.Join(missing, ", "), "focusflow doctor --fix"))
	} else {
		out = append(out, ok("hooks", "SessionStart, UserPromptSubmit, Stop installed"))
	}
	skillPath := filepath.Join(c.home, ".claude", "skills", "focusflow", "SKILL.md")
	got, err := os.ReadFile(skillPath)
	switch {
	case err != nil:
		out = append(out, fail("skill", "not installed", "focusflow doctor --fix"))
	case !bytes.Equal(got, embeddedSkill):
		out = append(out, warn("skill", "installed skill differs from this binary's", "focusflow doctor --fix"))
	default:
		out = append(out, ok("skill", "up to date"))
	}
	return out
}

func checkBinary(c doctorCtx) []finding {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	onPath := c.pathBinary()
	if onPath == "" {
		return []finding{fail("binary on PATH", "focusflow not found on PATH", "make install, and add ~/.local/bin to PATH")}
	}
	resolved, _ := filepath.EvalSymlinks(onPath)
	if resolved != self {
		v := c.binVersion(onPath)
		return []finding{warn("binary on PATH", fmt.Sprintf("running %s but PATH resolves %s (%s)", self, onPath, v), "make install (single installed copy)")}
	}
	return []finding{ok("binary on PATH", onPath)}
}

func checkTerminal() []finding {
	b, err := iterm.SelectBackend()
	if err != nil {
		return []finding{fail("terminal", err.Error(), "set FOCUSFLOW_TERM to iTerm2 or Terminal.app")}
	}
	return []finding{ok("terminal", string(b))}
}

func checkBackups(c doctorCtx) []finding {
	matches, _ := filepath.Glob(filepath.Join(c.root, backupDirName, backupPrefix+"*.tar.gz"))
	if len(matches) == 0 {
		return []finding{warn("backups", "no backup yet", "focusflow backup")}
	}
	newest := matches[0]
	for _, m := range matches {
		if m > newest {
			newest = m
		}
	}
	if fi, err := os.Stat(newest); err == nil && c.now.Sub(fi.ModTime()) > 7*24*time.Hour {
		return []finding{warn("backups", "newest backup is over 7 days old", "focusflow backup")}
	}
	detail := fmt.Sprintf("%d kept, newest %s", len(matches), filepath.Base(newest))
	if p, err := backupPlistPath(); err == nil {
		if _, err := os.Stat(p); err != nil {
			return []finding{warn("backups", detail+"; no automatic schedule", "focusflow backup schedule install")}
		}
	}
	return []finding{ok("backups", detail)}
}

var plistBinRe = regexp.MustCompile(`(?s)<key>FOCUSFLOW_BIN</key>\s*<string>([^<]+)</string>`)

func checkControlCenter(c doctorCtx) []finding {
	if c.launchdCC == "" {
		return nil
	}
	raw, err := os.ReadFile(c.launchdCC)
	if err != nil {
		return nil // tend not installed — nothing to check
	}
	m := plistBinRe.FindSubmatch(raw)
	if m == nil {
		return []finding{warn("tend", "launchd job has no FOCUSFLOW_BIN", "")}
	}
	bin := string(m[1])
	if _, err := os.Stat(bin); err != nil {
		return []finding{fail("tend", "FOCUSFLOW_BIN "+bin+" does not exist", "point the launchd job at the installed focusflow")}
	}
	if v := c.binVersion(bin); v != "" {
		self := c.binVersion(os.Args[0])
		if self != "" && v != self {
			return []finding{warn("tend", fmt.Sprintf("uses %s (%s), this is %s", bin, v, self), "make install")}
		}
	}
	return []finding{ok("tend", "uses "+bin)}
}

func cmdDoctor(args []string) int {
	fs := flagSet("doctor")
	fix := fs.Bool("fix", false, "apply safe fixes (permissions, hooks, skill)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := defaultDoctorCtx()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *fix {
		applyDoctorFixes(c)
	}
	findings := runDoctor(c)
	failed := 0
	for _, f := range findings {
		if f.Level == "fail" {
			failed++
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(findings)
	} else {
		for _, f := range findings {
			tag := map[string]string{"ok": " ok ", "warn": "warn", "fail": "FAIL"}[f.Level]
			line := fmt.Sprintf("[%s] %-24s %s", tag, f.Check, f.Detail)
			fmt.Println(strings.TrimRight(line, " "))
			if f.Level != "ok" && f.Fix != "" {
				fmt.Printf("       fix: %s\n", f.Fix)
			}
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// applyDoctorFixes performs only fixes that are safe and idempotent.
func applyDoctorFixes(c doctorCtx) {
	if n, err := hardenRoot(c.root); err == nil && n > 0 {
		fmt.Printf("fixed: made %d entries under %s private\n", n, c.root)
	}
	if changed, err := installSessionStartHook(); err == nil && changed {
		fmt.Println("fixed: installed missing hooks")
	}
	if p, err := skillInstallPath(); err == nil {
		if got, rerr := os.ReadFile(p); rerr != nil || !bytes.Equal(got, embeddedSkill) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
				if os.WriteFile(p, embeddedSkill, 0o644) == nil {
					_ = writeSkillVersion(Version)
					fmt.Println("fixed: refreshed the skill")
				}
			}
		}
	}
}
