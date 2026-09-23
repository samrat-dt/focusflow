# focusflow-parity Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring focusflow's `list tasks|projects|playbooks`, `add task`, `update task`, `do`, and `run playbook` CLI surfaces to exact flag-for-flag, schema-for-schema parity with the live `flow` binary (v0.1.0-alpha.28) — the subset that `control-center` actually depends on — while leaving real flow (`~/.flow/flow.db`, `~/.local/bin/flow`) provably untouched.

**Architecture:** Additive schema migrations (new columns/tables, idempotent `ALTER TABLE ... ADD COLUMN` + `CREATE TABLE IF NOT EXISTS`, following the existing `runMigrations` pattern in `internal/flowdb/db.go`) followed by CLI-surface changes in `internal/app/*.go` that mirror flow's exact flag names/types/defaults. A new process-liveness helper (mockable via a package-level func var, matching the existing `iterm.Runner` pattern) backs the `live` field. `--format table|json|tsv` output modes are added to the three `list` subcommands using field names/order reverse-engineered from live flow's actual JSON/TSV samples. `do --auto` becomes a real headless background runner tracked via new `auto_run_*` columns.

**Tech Stack:** Go, `modernc.org/sqlite` (no CGO), `database/sql`, stdlib `flag.FlagSet`, stdlib `os/exec` for `ps`-based liveness checks and headless `claude` process spawning.

**Spec:** `/Users/samrattalukder/.flow/tasks/focusflow-parity/brief.md` (the flow task brief this plan implements) + the ground-truth schema of real flow's `~/.flow/flow.db` (captured read-only via `sqlite3 -readonly ... .schema` in this planning session — see Task 1 for the exact DDL). This is Phase 1 of 2: messaging (`message`/`broadcast`/`inbox`/`watch`), `stats`, and `owner` are Phase 2, tracked separately and NOT covered by this plan.

## Global Constraints

- No CGO — pure Go `modernc.org/sqlite` driver only.
- Flag parsing via `flag.FlagSet` with `ContinueOnError`, created through the existing `flagSet()` helper in `internal/app/helpers.go` — never `flag.Parse()`.
- Exit codes: 0 = success, 1 = runtime error, 2 = usage error.
- Timestamps: RFC3339 strings everywhere (`flowdb.NowISO()`), never Unix timestamps.
- Tests: table-driven where possible; real SQLite in a temp dir (no DB mocks); only external processes (osascript, claude, `ps`) are mocked, via package-level function vars.
- **Real flow (`~/.flow/flow.db`, `~/.local/bin/flow`) must never be written to by any test or manual verification step in this plan.** Any comparison against live flow uses only `--help`, `list`, `show`, or direct `sqlite3 -readonly` schema reads — never `add`, `do`, `done`, `update`, `edit`, `archive`, `hook`, or `skill install/uninstall`.
- `list tasks --format json`'s field set, exactly (from a live sample, confirmed this session): `slug, name, status, priority, project, live, updated, tags, archived, auto_run, age_days`. No `kind` field is ever emitted, even under `--kind all`.
- `list tasks --format tsv`'s header, exactly (from a live sample, confirmed this session): `slug status priority project age_days due_in_days due_label stale stale_days waiting_on assignee live auto_run auto_run_pid archived tags`.
- Real flow's `tasks` table schema (ground truth, read via `sqlite3 -readonly`), the columns this plan's migration must add to focusflow: `assignee TEXT`, `harness TEXT`, `auto_run_status TEXT`, `auto_run_pid INTEGER`, `auto_run_started TEXT`, `auto_run_finished TEXT`, `auto_run_log TEXT`; plus a new `task_tags(task_slug TEXT, tag TEXT, created_at TEXT, PRIMARY KEY(task_slug, tag))` table with an index on `tag`; plus a unique index `idx_tasks_session_id ON tasks(session_id) WHERE session_id IS NOT NULL` (focusflow currently lacks this uniqueness invariant entirely).
- **Deliberate deviation from flow's schema:** flow has `CHECK (status = 'backlog' OR session_id IS NOT NULL)` on `tasks`. Do NOT port this constraint — focusflow's `flow-mirror` rows (see `internal/app/mirror.go`, `UpsertMirroredTask`) can legitimately be `status='in-progress'` with no `session_id` (session lifecycle belongs to real flow for mirrored rows). Adding this CHECK would break mirroring. Enforce nothing extra here; just don't regress existing mirror behavior.

## Review Focus

- **`kind` field leakage in JSON output.** It would be natural (and wrong) to include the existing `Kind` field (`regular`/`playbook_run`) in `list tasks --format json` output since it's already on the `Task` struct — live flow's JSON never has a `kind` key, even under `--kind all`. Task 3's test must assert the JSON object's exact key set, not just that expected keys are present.
- **`--tag` filter semantics with multiple values.** Passing `--tag a --tag b` could mean "has tag a AND tag b" or "has tag a OR tag b" — real flow's exact semantics weren't observed live (repeatable-flag filter behavior wasn't safely exercised against production data). Task 3 must pick one interpretation (AND, matching how most CLI multi-value filters narrow results, e.g. `docker ps --filter`), document the choice in a code comment, and pin it with a test — not leave it ambiguous.
- **`--no-truncate` as a no-op.** The existing `list tasks` table renderer hard-caps identifier width at 40 runes (`internal/app/list.go`, the `identW` clamp). A naive `--no-truncate` implementation that's accepted but never actually bypasses that cap would silently ship a flag that does nothing. Task 3's test must assert a >40-char slug renders unclipped under `--no-truncate` and clipped without it.
- **Deleting `due.go` breaks `add.go`'s `--due` flag.** `addTask` in `internal/app/add.go` calls `parseDueDate` (defined in `internal/app/due.go`). Task 2 removes the `due` top-level command per the user's explicit choice, but `parseDueDate` itself must be relocated (not deleted) or `add task --due` stops compiling. Task 2's steps must move `parseDueDate` into a file that survives (e.g. `internal/app/dateparse.go`) before deleting `due.go`.
- **`do --auto` orphan/zombie detection.** If the headless `claude` process crashes or is killed externally, `auto_run_status` must not stay `'running'` forever — control-center's dashboard treats a stuck `'running'` with a dead PID as a lie. Task 8's test must simulate a dead PID (a `--session-id`'d process that has already exited) and assert the liveness/status-read path reports `'dead'`, not stale `'running'`.

---

## File Structure

- **`internal/flowdb/db.go`** (modify) — schema migration additions: `tasks.assignee`, `tasks.harness`, `tasks.auto_run_*` columns; new `task_tags` table; new unique index on `session_id`; new `Task` struct fields; new `TagFilter`/tag CRUD query functions (`AddTag`, `RemoveTag`, `ClearTags`, `ListTagsForTask`, `ListAllTags`); `TaskFilter.Tags []string` field wired into `ListTasks`'s WHERE-building.
- **`internal/app/add.go`** (modify) — `addTask`: add `--assignee` and repeatable `--tag`.
- **`internal/app/update.go`** (modify — near-total rewrite of `cmdUpdateTask`) — add `--status`, `--priority`, `--assignee`, `--clear-assignee`, `--due-date`, `--clear-due`, `--waiting`, `--clear-waiting`, `--tag` (repeatable), `--remove-tag` (repeatable), `--clear-tags`; remove `--session-id`.
- **`internal/app/due.go`** (delete) — `cmdDue` removed; `parseDueDate` relocated to `internal/app/dateparse.go` first.
- **`internal/app/dateparse.go`** (create) — houses `parseDueDate` (moved verbatim from `due.go`) so `add.go` keeps compiling after `due.go` is deleted.
- **`internal/app/priority.go`** (delete) — `cmdPriority` removed; its project-priority-update capability folds into `update project --priority` (already exists as a flag — verify, add if missing).
- **`internal/app/waiting.go`** (delete) — `cmdWaiting` and `resolveTaskForWaiting` removed; the resolution logic (task-only, reject project refs) moves inline into `cmdUpdateTask`'s `--waiting`/`--clear-waiting` handling in `update.go`.
- **`internal/app/app.go`** (modify) — remove `due`/`priority`/`waiting` dispatch cases from `Run()`'s switch.
- **`internal/app/list.go`** (modify — largest change) — `--format table|json|tsv`, `--tag` (repeatable), `--no-color`, `--no-truncate` on `listTasksCmd`, `listProjectsCmd`, `listPlaybooksCmd`; add `listTagsCmd` + `tags` case in `cmdList`; JSON/TSV struct types and marshaling; liveness field wiring.
- **`internal/app/liveness.go`** (create) — `isSessionLive(sessionID string) bool`, backed by a mockable `var psLines = defaultPSLines` (returns `[]string` of process command lines via `ps -axo command=`), matching the `iterm.Runner` mocking pattern.
- **`internal/app/do.go`** (modify — largest change) — add `--harness`, `--with`, `--with-file`, `--force`, `--here` flags to `cmdDo`; implement `--auto` headless spawn path (`spawnHeadless` helper) writing to `tasks/<slug>/auto-runs/<timestamp>.log` and updating `auto_run_status`/`auto_run_pid`/`auto_run_started`/`auto_run_finished`.
- **`internal/app/run.go`** (modify) — `cmdRunPlaybook`: pass `--here`/`--auto` through to the delegated `cmdDo` call.
- **`internal/app/*_test.go`** — new/updated tests colocated per existing convention (`add_test.go`, `update_test.go`, `list_test.go`, `do_test.go`, `run_test.go`; new `liveness_test.go`, `dateparse_test.go`).
- **`internal/app/parity_test.go`** (create) — the repeatable parity check: golden `--help` fixtures under `internal/app/testdata/flow-help/*.txt` (captured once, read-only, from live flow) diffed against focusflow's own `--help` output per command.
- **`testdata/flow-help/*.txt`** (create, multiple files) — golden fixtures, one per command, captured from live `flow <cmd> --help` output (read-only capture, checked into the repo so the parity test needs no live flow binary at CI/test time).

---

## Task 1: Schema migration — tags, assignee, harness, auto-run columns

**Files:**
- Modify: `internal/flowdb/db.go`
- Test: `internal/flowdb/db_test.go`

**Interfaces:**
- Produces: `Task.Assignee sql.NullString`, `Task.Harness sql.NullString`, `Task.AutoRunStatus sql.NullString`, `Task.AutoRunPID sql.NullInt64`, `Task.AutoRunStarted sql.NullString`, `Task.AutoRunFinished sql.NullString`, `Task.AutoRunLog sql.NullString` (all added to the existing `Task` struct); updated `TaskCols` constant and `ScanTask` (must select/scan all new columns in the same order); `TagFilter` type; `AddTag(db *sql.DB, taskSlug, tag string) error`; `RemoveTag(db *sql.DB, taskSlug, tag string) error`; `ClearTags(db *sql.DB, taskSlug string) error`; `ListTagsForTask(db *sql.DB, taskSlug string) ([]string, error)` (sorted); `type TagCount struct { Tag string; Count int }`; `ListAllTags(db *sql.DB) ([]TagCount, error)` (sorted by tag); `TaskFilter.Tags []string` (AND semantics — see Review Focus).

- [ ] **Step 1: Write the failing migration test**

Add to `internal/flowdb/db_test.go`:

```go
func TestMigration_AddsAssigneeHarnessAutoRunTagsColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	for _, col := range []string{"assignee", "harness", "auto_run_status", "auto_run_pid", "auto_run_started", "auto_run_finished", "auto_run_log"} {
		has, err := columnExists(db, "tasks", col)
		if err != nil {
			t.Fatalf("columnExists(%s): %v", col, err)
		}
		if !has {
			t.Errorf("expected tasks.%s to exist after migration", col)
		}
	}

	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='task_tags'`).Scan(&tableCount); err != nil {
		t.Fatalf("check task_tags table: %v", err)
	}
	if tableCount != 1 {
		t.Errorf("expected task_tags table to exist, got count=%d", tableCount)
	}

	var idxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_tasks_session_id'`).Scan(&idxCount); err != nil {
		t.Fatalf("check idx_tasks_session_id: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("expected idx_tasks_session_id unique index to exist, got count=%d", idxCount)
	}
}

func TestMigration_OpeningTwiceIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}
	db1.Close()
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("second OpenDB (should be idempotent): %v", err)
	}
	db2.Close()
}

func TestSessionIDUniqueIndex_RejectsDuplicateSessionID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	now := NowISO()
	mustInsertTask(t, db, "task-a", now)
	mustInsertTask(t, db, "task-b", now)
	if _, err := db.Exec(`UPDATE tasks SET session_id='dup-session' WHERE slug='task-a'`); err != nil {
		t.Fatalf("set session_id on task-a: %v", err)
	}
	_, err = db.Exec(`UPDATE tasks SET session_id='dup-session' WHERE slug='task-b'`)
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation assigning the same session_id to a second task")
	}
}

func TestTagCRUD(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	mustInsertTask(t, db, "tagged-task", NowISO())

	if err := AddTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if err := AddTag(db, "tagged-task", "frontend"); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	// Adding the same tag twice must not error (idempotent).
	if err := AddTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("AddTag (duplicate): %v", err)
	}

	tags, err := ListTagsForTask(db, "tagged-task")
	if err != nil {
		t.Fatalf("ListTagsForTask: %v", err)
	}
	if got, want := tags, []string{"frontend", "urgent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagsForTask = %v, want %v", got, want)
	}

	all, err := ListAllTags(db)
	if err != nil {
		t.Fatalf("ListAllTags: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAllTags: expected 2 distinct tags, got %d (%v)", len(all), all)
	}

	if err := RemoveTag(db, "tagged-task", "urgent"); err != nil {
		t.Fatalf("RemoveTag: %v", err)
	}
	tags, _ = ListTagsForTask(db, "tagged-task")
	if got, want := tags, []string{"frontend"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after RemoveTag: ListTagsForTask = %v, want %v", got, want)
	}

	if err := ClearTags(db, "tagged-task"); err != nil {
		t.Fatalf("ClearTags: %v", err)
	}
	tags, _ = ListTagsForTask(db, "tagged-task")
	if len(tags) != 0 {
		t.Errorf("after ClearTags: expected no tags, got %v", tags)
	}
}

func TestListTasks_FilterByTags_ANDSemantics(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "focusflow.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	mustInsertTask(t, db, "both-tags", NowISO())
	mustInsertTask(t, db, "one-tag", NowISO())
	AddTag(db, "both-tags", "a")
	AddTag(db, "both-tags", "b")
	AddTag(db, "one-tag", "a")

	tasks, err := ListTasks(db, TaskFilter{Tags: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Slug != "both-tags" {
		t.Errorf("ListTasks with Tags=[a,b] (AND semantics) = %+v, want only both-tags", tasks)
	}
}

// mustInsertTask inserts a minimal valid task row for tests that only
// need a task to exist, not its full field set.
func mustInsertTask(t *testing.T, db *sql.DB, slug, now string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (slug, name, status, priority, work_dir, created_at, updated_at) VALUES (?, ?, 'backlog', 'medium', '/tmp', ?, ?)`,
		slug, slug, now, now,
	); err != nil {
		t.Fatalf("insert task %s: %v", slug, err)
	}
}
```

Add `"reflect"` and `"path/filepath"` to the test file's imports if not already present (check the top of `internal/flowdb/db_test.go` first — it likely already imports `path/filepath` and `database/sql`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/flowdb/... -run 'TestMigration_AddsAssigneeHarnessAutoRunTagsColumns|TestSessionIDUniqueIndex|TestTagCRUD|TestListTasks_FilterByTags' -v`
Expected: FAIL — `columnExists` returns false for the new columns, `task_tags` table doesn't exist, `AddTag`/`RemoveTag`/etc. are undefined.

- [ ] **Step 3: Implement the migration and tag CRUD**

In `internal/flowdb/db.go`, extend `runMigrations` (append after the existing `tasks.source` migration block, before the `indexesPostMigrate` exec):

```go
	for _, col := range []struct{ name, ddl string }{
		{"assignee", "assignee TEXT"},
		{"harness", "harness TEXT"},
		{"auto_run_status", "auto_run_status TEXT"},
		{"auto_run_pid", "auto_run_pid INTEGER"},
		{"auto_run_started", "auto_run_started TEXT"},
		{"auto_run_finished", "auto_run_finished TEXT"},
		{"auto_run_log", "auto_run_log TEXT"},
	} {
		has, err := columnExists(db, "tasks", col.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(`ALTER TABLE tasks ADD COLUMN ` + col.ddl); err != nil {
				return fmt.Errorf("add tasks.%s: %w", col.name, err)
			}
		}
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS task_tags (
		    task_slug   TEXT NOT NULL REFERENCES tasks(slug) ON DELETE CASCADE,
		    tag         TEXT NOT NULL,
		    created_at  TEXT NOT NULL,
		    PRIMARY KEY (task_slug, tag)
		)
	`); err != nil {
		return fmt.Errorf("create task_tags: %w", err)
	}
```

And extend `indexesPostMigrate`'s const string with:

```go
CREATE INDEX IF NOT EXISTS idx_task_tags_tag ON task_tags(tag);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_session_id ON tasks(session_id) WHERE session_id IS NOT NULL;
```

Update the `Task` struct (add fields after `ArchivedAt`... actually insert logically near `SessionLastResumed`, before `CreatedAt`, to group session/run-lifecycle fields together):

```go
	Assignee           sql.NullString
	Harness            sql.NullString
	AutoRunStatus      sql.NullString // "" | "running" | "completed" | "dead"
	AutoRunPID         sql.NullInt64
	AutoRunStarted     sql.NullString
	AutoRunFinished    sql.NullString
	AutoRunLog         sql.NullString
```

Update `TaskCols` to append `, assignee, harness, auto_run_status, auto_run_pid, auto_run_started, auto_run_finished, auto_run_log` and update `ScanTask`'s `row.Scan(...)` call to scan the seven new fields in the same order, into the same-order struct fields.

Add `Tags []string` to `TaskFilter`. In `ListTasks`, after the existing `if filter.Since != ""` block, add AND-semantics tag filtering (one tag per subquery, so N tags require N matches — this is what makes it AND not OR):

```go
	for _, tag := range filter.Tags {
		where = append(where, "slug IN (SELECT task_slug FROM task_tags WHERE tag = ?)")
		args = append(args, tag)
	}
```

Add the tag CRUD functions (near the end of the file, before the flow-mirror section):

```go
// AddTag associates tag with taskSlug. Idempotent — adding an existing
// tag is a no-op, not an error.
func AddTag(db *sql.DB, taskSlug, tag string) error {
	_, err := db.Exec(
		`INSERT INTO task_tags (task_slug, tag, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(task_slug, tag) DO NOTHING`,
		taskSlug, tag, NowISO(),
	)
	if err != nil {
		return fmt.Errorf("add tag %q to %s: %w", tag, taskSlug, err)
	}
	return nil
}

// RemoveTag disassociates tag from taskSlug. Removing a tag that isn't
// present is a no-op, not an error.
func RemoveTag(db *sql.DB, taskSlug, tag string) error {
	if _, err := db.Exec(`DELETE FROM task_tags WHERE task_slug = ? AND tag = ?`, taskSlug, tag); err != nil {
		return fmt.Errorf("remove tag %q from %s: %w", tag, taskSlug, err)
	}
	return nil
}

// ClearTags removes all tags from taskSlug.
func ClearTags(db *sql.DB, taskSlug string) error {
	if _, err := db.Exec(`DELETE FROM task_tags WHERE task_slug = ?`, taskSlug); err != nil {
		return fmt.Errorf("clear tags for %s: %w", taskSlug, err)
	}
	return nil
}

// ListTagsForTask returns taskSlug's tags, sorted alphabetically.
func ListTagsForTask(db *sql.DB, taskSlug string) ([]string, error) {
	rows, err := db.Query(`SELECT tag FROM task_tags WHERE task_slug = ? ORDER BY tag`, taskSlug)
	if err != nil {
		return nil, fmt.Errorf("list tags for %s: %w", taskSlug, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

// TagCount pairs a tag with how many non-archived tasks carry it.
type TagCount struct {
	Tag   string
	Count int
}

// ListAllTags returns every distinct tag in use (across non-archived
// tasks) with its usage count, sorted alphabetically by tag.
func ListAllTags(db *sql.DB) ([]TagCount, error) {
	rows, err := db.Query(`
		SELECT tt.tag, COUNT(*) FROM task_tags tt
		JOIN tasks t ON t.slug = tt.task_slug
		WHERE t.archived_at IS NULL
		GROUP BY tt.tag ORDER BY tt.tag
	`)
	if err != nil {
		return nil, fmt.Errorf("list all tags: %w", err)
	}
	defer rows.Close()
	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/flowdb/... -v`
Expected: PASS for all tests in the package, including the pre-existing ones (this confirms the migration didn't break anything already covered).

- [ ] **Step 5: Run the full test suite to catch ripple effects**

Run: `go build ./... && go test ./...`
Expected: builds clean; other packages (`internal/app`) may now fail to compile if they construct `Task{}` literals positionally rather than by field name — check for compile errors and fix any found (should be none, since Go struct literals in this codebase use field names based on the files read so far, but verify).

- [ ] **Step 6: Commit**

```bash
git add internal/flowdb/db.go internal/flowdb/db_test.go
git commit -m "feat(flowdb): add tags, assignee, harness, auto-run columns for flow parity"
```

## Task 2: `add task`/`update task` overhaul — tags, assignee, status/priority/due/waiting flags; remove due/priority/waiting commands and --session-id

**Files:**
- Modify: `internal/app/add.go`, `internal/app/update.go`, `internal/app/app.go`
- Create: `internal/app/dateparse.go`
- Delete: `internal/app/due.go`, `internal/app/priority.go`, `internal/app/waiting.go`
- Modify/relocate tests: `internal/app/due_test.go` → `internal/app/dateparse_test.go` (rename, keep `parseDueDate` tests); `internal/app/priority_test.go`, `internal/app/waiting_test.go` (delete or fold surviving assertions into `internal/app/update_test.go`)
- Test: `internal/app/add_test.go`, `internal/app/update_test.go`

**Interfaces:**
- Consumes: `flowdb.AddTag`, `flowdb.RemoveTag`, `flowdb.ClearTags`, `flowdb.ListTagsForTask` (Task 1).
- Produces: `addTask` accepts `--assignee <name>` and repeatable `--tag <t>`; `cmdUpdateTask` accepts `--status`, `--priority`, `--assignee`, `--clear-assignee`, `--due-date`, `--clear-due`, `--waiting`, `--clear-waiting`, `--tag` (repeatable), `--remove-tag` (repeatable), `--clear-tags`, `--work-dir`, `--mkdir`; `--session-id` is gone.

- [ ] **Step 1: Move `parseDueDate` out of `due.go` before deleting it**

Create `internal/app/dateparse.go` containing exactly the `parseDueDate` function and its doc comment, copied verbatim from the current `internal/app/due.go` (the function starting `func parseDueDate(s string, now time.Time) (time.Time, error) {` through its closing brace), with package `app` and imports `"fmt"`, `"strings"`, `"time"`.

Rename `internal/app/due_test.go` to `internal/app/dateparse_test.go`, keeping only the `TestParseDueDate`-style tests (the ones that test `parseDueDate` directly) — delete any test in that file that calls `cmdDue` (those test the command being removed).

- [ ] **Step 2: Run the build to confirm the move is safe**

Run: `go build ./...`
Expected: PASS — `add.go`'s call to `parseDueDate` still resolves (same package), even before `due.go` is deleted (duplicate definition would fail — so do this step BEFORE deleting `due.go` only if you kept both; instead, delete `due.go`'s `cmdDue` function but temporarily leave `parseDueDate` there commented as "moved" — no, simplest: delete `due.go` entirely in this same step and rely on `dateparse.go` now providing `parseDueDate`.)

Run: `rm internal/app/due.go && go build ./...`
Expected: PASS.

- [ ] **Step 3: Delete `priority.go` and `waiting.go`, remove their dispatch cases**

```bash
rm internal/app/priority.go internal/app/waiting.go internal/app/priority_test.go internal/app/waiting_test.go
```

In `internal/app/app.go`, remove these three `case` blocks from the switch in `Run()`:

```go
	case "due":
		return cmdDue(rest)
```
```go
	case "priority":
		return cmdPriority(rest)
```
```go
	case "waiting":
		return cmdWaiting(rest)
```

Run: `go build ./...`
Expected: FAIL — `cmdUpdateTask` doesn't yet have equivalents, and any e2e test (`internal/app/e2e_test.go`) that calls `focusflow due|priority|waiting ...` now fails at the dispatch level. That's expected; Step 6 rewrites `cmdUpdateTask` and Step 7 fixes callers.

- [ ] **Step 4: Write the failing test for `add task --assignee --tag`**

Add to `internal/app/add_test.go`:

```go
func TestAddTask_AssigneeAndTags(t *testing.T) {
	env := newTestEnv(t) // existing test harness helper — check add_test.go's existing tests for the exact setup helper name and use it (likely something like setupTestEnv(t) or similar already used by other tests in this file; match that pattern instead of inventing a new one).
	rc := app.Run([]string{"add", "task", "Fix login bug", "--slug", "fix-login", "--work-dir", env.WorkDir, "--assignee", "sam", "--tag", "urgent", "--tag", "frontend"})
	if rc != 0 {
		t.Fatalf("add task rc=%d", rc)
	}
	db := env.OpenDB(t)
	defer db.Close()
	task, err := flowdb.GetTask(db, "fix-login")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !task.Assignee.Valid || task.Assignee.String != "sam" {
		t.Errorf("Assignee = %+v, want sam", task.Assignee)
	}
	tags, err := flowdb.ListTagsForTask(db, "fix-login")
	if err != nil {
		t.Fatalf("ListTagsForTask: %v", err)
	}
	if got, want := tags, []string{"frontend", "urgent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
}
```

Before writing this, open `internal/app/add_test.go` and `internal/app/testhelpers_test.go` to find the actual test-environment setup helper this codebase uses (its name and how it exposes `WorkDir` / a way to open the DB) — reuse it exactly rather than the placeholder names above; the placeholder names are illustrative of intent, not literal.

- [ ] **Step 5: Run the test to verify it fails**

Run: `go test ./internal/app/... -run TestAddTask_AssigneeAndTags -v`
Expected: FAIL — `flag provided but not defined: -assignee`.

- [ ] **Step 6: Implement `--assignee`/`--tag` in `addTask`**

In `internal/app/add.go`'s `addTask`, add flag declarations alongside the existing ones:

```go
	assignee := fs.String("assignee", "", "person or agent this task is assigned to")
	var tags stringSliceFlag
	fs.Var(&tags, "tag", "tag to apply (repeatable)")
```

This needs a repeatable-string flag type. Add to `internal/app/helpers.go`:

```go
// stringSliceFlag implements flag.Value to collect repeated -tag flags
// into a slice, in the order given.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}
```

Add `"strings"` to `helpers.go`'s imports.

After the existing `INSERT INTO tasks (...)` call in `addTask`, before the `os.MkdirAll` call for `updates/`, add:

```go
	if *assignee != "" {
		if _, err := db.Exec(`UPDATE tasks SET assignee = ? WHERE slug = ?`, *assignee, slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: set assignee: %v\n", err)
			return 1
		}
	}
	for _, tag := range tags {
		if err := flowdb.AddTag(db, slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `go test ./internal/app/... -run TestAddTask_AssigneeAndTags -v`
Expected: PASS.

- [ ] **Step 8: Write the failing test for the `update task` overhaul**

Add to `internal/app/update_test.go`:

```go
func TestUpdateTask_StatusPriorityAssigneeDueWaitingTags(t *testing.T) {
	env := newTestEnv(t) // match the actual helper name from the existing file, as in Step 4
	rc := app.Run([]string{"add", "task", "Sample", "--slug", "sample-task", "--work-dir", env.WorkDir})
	if rc != 0 {
		t.Fatalf("add task rc=%d", rc)
	}

	rc = app.Run([]string{"update", "task", "sample-task",
		"--status", "in-progress",
		"--priority", "high",
		"--assignee", "sam",
		"--due-date", "2026-12-25",
		"--waiting", "design review",
		"--tag", "urgent",
	})
	if rc != 0 {
		t.Fatalf("update task rc=%d", rc)
	}

	db := env.OpenDB(t)
	defer db.Close()
	task, err := flowdb.GetTask(db, "sample-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != "in-progress" {
		t.Errorf("Status = %q, want in-progress", task.Status)
	}
	if task.Priority != "high" {
		t.Errorf("Priority = %q, want high", task.Priority)
	}
	if !task.Assignee.Valid || task.Assignee.String != "sam" {
		t.Errorf("Assignee = %+v, want sam", task.Assignee)
	}
	if !task.DueDate.Valid || task.DueDate.String != "2026-12-25" {
		t.Errorf("DueDate = %+v, want 2026-12-25", task.DueDate)
	}
	if !task.WaitingOn.Valid || task.WaitingOn.String != "design review" {
		t.Errorf("WaitingOn = %+v, want 'design review'", task.WaitingOn)
	}
	tags, _ := flowdb.ListTagsForTask(db, "sample-task")
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("tags = %v, want [urgent]", tags)
	}

	// --clear-waiting, --clear-due, --clear-assignee, --remove-tag, --clear-tags
	rc = app.Run([]string{"update", "task", "sample-task",
		"--clear-waiting", "--clear-due", "--clear-assignee", "--remove-tag", "urgent",
	})
	if rc != 0 {
		t.Fatalf("update task (clears) rc=%d", rc)
	}
	task, _ = flowdb.GetTask(db, "sample-task")
	if task.WaitingOn.Valid {
		t.Errorf("WaitingOn should be cleared, got %+v", task.WaitingOn)
	}
	if task.DueDate.Valid {
		t.Errorf("DueDate should be cleared, got %+v", task.DueDate)
	}
	if task.Assignee.Valid {
		t.Errorf("Assignee should be cleared, got %+v", task.Assignee)
	}
	tags, _ = flowdb.ListTagsForTask(db, "sample-task")
	if len(tags) != 0 {
		t.Errorf("tags after --remove-tag urgent should be empty, got %v", tags)
	}
}

func TestUpdateTask_SessionIDFlagNoLongerExists(t *testing.T) {
	env := newTestEnv(t)
	rc := app.Run([]string{"add", "task", "Sample2", "--slug", "sample-task-2", "--work-dir", env.WorkDir})
	if rc != 0 {
		t.Fatalf("add task rc=%d", rc)
	}
	rc = app.Run([]string{"update", "task", "sample-task-2", "--session-id", "11111111-1111-4111-8111-111111111111"})
	if rc != 2 {
		t.Errorf("update task --session-id: rc=%d, want 2 (unknown flag, usage error)", rc)
	}
}

func TestUpdateTask_WaitingRejectsProjectRef(t *testing.T) {
	env := newTestEnv(t)
	rc := app.Run([]string{"add", "project", "Proj", "--slug", "proj", "--work-dir", env.WorkDir})
	if rc != 0 {
		t.Fatalf("add project rc=%d", rc)
	}
	rc = app.Run([]string{"update", "task", "proj", "--waiting", "someone"})
	if rc == 0 {
		t.Error("update task --waiting on a project ref should fail, got rc=0")
	}
}
```

- [ ] **Step 9: Run the tests to verify they fail**

Run: `go test ./internal/app/... -run TestUpdateTask -v`
Expected: FAIL — new flags undefined; `--session-id` still exists so the "no longer exists" test fails the wrong way (rc=0 instead of 2).

- [ ] **Step 10: Rewrite `cmdUpdateTask`**

Replace the body of `cmdUpdateTask` in `internal/app/update.go` with:

```go
func cmdUpdateTask(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: update task requires a task ref")
		return 2
	}
	ref := args[0]
	fs := flagSet("update task")
	workDir := fs.String("work-dir", "", "new absolute work directory")
	mkdir := fs.Bool("mkdir", false, "create --work-dir if it does not exist")
	status := fs.String("status", "", "backlog|in-progress|done")
	priority := fs.String("priority", "", "high|medium|low")
	assignee := fs.String("assignee", "", "person or agent to assign")
	clearAssignee := fs.Bool("clear-assignee", false, "remove the assignee")
	dueDate := fs.String("due-date", "", "due date (YYYY-MM-DD, today, tomorrow, monday, 3d)")
	clearDue := fs.Bool("clear-due", false, "remove the due date")
	waiting := fs.String("waiting", "", "who or what this task is waiting on")
	clearWaiting := fs.Bool("clear-waiting", false, "clear waiting_on")
	var addTags, removeTags stringSliceFlag
	fs.Var(&addTags, "tag", "tag to add (repeatable)")
	fs.Var(&removeTags, "remove-tag", "tag to remove (repeatable)")
	clearTags := fs.Bool("clear-tags", false, "remove all tags")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *status != "" {
		switch *status {
		case "backlog", "in-progress", "done":
		default:
			fmt.Fprintf(os.Stderr, "error: --status must be backlog|in-progress|done, got %q\n", *status)
			return 2
		}
	}
	if *priority != "" && !isValidPriority(*priority) {
		fmt.Fprintf(os.Stderr, "error: --priority must be high|medium|low, got %q\n", *priority)
		return 2
	}
	if *assignee != "" && *clearAssignee {
		fmt.Fprintln(os.Stderr, "error: --assignee and --clear-assignee are mutually exclusive")
		return 2
	}
	if *dueDate != "" && *clearDue {
		fmt.Fprintln(os.Stderr, "error: --due-date and --clear-due are mutually exclusive")
		return 2
	}
	if *waiting != "" && *clearWaiting {
		fmt.Fprintln(os.Stderr, "error: --waiting and --clear-waiting are mutually exclusive")
		return 2
	}

	var absWorkDir string
	if *workDir != "" {
		abs, err := resolveWorkDir(*workDir, *mkdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		absWorkDir = abs
	}

	var parsedDue string
	if *dueDate != "" {
		d, err := parseDueDate(*dueDate, timeNow())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --due-date: %v\n", err)
			return 2
		}
		parsedDue = d.Format("2006-01-02")
	}

	if *workDir == "" && *status == "" && *priority == "" && *assignee == "" && !*clearAssignee &&
		*dueDate == "" && !*clearDue && *waiting == "" && !*clearWaiting &&
		len(addTags) == 0 && len(removeTags) == 0 && !*clearTags {
		fmt.Fprintln(os.Stderr, "error: give at least one field to update")
		return 2
	}

	dbPath, err := flowDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := flowdb.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	// --waiting/--clear-waiting is tasks-only; give a clear error on a
	// project ref rather than silently no-op'ing (mirrors the old
	// cmdWaiting behavior via resolveTaskForWaiting).
	if (*waiting != "" || *clearWaiting) && strings.HasPrefix(ref, "project/") {
		fmt.Fprintf(os.Stderr, "error: waiting is valid on tasks only, got project ref %q\n", ref)
		return 1
	}

	task, err := ResolveTask(db, strings.TrimPrefix(ref, "task/"), true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if (*waiting != "" || *clearWaiting) {
				if p, pErr := ResolveProject(db, ref, true); pErr == nil {
					fmt.Fprintf(os.Stderr, "error: waiting is valid on tasks only, %q is a project\n", p.Slug)
					return 1
				}
			}
			fmt.Fprintf(os.Stderr, "error: task %q not found\n", ref)
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	now := flowdb.NowISO()
	if absWorkDir != "" {
		if _, err := db.Exec(`UPDATE tasks SET work_dir=?, updated_at=? WHERE slug=?`, absWorkDir, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update work_dir: %v\n", err)
			return 1
		}
		fmt.Printf("work_dir → %s\n", absWorkDir)
	}
	if *status != "" {
		if _, err := db.Exec(`UPDATE tasks SET status=?, updated_at=? WHERE slug=?`, *status, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update status: %v\n", err)
			return 1
		}
		fmt.Printf("status → %s\n", *status)
	}
	if *priority != "" {
		if _, err := db.Exec(`UPDATE tasks SET priority=?, updated_at=? WHERE slug=?`, *priority, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update priority: %v\n", err)
			return 1
		}
		fmt.Printf("priority → %s\n", *priority)
	}
	if *assignee != "" {
		if _, err := db.Exec(`UPDATE tasks SET assignee=?, updated_at=? WHERE slug=?`, *assignee, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update assignee: %v\n", err)
			return 1
		}
		fmt.Printf("assignee → %s\n", *assignee)
	}
	if *clearAssignee {
		if _, err := db.Exec(`UPDATE tasks SET assignee=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear assignee: %v\n", err)
			return 1
		}
		fmt.Println("assignee cleared")
	}
	if parsedDue != "" {
		if _, err := db.Exec(`UPDATE tasks SET due_date=?, updated_at=? WHERE slug=?`, parsedDue, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update due_date: %v\n", err)
			return 1
		}
		fmt.Printf("due_date → %s\n", parsedDue)
	}
	if *clearDue {
		if _, err := db.Exec(`UPDATE tasks SET due_date=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear due_date: %v\n", err)
			return 1
		}
		fmt.Println("due_date cleared")
	}
	if *waiting != "" {
		if _, err := db.Exec(`UPDATE tasks SET waiting_on=?, updated_at=? WHERE slug=?`, *waiting, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: update waiting_on: %v\n", err)
			return 1
		}
		fmt.Printf("waiting_on → %s\n", *waiting)
	}
	if *clearWaiting {
		if _, err := db.Exec(`UPDATE tasks SET waiting_on=NULL, updated_at=? WHERE slug=?`, now, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: clear waiting_on: %v\n", err)
			return 1
		}
		fmt.Println("waiting_on cleared")
	}
	for _, tag := range addTags {
		if err := flowdb.AddTag(db, task.Slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	for _, tag := range removeTags {
		if err := flowdb.RemoveTag(db, task.Slug, tag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	if *clearTags {
		if err := flowdb.ClearTags(db, task.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	return 0
}
```

Add `"strings"` to `update.go`'s imports (for `strings.HasPrefix`/`TrimPrefix`) if not already present, and remove the now-unused `sessionUUIDRe` variable and its `"regexp"` import.

Add a tiny `timeNow` indirection to `internal/app/helpers.go` if the codebase doesn't already have one for testability:

```go
// timeNow is time.Now, indirected for test control where needed.
var timeNow = time.Now
```

(Check `internal/app/due.go`'s old code and `internal/app/add.go`: both called `parseDueDate(*dueFlag, time.Now())` directly. If no existing test overrides a `timeNow` var elsewhere in the codebase, it's simplest to just call `time.Now()` directly in `cmdUpdateTask` instead of introducing `timeNow` — check `internal/app/dateparse_test.go` after Step 1's rename: if its tests already pass a fixed `now` directly to `parseDueDate` rather than relying on a package var, skip adding `timeNow` and use `time.Now()` inline here to match existing convention.)

- [ ] **Step 11: Run the tests to verify they pass**

Run: `go test ./internal/app/... -run TestUpdateTask -v`
Expected: PASS.

- [ ] **Step 12: Fix ripple effects from removing `due`/`priority`/`waiting`**

Search the codebase for any remaining callers of the removed commands/functions:

Run: `grep -rn "cmdDue\|cmdPriority\|cmdWaiting\|resolveTaskForWaiting\|sessionUUIDRe" internal/ --include=*.go`
Expected: no matches outside of test files being deleted/renamed in this task. If `internal/app/e2e_test.go` or the flow skill's `SKILL.md` embed mentions `focusflow due`/`focusflow priority`/`focusflow waiting`, update those references too (check `internal/app/skill/SKILL.md` for any usage examples referencing these commands and update them to the `update task` equivalents).

Also check `internal/app/update_test.go`'s pre-existing tests (before this task's additions) for any that exercised the now-removed `--session-id` flag directly — delete/replace them, since Step 8 already added `TestUpdateTask_SessionIDFlagNoLongerExists` to cover the removal explicitly.

Run: `go build ./... && go test ./...`
Expected: PASS, no leftover references.

- [ ] **Step 13: Commit**

```bash
git add internal/app/add.go internal/app/update.go internal/app/app.go internal/app/helpers.go internal/app/dateparse.go internal/app/dateparse_test.go internal/app/add_test.go internal/app/update_test.go
git rm internal/app/due.go internal/app/priority.go internal/app/waiting.go internal/app/priority_test.go internal/app/waiting_test.go internal/app/due_test.go
git commit -m "feat(app): overhaul update task to match flow's flag surface; remove due/priority/waiting/--session-id"
```

## Task 3: `list tasks --format table|json|tsv --tag --no-color --no-truncate` + `list tags`

**Files:**
- Modify: `internal/app/list.go`, `internal/app/app.go` (no change needed — `list` already dispatches through `cmdList`)
- Create: `internal/app/liveness.go`
- Test: `internal/app/list_test.go`, `internal/app/liveness_test.go`

**Interfaces:**
- Consumes: `flowdb.TaskFilter.Tags`, `flowdb.ListAllTags`, `flowdb.ListTagsForTask` (Task 1); `Task.AutoRunStatus`, `Task.AutoRunPID` (Task 1).
- Produces: `isSessionLive(sessionID string) bool` (in `liveness.go`, backed by `var psLines = defaultPSLines`); `taskJSON` struct (exported field names via json tags, NOT the Go struct field names, matching flow's exact lowercase keys); `cmdList` gains a `"tags"` case calling `listTagsCmd`.

- [ ] **Step 1: Write the failing liveness test**

Create `internal/app/liveness_test.go`:

```go
package app

import "testing"

func TestIsSessionLive(t *testing.T) {
	orig := psLines
	defer func() { psLines = orig }()

	psLines = func() ([]string, error) {
		return []string{
			"claude --session-id 11111111-1111-4111-8111-111111111111 some prompt text",
			"claude --resume 22222222-2222-4222-8222-222222222222",
			"/bin/bash -c something",
		}, nil
	}

	if !isSessionLive("11111111-1111-4111-8111-111111111111") {
		t.Error("expected --session-id match to be live")
	}
	if !isSessionLive("22222222-2222-4222-8222-222222222222") {
		t.Error("expected --resume match to be live")
	}
	if isSessionLive("33333333-3333-4333-8333-333333333333") {
		t.Error("expected no match to be not live")
	}
	if isSessionLive("") {
		t.Error("expected empty session id to never be live")
	}
}

func TestIsSessionLive_PSErrorTreatedAsNotLive(t *testing.T) {
	orig := psLines
	defer func() { psLines = orig }()
	psLines = func() ([]string, error) { return nil, errBoom }
	if isSessionLive("anything") {
		t.Error("expected ps error to be treated as not-live, not panic or true")
	}
}
```

Add a package-level `var errBoom = errors.New("boom")` near the top of `liveness_test.go` (with `"errors"` imported), or inline `fmt.Errorf("boom")` instead — either is fine, pick one and use it consistently.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app/... -run TestIsSessionLive -v`
Expected: FAIL — `psLines`/`isSessionLive` undefined.

- [ ] **Step 3: Implement `liveness.go`**

```go
package app

import (
	"os/exec"
	"strings"
)

// psLines returns the command line of every running process, one per
// line. Indirected as a package var so tests can substitute canned
// output without touching the real process table — same mocking
// pattern as iterm.Runner.
var psLines = defaultPSLines

func defaultPSLines() ([]string, error) {
	out, err := exec.Command("ps", "-axo", "command=").Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

// isSessionLive reports whether some running process's command line
// shows it is actively serving sessionID — i.e. a `claude --session-id
// <id>` (fresh bootstrap) or `claude --resume <id>` (resumed) process.
// A ps failure is treated as "not live" rather than propagated, since
// liveness is a best-effort display hint, not a correctness-critical
// value.
func isSessionLive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	lines, err := psLines()
	if err != nil {
		return false
	}
	needleSessionID := "--session-id " + sessionID
	needleResume := "--resume " + sessionID
	for _, line := range lines {
		if strings.Contains(line, needleSessionID) || strings.Contains(line, needleResume) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/app/... -run TestIsSessionLive -v`
Expected: PASS.

- [ ] **Step 5: Write the failing test for `--tag`/`--no-truncate` filtering and table rendering**

Add to `internal/app/list_test.go`:

```go
func TestListTasks_TagFilter(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "A", "--slug", "task-a", "--work-dir", env.WorkDir, "--tag", "urgent"})
	app.Run([]string{"add", "task", "B", "--slug", "task-b", "--work-dir", env.WorkDir, "--tag", "backend"})

	out := captureStdout(t, func() {
		rc := app.Run([]string{"list", "tasks", "--tag", "urgent"})
		if rc != 0 {
			t.Fatalf("list tasks --tag urgent: rc=%d", rc)
		}
	})
	if !strings.Contains(out, "task-a") {
		t.Errorf("expected task-a in output, got:\n%s", out)
	}
	if strings.Contains(out, "task-b") {
		t.Errorf("expected task-b to be filtered out, got:\n%s", out)
	}
}

func TestListTasks_NoTruncateShowsFullLongSlug(t *testing.T) {
	env := newTestEnv(t)
	longSlug := "a-very-long-task-slug-that-exceeds-forty-characters-easily"
	app.Run([]string{"add", "task", "Long", "--slug", longSlug, "--work-dir", env.WorkDir})

	truncated := captureStdout(t, func() {
		app.Run([]string{"list", "tasks"})
	})
	if strings.Contains(truncated, longSlug) {
		t.Errorf("expected default output to truncate slug, but found the full slug:\n%s", truncated)
	}

	full := captureStdout(t, func() {
		app.Run([]string{"list", "tasks", "--no-truncate"})
	})
	if !strings.Contains(full, longSlug) {
		t.Errorf("expected --no-truncate output to contain the full slug, got:\n%s", full)
	}
}
```

Check `internal/app/list_test.go` or `internal/app/testhelpers_test.go` for an existing `captureStdout`-style helper (this codebase's tests must already capture CLI stdout somewhere, e.g. for `list tasks` assertions elsewhere) — reuse it. If none exists, add one to `internal/app/testhelpers_test.go`:

```go
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy captured stdout: %v", err)
	}
	return buf.String()
}
```

(Add `"bytes"`, `"io"` imports if introducing this helper.)

- [ ] **Step 6: Run the tests to verify they fail**

Run: `go test ./internal/app/... -run 'TestListTasks_TagFilter|TestListTasks_NoTruncateShowsFullLongSlug' -v`
Expected: FAIL — `flag provided but not defined: -tag` / `-no-truncate`.

- [ ] **Step 7: Write the failing test for `--format json` exact schema**

Add to `internal/app/list_test.go`:

```go
func TestListTasks_FormatJSON_ExactFieldSet(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "project", "Proj", "--slug", "proj", "--work-dir", env.WorkDir})
	app.Run([]string{"add", "task", "A", "--slug", "task-a", "--project", "proj", "--work-dir", env.WorkDir, "--tag", "urgent"})

	out := captureStdout(t, func() {
		rc := app.Run([]string{"list", "tasks", "--format", "json", "--include-done", "--include-archived", "--kind", "all"})
		if rc != 0 {
			t.Fatalf("list tasks --format json: rc=%d", rc)
		}
	})

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput was:\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]

	wantKeys := []string{"slug", "name", "status", "priority", "project", "live", "updated", "tags", "archived", "auto_run", "age_days"}
	if len(row) != len(wantKeys) {
		t.Errorf("row has %d keys, want %d: got keys %v", len(row), len(wantKeys), keysOf(row))
	}
	for _, k := range wantKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("missing expected key %q in row: %v", k, row)
		}
	}
	if _, hasKind := row["kind"]; hasKind {
		t.Error("row must NOT have a 'kind' key — live flow never emits one, even under --kind all")
	}
	if row["slug"] != "task-a" {
		t.Errorf("slug = %v, want task-a", row["slug"])
	}
	if row["project"] != "proj" {
		t.Errorf("project = %v, want proj", row["project"])
	}
	tags, ok := row["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "urgent" {
		t.Errorf("tags = %v, want [urgent]", row["tags"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

Add `"encoding/json"` to `list_test.go`'s imports.

- [ ] **Step 8: Run the test to verify it fails**

Run: `go test ./internal/app/... -run TestListTasks_FormatJSON_ExactFieldSet -v`
Expected: FAIL — `flag provided but not defined: -format`.

- [ ] **Step 9: Implement `--format`/`--tag`/`--no-color`/`--no-truncate` in `listTasksCmd`, plus `listTagsCmd`**

In `internal/app/list.go`, add to `listTasksCmd`'s flag block:

```go
	format := fs.String("format", "table", "table|json|tsv")
	var tagFilter stringSliceFlag
	fs.Var(&tagFilter, "tag", "filter to tasks having this tag (repeatable; AND semantics)")
	noColor := fs.Bool("no-color", false, "disable ANSI color/emoji markers in table output")
	noTruncate := fs.Bool("no-truncate", false, "do not truncate long identifiers in table output")
```

Wire `tagFilter` into `filter.Tags = []string(tagFilter)` alongside the existing filter construction.

Validate `*format` right after `fs.Parse`:

```go
	switch *format {
	case "table", "json", "tsv":
	default:
		fmt.Fprintf(os.Stderr, "error: --format must be table|json|tsv, got %q\n", *format)
		return 2
	}
```

After fetching `tasks` from `flowdb.ListTasks`, branch on format before the existing table-rendering code:

```go
	if *format == "json" {
		return renderTasksJSON(tasks, db)
	}
	if *format == "tsv" {
		return renderTasksTSV(tasks, db, root)
	}
```

(`root` is already computed earlier in the function for staleness checks — reuse it.)

Add the JSON/TSV renderers and row-building helper near the bottom of `list.go`:

```go
// taskRowCommon computes the fields shared by JSON and TSV rendering
// that require extra DB/process lookups beyond what's on the Task
// struct itself: tags and liveness.
type taskRowCommon struct {
	tags []string
	live bool
}

func buildTaskRowCommon(db *sql.DB, t *flowdb.Task) (taskRowCommon, error) {
	tags, err := flowdb.ListTagsForTask(db, t.Slug)
	if err != nil {
		return taskRowCommon{}, err
	}
	live := t.SessionID.Valid && isSessionLive(t.SessionID.String)
	return taskRowCommon{tags: tags, live: live}, nil
}

// autoRunDisplay renders the auto_run column exactly as flow does:
// "" when never run, otherwise one of running|completed|dead.
func autoRunDisplay(t *flowdb.Task) string {
	if !t.AutoRunStatus.Valid {
		return ""
	}
	return t.AutoRunStatus.String
}

func renderTasksJSON(tasks []*flowdb.Task, db *sql.DB) int {
	type jsonRow struct {
		Slug     string   `json:"slug"`
		Name     string   `json:"name"`
		Status   string   `json:"status"`
		Priority string   `json:"priority"`
		Project  string   `json:"project"`
		Live     bool     `json:"live"`
		Updated  string   `json:"updated"`
		Tags     []string `json:"tags"`
		Archived bool     `json:"archived"`
		AutoRun  string   `json:"auto_run"`
		AgeDays  int      `json:"age_days"`
	}
	now := time.Now()
	rows := make([]jsonRow, 0, len(tasks))
	for _, t := range tasks {
		common, err := buildTaskRowCommon(db, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		project := ""
		if t.ProjectSlug.Valid {
			project = t.ProjectSlug.String
		}
		tags := common.tags
		if tags == nil {
			tags = []string{}
		}
		rows = append(rows, jsonRow{
			Slug: t.Slug, Name: t.Name, Status: t.Status, Priority: t.Priority,
			Project: project, Live: common.live, Updated: t.UpdatedAt, Tags: tags,
			Archived: t.ArchivedAt.Valid, AutoRun: autoRunDisplay(t),
			AgeDays: daysInStatus(t, now),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode json: %v\n", err)
		return 1
	}
	return 0
}

func renderTasksTSV(tasks []*flowdb.Task, db *sql.DB, root string) int {
	w := os.Stdout
	fmt.Fprintln(w, strings.Join([]string{
		"slug", "status", "priority", "project", "age_days", "due_in_days", "due_label",
		"stale", "stale_days", "waiting_on", "assignee", "live", "auto_run", "auto_run_pid",
		"archived", "tags",
	}, "\t"))
	now := time.Now()
	for _, t := range tasks {
		common, err := buildTaskRowCommon(db, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		project := ""
		if t.ProjectSlug.Valid {
			project = t.ProjectSlug.String
		}
		dueInDays, dueLabel := "", ""
		if diff, ok := daysUntilDue(t, now); ok {
			dueInDays = fmt.Sprintf("%d", diff)
			switch {
			case diff < 0:
				dueLabel = "overdue"
			case diff == 0:
				dueLabel = "due today"
			default:
				dueLabel = "due"
			}
		}
		stale, staleDays := "false", ""
		if t.Status == "in-progress" && !t.ArchivedAt.Valid {
			if days, ok := taskStaleness(t, root); ok {
				stale = "true"
				staleDays = fmt.Sprintf("%d", days)
			}
		}
		waitingOn := ""
		if t.WaitingOn.Valid {
			waitingOn = t.WaitingOn.String
		}
		assignee := ""
		if t.Assignee.Valid {
			assignee = t.Assignee.String
		}
		pid := ""
		if t.AutoRunPID.Valid {
			pid = fmt.Sprintf("%d", t.AutoRunPID.Int64)
		}
		fmt.Fprintln(w, strings.Join([]string{
			t.Slug, t.Status, t.Priority, project,
			fmt.Sprintf("%d", daysInStatus(t, now)), dueInDays, dueLabel,
			stale, staleDays, waitingOn, assignee,
			fmt.Sprintf("%t", common.live), autoRunDisplay(t), pid,
			fmt.Sprintf("%t", t.ArchivedAt.Valid), strings.Join(common.tags, ","),
		}, "\t"))
	}
	return 0
}
```

Add `"encoding/json"` to `list.go`'s imports.

Wire `--no-truncate` into the existing table renderer: find the `identW` clamp (`if identW > 40 { identW = 40 }`) and guard it:

```go
	if !*noTruncate && identW > 40 {
		identW = 40
	}
```

And the truncation line just below (`if len(ident) > identW { ident = ident[:identW] }`) — guard the same way:

```go
	if !*noTruncate && len(ident) > identW {
		ident = ident[:identW]
	}
```

`--no-color` in the current implementation has nothing to strip (no ANSI codes or color are emitted today — only `⚠`/`⚡` emoji markers). Treat `--no-color` as stripping those emoji markers specifically (the closest existing "color-like" decoration): wrap the `r.due`/`r.stale` construction so the emoji prefix is omitted when `*noColor` is true — e.g. change `"⚠ overdue %dd"` to a `noColor`-conditional format string picked once before the loop:

```go
	warnPrefix, boltPrefix := "⚠ ", "⚡ "
	if *noColor {
		warnPrefix, boltPrefix = "", ""
	}
```

and use `warnPrefix`/`boltPrefix` in place of the literal `"⚠ "`/`"⚡ "` in the `r.due`/`r.stale` assignments.

Add `listTagsCmd` and wire it into `cmdList`:

```go
	case "tags":
		return listTagsCmd(args[1:])
```

```go
func listTagsCmd(args []string) int {
	fs := flagSet("list tags")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dbPath, err := flowDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := flowdb.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	tags, err := flowdb.ListAllTags(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(tags) == 0 {
		fmt.Println("(no tags)")
		return 0
	}
	for _, tc := range tags {
		fmt.Printf("  #%-20s %d task(s)\n", tc.Tag, tc.Count)
	}
	return 0
}
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `go test ./internal/app/... -run 'TestListTasks' -v`
Expected: PASS.

- [ ] **Step 11: Repeat Steps 5-10's pattern minimally for `listProjectsCmd` and `listPlaybooksCmd`'s `--format`/`--no-color`/`--no-truncate`**

Add `--format table|json` (projects/playbooks have no TSV sample confirmed live — support `table`/`json` only, reject `tsv` with a clear "not supported for this list" error so the flag surface still matches flow's `--help` even if TSV's exact schema for these two is unverified — flag this as UNVERIFIED in the final report, not guessed) and `--no-color`/`--no-truncate` to both `listProjectsCmd` and `listPlaybooksCmd`, following exactly the same flag-declaration and JSON-rendering pattern as `listTasksCmd` (own small `jsonRow` struct per function, using the fields each command's existing table output already prints: for projects — `slug, name, status, priority, work_dir, task_counts, archived`; for playbooks — `slug, name, project, work_dir, archived`). Add corresponding tests in `list_test.go` (`TestListProjects_FormatJSON`, `TestListPlaybooks_FormatJSON`, and a `--no-truncate` test per command mirroring `TestListTasks_NoTruncateShowsFullLongSlug`) before implementing, per TDD.

- [ ] **Step 12: Run the full package test suite**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add internal/app/list.go internal/app/liveness.go internal/app/list_test.go internal/app/liveness_test.go internal/app/testhelpers_test.go
git commit -m "feat(app): add --format/--tag/--no-color/--no-truncate to list commands, list tags, and process-based liveness detection"
```

## Task 4: `do` — add `--harness`, `--with`, `--with-file`, `--force`, `--here`

**Files:**
- Modify: `internal/app/do.go`
- Test: `internal/app/do_test.go`

**Interfaces:**
- Consumes: `Task.Harness` (Task 1).
- Produces: `cmdDo` accepts `--harness claude|codex`, `--with <instruction>`, `--with-file <path>`, `--force`, `--here`; `--here` binds the current process's session id (`$CLAUDE_CODE_SESSION_ID`/`$CODEX_THREAD_ID`) to the task without spawning a tab.

- [ ] **Step 1: Write the failing test for `--here`**

Add to `internal/app/do_test.go`:

```go
func TestDo_Here_BindsCurrentSessionWithoutSpawningTab(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "Bindable", "--slug", "bindable", "--work-dir", env.WorkDir})

	spawnCalled := false
	origSpawn := iterm.Runner // check the actual exported mock hook name in internal/iterm/iterm.go and internal/app/do_test.go's existing setup — reuse it exactly
	iterm.Runner = func(title, cwd, command string, env map[string]string) error {
		spawnCalled = true
		return nil
	}
	defer func() { iterm.Runner = origSpawn }()

	t.Setenv("CLAUDE_CODE_SESSION_ID", "44444444-4444-4444-8444-444444444444")
	rc := app.Run([]string{"do", "--here", "bindable"})
	if rc != 0 {
		t.Fatalf("do --here rc=%d", rc)
	}
	if spawnCalled {
		t.Error("do --here must not spawn an iTerm tab")
	}

	db := env.OpenDB(t)
	defer db.Close()
	task, err := flowdb.GetTask(db, "bindable")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !task.SessionID.Valid || task.SessionID.String != "44444444-4444-4444-8444-444444444444" {
		t.Errorf("SessionID = %+v, want the CLAUDE_CODE_SESSION_ID value", task.SessionID)
	}
	if task.Status != "in-progress" {
		t.Errorf("Status = %q, want in-progress", task.Status)
	}
}

func TestDo_Here_RejectsAlreadyBoundTask(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "Already", "--slug", "already-bound", "--work-dir", env.WorkDir})
	t.Setenv("CLAUDE_CODE_SESSION_ID", "55555555-5555-4555-8555-555555555555")
	if rc := app.Run([]string{"do", "--here", "already-bound"}); rc != 0 {
		t.Fatalf("first do --here rc=%d", rc)
	}

	app.Run([]string{"add", "task", "Second", "--slug", "second-task", "--work-dir", env.WorkDir})
	rc := app.Run([]string{"do", "--here", "second-task"})
	if rc == 0 {
		t.Error("do --here on a second task from the same process should fail (session_id already bound elsewhere)")
	}
}
```

Check `internal/app/do_test.go`'s existing tests first for the exact iTerm mock variable name and env-var-setting pattern already in use (e.g. it likely already mocks `iterm.SpawnTab` or similar for the existing bootstrap tests) — match that exactly rather than the illustrative names above.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/... -run TestDo_Here -v`
Expected: FAIL — `flag provided but not defined: -here`.

- [ ] **Step 3: Implement `--here`, `--force`, `--harness`, `--with`, `--with-file`**

In `cmdDo`, add flags:

```go
	here := fs.Bool("here", false, "bind the current harness session to this task instead of spawning a tab")
	force := fs.Bool("force", false, "override the live-session guard")
	harness := fs.String("harness", "", "claude|codex (first-session choice only)")
	with := fs.String("with", "", "one-off instruction to include in the session's first/next message")
	withFile := fs.String("with-file", "", "path to a file whose contents are used as --with")
```

Validate near the top, after `fs.Parse`:

```go
	if *with != "" && *withFile != "" {
		fmt.Fprintln(os.Stderr, "error: --with and --with-file are mutually exclusive")
		return 2
	}
	if *harness != "" && *harness != "claude" && *harness != "codex" {
		fmt.Fprintf(os.Stderr, "error: --harness must be claude or codex, got %q\n", *harness)
		return 2
	}
	var withText string
	if *with != "" {
		withText = *with
	} else if *withFile != "" {
		b, err := os.ReadFile(*withFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --with-file: %v\n", err)
			return 1
		}
		withText = string(b)
	}
```

For `--here`: short-circuit before any of the existing tab-spawning logic, right after `findTask` resolves `task` (immediately after the `task, rc := findTask(db, query)` block):

```go
	if *here {
		return doHere(db, task, *force)
	}
```

Add `doHere` near the bottom of `do.go`:

```go
// doHere binds the current process's harness session id to task without
// spawning a tab. The env var checked mirrors flow's own detection order
// (§10 of the flow skill): Claude Code first, then Codex.
func doHere(db *sql.DB, task *flowdb.Task, force bool) int {
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	harness := "claude"
	if sessionID == "" {
		sessionID = os.Getenv("CODEX_THREAD_ID")
		harness = "codex"
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "error: --here requires a harness session id in the environment (CLAUDE_CODE_SESSION_ID or CODEX_THREAD_ID)")
		return 1
	}

	now := flowdb.NowISO()
	res, err := db.Exec(
		`UPDATE tasks SET status='in-progress',
		 status_changed_at = CASE WHEN status != 'in-progress' THEN ? ELSE status_changed_at END,
		 session_id=?, session_started=?, harness=?, updated_at=?
		 WHERE slug=? AND status IN ('backlog','in-progress') AND (session_id IS NULL OR session_id = ?)`,
		now, sessionID, now, harness, now, task.Slug, sessionID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			fmt.Fprintf(os.Stderr, "error: this session is already bound to another task; a session can only be bound to one task\n")
			return 1
		}
		fmt.Fprintf(os.Stderr, "error: bind session: %v\n", err)
		return 1
	}
	n, err := res.RowsAffected()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if n == 0 {
		fmt.Fprintf(os.Stderr, "error: task %q could not be bound (already done, or already bound to a different session — pass --force is not supported for rebinding; see the flow skill's binding invariants)\n", task.Slug)
		return 1
	}
	fmt.Printf("Bound %s to this session (%s)\n", task.Slug, sessionID)
	return 0
}
```

Add `"strings"` to `do.go`'s imports if not already present.

For `--harness` on first bootstrap: store it alongside the existing `sessionID` assignment inside the `needsBootstrap` branch's `UPDATE` statement — add `harness=?` to that `UPDATE tasks SET ...` and pass a resolved harness value (`*harness` if given, else `"claude"` — focusflow only actually spawns Claude Code today per existing `do.go` code, so `codex` selection can be accepted as a valid flag value without a real Codex-spawn implementation; note this explicitly as a partial-parity item, not a full Codex integration, since that's outside this plan's scope).

For `--with`/`--with-file`: append `withText` to the bootstrap prompt when present (fresh bootstrap path) or as an additional shell-quoted argument to `claude --resume <id>` (resume path) — find the `command = fmt.Sprintf("claude --session-id %s %s", ...)` and `command = "claude --resume " + sessionID` lines and extend both:

```go
	if withText != "" {
		if needsBootstrap {
			prompt = prompt + "\n\n" + withText
			command = fmt.Sprintf("claude --session-id %s %s", sessionID, iterm.ShellQuote(prompt))
		} else {
			command = "claude --resume " + sessionID + " " + iterm.ShellQuote(withText)
		}
	}
```

(Place this block after `command` is first assigned in both branches, before the `if *dangerSkip` append.)

For `--force`: thread it into `doHere`'s signature (already accepted above) — when `force` is true, allow rebinding even if `session_id` is already set to something else, by relaxing the `WHERE` clause's `AND (session_id IS NULL OR session_id = ?)` to be skipped entirely when force is true (two separate SQL strings, chosen by an `if force { ... } else { ... }` before the `db.Exec` call, since `database/sql` can't conditionally include a WHERE clause fragment via parameter alone without building the query string dynamically — build it with a small local `query` string variable).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/... -run 'TestDo' -v`
Expected: PASS.

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/do.go internal/app/do_test.go
git commit -m "feat(app): add do --here/--force/--harness/--with/--with-file"
```

## Task 5: `do --auto` — real headless background runner

**Files:**
- Modify: `internal/app/do.go`
- Test: `internal/app/do_test.go`

**Interfaces:**
- Consumes: `Task.AutoRunStatus/PID/Started/Finished/Log` (Task 1), `isSessionLive` (Task 3, reused conceptually — `--auto` liveness is PID-based, not `ps`-substring-based, since we control the PID directly).
- Produces: `cmdDo` accepts `--auto`; spawns `claude` as a detached background process instead of an iTerm tab; writes a log to `<taskDir>/auto-runs/<timestamp>.log`; sets `auto_run_status='running'` + `auto_run_pid` + `auto_run_started` synchronously before returning; a new `refreshAutoRunStatus(db *sql.DB, task *flowdb.Task) error` helper that flips a stale `'running'` row to `'dead'` when its pid is no longer alive (called from `list`/`show` paths in a later task, and exercised directly here).

- [ ] **Step 1: Write the failing test for `--auto` spawning**

Add to `internal/app/do_test.go`:

```go
func TestDo_Auto_SpawnsDetachedProcessAndRecordsPID(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "Auto", "--slug", "auto-task", "--work-dir", env.WorkDir})

	origExecCommand := execCommand // package var wrapping exec.Command in do.go, introduced in Step 3 below
	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string{name}, args...)
		// Use a real, near-instant command so Start()/Wait() behave like a
		// real (if trivial) process without depending on `claude` existing
		// in the test environment.
		return exec.Command("true")
	}
	defer func() { execCommand = origExecCommand }()

	rc := app.Run([]string{"do", "--auto", "auto-task"})
	if rc != 0 {
		t.Fatalf("do --auto rc=%d", rc)
	}
	if len(capturedArgs) == 0 || capturedArgs[0] != "claude" {
		t.Errorf("expected exec of claude, got %v", capturedArgs)
	}
	found := false
	for _, a := range capturedArgs {
		if a == "--dangerously-skip-permissions" {
			found = true
		}
	}
	if !found {
		t.Error("--auto must imply --dangerously-skip-permissions")
	}

	db := env.OpenDB(t)
	defer db.Close()
	task, err := flowdb.GetTask(db, "auto-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !task.AutoRunStatus.Valid || task.AutoRunStatus.String != "running" {
		t.Errorf("AutoRunStatus = %+v, want running", task.AutoRunStatus)
	}
	if !task.AutoRunPID.Valid || task.AutoRunPID.Int64 == 0 {
		t.Errorf("AutoRunPID = %+v, want a nonzero pid", task.AutoRunPID)
	}
}

func TestDo_Auto_CannotCombineWithHere(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "Both", "--slug", "both-flags", "--work-dir", env.WorkDir})
	rc := app.Run([]string{"do", "--auto", "--here", "both-flags"})
	if rc != 2 {
		t.Errorf("do --auto --here: rc=%d, want 2 (usage error)", rc)
	}
}

func TestRefreshAutoRunStatus_DetectsDeadPID(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "task", "WillDie", "--slug", "will-die", "--work-dir", env.WorkDir})

	db := env.OpenDB(t)
	defer db.Close()

	// A pid that is guaranteed not to be running: spawn `true`, wait for
	// it to exit, then use its now-dead pid.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start throwaway process: %v", err)
	}
	deadPID := cmd.Process.Pid
	cmd.Wait()

	now := flowdb.NowISO()
	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=?, auto_run_started=? WHERE slug='will-die'`,
		deadPID, now,
	); err != nil {
		t.Fatalf("seed running auto_run: %v", err)
	}

	task, err := flowdb.GetTask(db, "will-die")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if err := refreshAutoRunStatus(db, task); err != nil {
		t.Fatalf("refreshAutoRunStatus: %v", err)
	}

	task, _ = flowdb.GetTask(db, "will-die")
	if task.AutoRunStatus.String != "dead" {
		t.Errorf("AutoRunStatus = %q, want dead (pid %d is no longer alive)", task.AutoRunStatus.String, deadPID)
	}
}
```

Add `"os/exec"` to `do_test.go`'s imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/... -run 'TestDo_Auto|TestRefreshAutoRunStatus' -v`
Expected: FAIL — `flag provided but not defined: -auto`, `execCommand`/`refreshAutoRunStatus` undefined.

- [ ] **Step 3: Implement `--auto`**

In `internal/app/do.go`, add near the top (package-level, for test mocking):

```go
// execCommand wraps exec.Command so tests can substitute a fake process
// without depending on the real `claude` binary existing in the test
// environment. Matches the iterm.Runner mocking pattern.
var execCommand = exec.Command
```

Add `"os/exec"` and `"syscall"` to `do.go`'s imports.

Add the `--auto` flag to `cmdDo`:

```go
	auto := fs.Bool("auto", false, "run headlessly in the background; implies --dangerously-skip-permissions")
```

Validate right after the `--with`/`--with-file` mutual-exclusion check:

```go
	if *auto && *here {
		fmt.Fprintln(os.Stderr, "error: --auto and --here are mutually exclusive")
		return 2
	}
	if *auto {
		*dangerSkip = true
	}
```

After the existing status-flip transaction commits (right after `committed = true`, before the `if *fresh && curSessionID.Valid` block — this is the same point where the tab-spawning logic currently begins), branch:

```go
	if *auto {
		return spawnHeadless(db, task, sessionID, needsBootstrap, extraClaudeArgs, withText)
	}
```

Add `spawnHeadless` and `refreshAutoRunStatus` near the bottom of `do.go`:

```go
// spawnHeadless launches claude as a detached background process for
// `do --auto`. Unlike the interactive tab path, this never blocks: it
// starts the process, records its pid, and returns immediately. The
// process is expected to call `focusflow done` on itself when finished
// (per the flow skill's autonomous-mode contract) — spawnHeadless does
// not wait for that; it only tracks whether the pid is still alive via
// refreshAutoRunStatus, called lazily from list/show.
func spawnHeadless(db *sql.DB, task *flowdb.Task, sessionID string, needsBootstrap bool, extraArgs []string, withText string) int {
	root, err := flowRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	logDir := filepath.Join(root, "tasks", task.Slug, "auto-runs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: mkdir %s: %v\n", logDir, err)
		return 1
	}
	logPath := filepath.Join(logDir, time.Now().UTC().Format("2006-01-02-15-04-05")+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create log %s: %v\n", logPath, err)
		return 1
	}

	var args []string
	if needsBootstrap {
		prompt := buildBootstrapPromptForKindV2(task.Slug, task.Kind, task.PlaybookSlug.String, false)
		if withText != "" {
			prompt = prompt + "\n\n" + withText
		}
		args = []string{"--session-id", sessionID, prompt}
	} else {
		args = []string{"--resume", sessionID}
		if withText != "" {
			args = append(args, withText)
		}
	}
	args = append(args, extraArgs...)

	cmd := execCommand("claude", args...)
	cmd.Dir = task.WorkDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "FOCUSFLOW_TASK="+task.Slug)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		fmt.Fprintf(os.Stderr, "error: start headless claude: %v\n", err)
		return 1
	}
	pid := cmd.Process.Pid
	go func() {
		cmd.Wait()
		logFile.Close()
	}()

	now := flowdb.NowISO()
	if _, err := db.Exec(
		`UPDATE tasks SET auto_run_status='running', auto_run_pid=?, auto_run_started=?, auto_run_log=?, updated_at=? WHERE slug=?`,
		pid, now, logPath, now, task.Slug,
	); err != nil {
		fmt.Fprintf(os.Stderr, "error: record auto_run: %v\n", err)
		return 1
	}
	fmt.Printf("Started headless run for %s (pid %d), logging to %s\n", task.Slug, pid, logPath)
	return 0
}

// refreshAutoRunStatus checks whether a task's recorded auto_run_pid is
// still alive and, if the row claims 'running' but the pid is gone,
// flips it to 'dead' (a crash/kill outside focusflow's control — not
// the same as 'completed', which only `focusflow done` sets). No-op for
// tasks with no in-flight auto run.
func refreshAutoRunStatus(db *sql.DB, task *flowdb.Task) error {
	if !task.AutoRunStatus.Valid || task.AutoRunStatus.String != "running" || !task.AutoRunPID.Valid {
		return nil
	}
	proc, err := os.FindProcess(int(task.AutoRunPID.Int64))
	if err != nil {
		return markAutoRunDead(db, task.Slug)
	}
	// On Unix, FindProcess always succeeds; signal 0 is the standard
	// liveness probe — it errors if the process doesn't exist.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return markAutoRunDead(db, task.Slug)
	}
	return nil
}

func markAutoRunDead(db *sql.DB, slug string) error {
	now := flowdb.NowISO()
	_, err := db.Exec(
		`UPDATE tasks SET auto_run_status='dead', auto_run_finished=?, updated_at=? WHERE slug=? AND auto_run_status='running'`,
		now, now, slug,
	)
	return err
}
```

`focusflow done` (in `internal/app/done.go` — check it exists and inspect briefly) should set `auto_run_status='completed'` + `auto_run_finished` when the task being closed has `auto_run_status='running'`; add that as a small addition there, gated the same way the rest of `done.go`'s side effects are (read `done.go` first to match its existing structure before editing — this is a small addition, not a rewrite).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/... -run 'TestDo_Auto|TestRefreshAutoRunStatus' -v`
Expected: PASS.

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/do.go internal/app/do_test.go internal/app/done.go
git commit -m "feat(app): implement do --auto as a real headless background runner"
```

## Task 6: `run playbook --here --auto` pass-through

**Files:**
- Modify: `internal/app/run.go`
- Test: `internal/app/run_test.go`

**Interfaces:**
- Consumes: `cmdDo`'s `--here`/`--auto` flags (Tasks 4-5).

- [ ] **Step 1: Write the failing test**

Add to `internal/app/run_test.go`:

```go
func TestRunPlaybook_AutoFlagPassesThroughToDo(t *testing.T) {
	env := newTestEnv(t)
	app.Run([]string{"add", "playbook", "Nightly", "--slug", "nightly", "--work-dir", env.WorkDir})

	origExecCommand := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd { return exec.Command("true") }
	defer func() { execCommand = origExecCommand }()

	rc := app.Run([]string{"run", "playbook", "nightly", "--auto"})
	if rc != 0 {
		t.Fatalf("run playbook --auto rc=%d", rc)
	}

	db := env.OpenDB(t)
	defer db.Close()
	tasks, err := flowdb.ListTasks(db, flowdb.TaskFilter{Kind: "playbook_run", IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 run task, got %d", len(tasks))
	}
	if !tasks[0].AutoRunStatus.Valid || tasks[0].AutoRunStatus.String != "running" {
		t.Errorf("AutoRunStatus = %+v, want running (proves --auto reached cmdDo)", tasks[0].AutoRunStatus)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app/... -run TestRunPlaybook_AutoFlagPassesThroughToDo -v`
Expected: FAIL — `flag provided but not defined: -auto` at the `run playbook` level.

- [ ] **Step 3: Implement pass-through**

In `internal/app/run.go`'s `cmdRunPlaybook`, add flags alongside the existing `dangerSkip`:

```go
	here := fs.Bool("here", false, "bind the current harness session to the new run instead of spawning a tab")
	auto := fs.Bool("auto", false, "run the playbook headlessly in the background")
```

Extend the `doArgs` construction near the end of the function:

```go
	doArgs := []string{runSlug}
	if *dangerSkip {
		doArgs = append(doArgs, "--dangerously-skip-permissions")
	}
	if *here {
		doArgs = append(doArgs, "--here")
	}
	if *auto {
		doArgs = append(doArgs, "--auto")
	}
```

(`cmdDo` already rejects `--auto`+`--here` together, so no extra validation is needed here — the existing `fs.Parse` in `cmdDo` will surface that error if the user somehow passes both.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/app/... -run TestRunPlaybook_AutoFlagPassesThroughToDo -v`
Expected: PASS.

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/run.go internal/app/run_test.go
git commit -m "feat(app): pass --here/--auto through from run playbook to do"
```

## Task 7: Parity test harness — golden `--help` fixtures + diff test

**Files:**
- Create: `internal/app/testdata/flow-help/*.txt` (one file per command)
- Create: `internal/app/parity_test.go`

**Interfaces:**
- Produces: `TestParity_HelpFlagsMatchFlow` — a table-driven test that, for each covered command, parses flag names out of both a golden fixture and focusflow's live `--help` output, and asserts the flag-name sets are equal.

- [ ] **Step 1: Capture golden fixtures from live flow (read-only)**

Run (read-only `--help` invocations only — safe per this plan's Global Constraints):

```bash
mkdir -p internal/app/testdata/flow-help
for spec in "add_task:add task" "add_project:add project" "add_playbook:add playbook" "do:do" "run_playbook:run playbook" "done:done" "list_tasks:list tasks" "list_projects:list projects" "list_playbooks:list playbooks" "list_tags:list tags" "edit:edit" "update_task:update task" "archive:archive" "unarchive:unarchive" "workdir:workdir" "skill:skill" "transcript:transcript"; do
  name="${spec%%:*}"; cmd="${spec#*:}"
  flow $cmd --help > "internal/app/testdata/flow-help/${name}.txt" 2>&1 || true
done
```

Inspect each resulting file briefly (`head -5` on a couple) to confirm they contain real `--help` text, not an error like "unknown subcommand" (some of flow's own subcommands may not support a bare `--help` the same way — e.g. `list tags` might need `flow list tags --help` specifically, already covered above). Delete any fixture file that came back empty or as an error message rather than real help text, and note which commands' `--help` isn't independently discoverable (skip those in the test table below rather than asserting against garbage).

- [ ] **Step 2: Write the failing parity test**

Create `internal/app/parity_test.go`:

```go
package app_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// flagNameRe extracts long-flag names ("-format", "--format") from Go's
// flag.FlagSet-style --help output. This matches both the "-name" form
// (Go's own convention) and the "--name" form some fixtures may use.
var flagNameRe = regexp.MustCompile(`(?m)^\s*-{1,2}([a-zA-Z][a-zA-Z0-9-]*)`)

func extractFlagNames(help string) []string {
	seen := map[string]bool{}
	for _, m := range flagNameRe.FindAllStringSubmatch(help, -1) {
		seen[m[1]] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func TestParity_HelpFlagsMatchFlow(t *testing.T) {
	binPath := buildFocusflowBinary(t)

	cases := []struct {
		fixture string
		args    []string
	}{
		{"add_task", []string{"add", "task", "--help"}},
		{"add_project", []string{"add", "project", "--help"}},
		{"add_playbook", []string{"add", "playbook", "--help"}},
		{"do", []string{"do", "--help"}},
		{"run_playbook", []string{"run", "playbook", "--help"}},
		{"list_tasks", []string{"list", "tasks", "--help"}},
		{"list_projects", []string{"list", "projects", "--help"}},
		{"list_playbooks", []string{"list", "playbooks", "--help"}},
		{"list_tags", []string{"list", "tags", "--help"}},
		{"update_task", []string{"update", "task", "--help"}},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", "flow-help", tc.fixture+".txt")
			golden, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Skipf("no golden fixture for %s (%v) — capture it per Task 7 Step 1 if flow gained this command", tc.fixture, err)
			}
			wantFlags := extractFlagNames(string(golden))

			cmd := exec.Command(binPath, tc.args...)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			cmd.Run() // --help commonly exits nonzero via flag.ContinueOnError; ignore exit code, check output
			gotFlags := extractFlagNames(out.String())

			missing := diffStrings(wantFlags, gotFlags)
			extra := diffStrings(gotFlags, wantFlags)
			if len(missing) > 0 {
				t.Errorf("focusflow %v is missing flags present in live flow: %v", tc.args, missing)
			}
			if len(extra) > 0 {
				t.Logf("focusflow %v has extra flags not in live flow (may be intentional, e.g. removed --session-id replacements): %v", tc.args, extra)
			}
		})
	}
}

// diffStrings returns elements of a not present in b.
func diffStrings(a, b []string) []string {
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

// buildFocusflowBinary builds the current source into a temp binary once
// per test run, so the parity test always reflects the code under test,
// not a possibly-stale installed binary.
func buildFocusflowBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "focusflow")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = ".." // internal/app -> repo root is two levels up; verify this path resolves correctly given the actual package layout, adjust if internal/app isn't exactly one level under the module root's sibling — check go.mod's location (repo root) relative to internal/app/parity_test.go and correct "Dir" accordingly (likely "../.." from internal/app, not ".." — internal/app/parity_test.go is at internal/app/, module root is two directories up).
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build focusflow: %v\n%s", err, stderr.String())
	}
	return binPath
}
```

Double check the `cmd.Dir` path for `buildFocusflowBinary` against the actual repo layout (`go.mod` is at `/Users/samrattalukder/Documents/random/focusflow/go.mod`; this test file lives at `internal/app/parity_test.go`) before running — it should be `"../.."`.

- [ ] **Step 3: Run the test to see current gaps (informational, not required to pass yet)**

Run: `go test ./internal/app/... -run TestParity_HelpFlagsMatchFlow -v`
Expected: after Tasks 1-6 are complete, this should mostly PASS (or show only the intentionally-removed `--session-id` as an "extra" `t.Log`, not a failure, and no `missing` entries for the commands covered). If it still reports `missing` flags for a command this plan claims to have covered, that's a real bug to fix before moving on — do not mark this task done with known `missing` entries.

- [ ] **Step 4: Fix any real gaps surfaced, then confirm clean**

Run: `go test ./internal/app/... -run TestParity_HelpFlagsMatchFlow -v`
Expected: PASS, zero `missing` entries for every case.

- [ ] **Step 5: Commit**

```bash
git add internal/app/testdata/flow-help internal/app/parity_test.go
git commit -m "test(app): add repeatable flag-parity check against golden flow --help fixtures"
```

## Task 8: End-to-end proof — control-center's exact call against real focusflow data

**Files:**
- Test: `internal/app/e2e_test.go` (add to the existing file — do not create a new one; check its structure first and match its conventions)

**Interfaces:**
- Consumes: everything from Tasks 1-6.

- [ ] **Step 1: Write the failing end-to-end test**

Add to `internal/app/e2e_test.go` (after reading its existing structure/helpers to match conventions exactly):

```go
func TestE2E_ControlCenterExactListTasksCall(t *testing.T) {
	env := newTestEnv(t) // match existing e2e helper name
	app.Run([]string{"add", "project", "Dash", "--slug", "dash", "--work-dir", env.WorkDir})
	app.Run([]string{"add", "task", "Ship it", "--slug", "ship-it", "--project", "dash", "--work-dir", env.WorkDir, "--priority", "high", "--tag", "urgent"})
	app.Run([]string{"add", "task", "Old one", "--slug", "old-one", "--work-dir", env.WorkDir})
	app.Run([]string{"archive", "old-one"})
	app.Run([]string{"add", "task", "Finished", "--slug", "finished", "--work-dir", env.WorkDir})
	app.Run([]string{"update", "task", "finished", "--status", "done"})

	out := captureStdout(t, func() {
		rc := app.Run([]string{"list", "tasks", "--format", "json", "--include-done", "--include-archived", "--kind", "all"})
		if rc != 0 {
			t.Fatalf("control-center's exact call failed: rc=%d", rc)
		}
	})

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("control-center's exact call did not produce parseable JSON: %v\noutput:\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (ship-it, old-one archived, finished done), got %d: %v", len(rows), rows)
	}

	// readTasks() in control-center's lib/flow-reader.js reads: slug,
	// name, project, status, priority, tags, live, archived. Confirm
	// every one is present and correctly typed on every row, not just
	// the first.
	requiredKeys := []string{"slug", "name", "project", "status", "priority", "tags", "live", "archived"}
	for _, row := range rows {
		for _, k := range requiredKeys {
			if _, ok := row[k]; !ok {
				t.Errorf("row %v missing key %q required by control-center's readTasks()", row["slug"], k)
			}
		}
		if _, ok := row["tags"].([]any); !ok {
			t.Errorf("row %v: tags must be a JSON array, got %T", row["slug"], row["tags"])
		}
		if _, ok := row["live"].(bool); !ok {
			t.Errorf("row %v: live must be a JSON boolean, got %T", row["slug"], row["live"])
		}
		if _, ok := row["archived"].(bool); !ok {
			t.Errorf("row %v: archived must be a JSON boolean, got %T", row["slug"], row["archived"])
		}
	}
}
```

Add `"encoding/json"` to `e2e_test.go`'s imports if not already present.

- [ ] **Step 2: Run the test to verify it fails (before Tasks 1-6) or passes (after)**

Run: `go test ./internal/app/... -run TestE2E_ControlCenterExactListTasksCall -v`
Expected: this task is written last, after Tasks 1-6 are done, so it should PASS immediately, proving end-to-end integration. If it fails, that's a real integration bug between the individually-tested pieces — debug and fix rather than adjusting the test to match broken behavior.

- [ ] **Step 3: Run the entire test suite one final time**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./...`
Expected: build clean, `go vet` clean, `gofmt -l .` prints nothing (no unformatted files), all tests pass.

- [ ] **Step 4: Verify real flow is untouched**

Run:
```bash
shasum -a 256 ~/.local/bin/flow
```
Expected: matches the pre-session baseline `ead9d62bec26532a57d637363568c19f0b1297318fe93cf06178488998228576` exactly (captured at the start of this task's planning session). The `~/.flow/flow.db` checksum will legitimately differ from the very first baseline due to the already-disclosed, already-resolved `help-4` stray-task incident from this session's research phase — that's a known, accepted, reversible deviation, not something to re-verify against here.

- [ ] **Step 5: Commit**

```bash
git add internal/app/e2e_test.go
git commit -m "test(app): add end-to-end proof of control-center's exact list tasks call"
```

---

## After this plan

Phase 2 (messaging: `message`/`msg`, `broadcast`/`post`, `inbox`, `watch`; `stats`; and the `owner` subsystem) is tracked as separate follow-on work per the user's explicit sequencing choice — do not start it as part of this plan. Rebuild and reinstall the binary (`make install` or `go build -o focusflow . && cp focusflow ~/.local/bin/focusflow`) once Phase 1 is merged, so `focusflow skill update` and the installed binary reflect these changes for real use, not just the repo.
