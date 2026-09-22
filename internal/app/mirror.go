package app

import (
	"database/sql"
	"fmt"
	"focusflow/internal/flowdb"
	"focusflow/internal/realflow"
	"os"
	"path/filepath"
	"time"
)

// mirrorTimeout bounds how long mirrorFromFlow will wait for a real-flow
// read before giving up. This runs in the hot path of EVERY session
// start (see cmdHookSessionStart) — a slow or hung real flow.db must
// never delay, block, or break the user's actual session start. A var
// (not const) so tests can shrink it instead of sleeping past a
// production-sized timeout.
var mirrorTimeout = 1500 * time.Millisecond

// mirrorBusyTimeoutMs is the SQLite busy_timeout passed to the read-only
// connection against real flow.db. It bounds lock-contention waits
// specifically; mirrorTimeout is the overall wall-clock backstop that
// also covers a slow disk, a huge WAL, or any other cause of a stuck
// call — not just a held lock.
const mirrorBusyTimeoutMs = 500

// realFlowLookup is a package-level function var so tests can substitute
// a slow/failing double without touching real ~/.flow state or real
// timing — the same pattern iterm.Runner uses for osascript (see
// CLAUDE.md "External dependencies... are mocked via package-level
// function vars").
var realFlowLookup = realflow.LookupBySessionID

// currentHarnessSessionID returns the active harness's session id, or ""
// if this session wasn't spawned by either harness's usual entry point.
// Mirrors the flow skill's §10 rule: Claude Code exposes
// CLAUDE_CODE_SESSION_ID, Codex exposes CODEX_THREAD_ID. This is
// deliberately independent of $FOCUSFLOW_TASK — a session can be bound
// in real flow (and therefore mirror-eligible) whether or not focusflow
// itself spawned it.
func currentHarnessSessionID() string {
	if id := os.Getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		return id
	}
	return os.Getenv("CODEX_THREAD_ID")
}

// mirrorFromFlow is the SessionStart-hook entry point for the flow→focusflow
// mirror. It is entirely best-effort: any error — no real flow installed,
// a slow or failing read, bad data, focusflow itself not initialized — is
// logged to stderr (or silently skipped for the expected "nothing to do"
// cases) and swallowed. It must never delay or crash the user's actual
// session start. One-way only: reads real flow's flow.db read-only and
// writes ONLY into focusflow's own database. Real flow is never written
// to by any code reachable from here.
func mirrorFromFlow(sessionID string) {
	if sessionID == "" {
		return
	}
	dbPath, err := realflow.DBPath()
	if err != nil {
		return // no real-flow root resolvable — nothing to mirror
	}
	if _, err := os.Stat(dbPath); err != nil {
		return // real flow not installed/reachable — silently skip
	}

	type result struct {
		task    *realflow.Task
		project *realflow.Project
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		t, p, err := realFlowLookup(dbPath, sessionID, mirrorBusyTimeoutMs)
		ch <- result{t, p, err}
	}()

	var res result
	select {
	case res = <-ch:
	case <-time.After(mirrorTimeout):
		fmt.Fprintf(os.Stderr, "focusflow: mirror: real-flow read timed out after %s, skipping\n", mirrorTimeout)
		return
	}
	if res.err != nil {
		fmt.Fprintf(os.Stderr, "focusflow: mirror: %v\n", res.err)
		return
	}
	if res.task == nil {
		return // this session isn't bound to a real-flow task
	}

	focusDBPath, err := flowDBPath()
	if err != nil {
		return // focusflow itself not initialized — nothing to mirror into
	}
	db, err := openConcurrentDB(focusDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "focusflow: mirror: open focusflow.db: %v\n", err)
		return
	}
	defer db.Close()

	if err := applyMirror(db, res.task, res.project); err != nil {
		fmt.Fprintf(os.Stderr, "focusflow: mirror: %v\n", err)
	}
}

// applyMirror performs the idempotent upsert of a real-flow task (and,
// if present, its project) into focusflow's own database. Every write
// goes through flowdb.UpsertMirroredProject / UpsertMirroredTask, which
// guard on source = 'flow-mirror' so a focusflow-native row is never
// touched by a mirror run.
func applyMirror(db *sql.DB, t *realflow.Task, p *realflow.Project) error {
	root, err := flowRoot()
	if err != nil {
		return err
	}

	if p != nil {
		applied, err := flowdb.UpsertMirroredProject(db, p.Slug, p.Name, p.Status, p.Priority, p.WorkDir)
		if err != nil {
			return fmt.Errorf("mirror project %s: %w", p.Slug, err)
		}
		if applied {
			if err := ensureMirroredEntityFiles(root, "projects", p.Slug, p.Name); err != nil {
				return fmt.Errorf("mirror project %s: %w", p.Slug, err)
			}
		}
	}

	applied, err := flowdb.UpsertMirroredTask(db, t.Slug, t.Name, t.ProjectSlug, t.Status, t.Priority, t.WorkDir)
	if err != nil {
		return fmt.Errorf("mirror task %s: %w", t.Slug, err)
	}
	if applied {
		if err := ensureMirroredEntityFiles(root, "tasks", t.Slug, t.Name); err != nil {
			return fmt.Errorf("mirror task %s: %w", t.Slug, err)
		}
	}
	return nil
}

// mirroredBriefStub is written for a mirrored project/task the first
// time it's mirrored (never overwritten on refresh — the on-disk brief
// is not part of what gets kept in sync; only the DB row is).
const mirroredBriefStub = `# %s

Mirrored read-only from real flow. This brief is a local stub — the
source of truth for planning content is the real flow task/project of
the same slug. Edit this file freely; the mirror never overwrites it
after creation, only the DB row (status, priority, name, work_dir)
refreshes on each session start.
`

// ensureMirroredEntityFiles creates the on-disk updates/ dir and a stub
// brief.md for a newly-mirrored project or task, mirroring the layout
// addProject/addTask create for native entities — so `focusflow show`
// and the skill's file-reading conventions work identically for mirrored
// rows. kind is "projects" or "tasks". Idempotent: never overwrites an
// existing brief.md (a user may have started editing it).
func ensureMirroredEntityFiles(root, kind, slug, name string) error {
	dir := filepath.Join(root, kind, slug)
	if err := os.MkdirAll(filepath.Join(dir, "updates"), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	briefPath := filepath.Join(dir, "brief.md")
	if _, err := os.Stat(briefPath); os.IsNotExist(err) {
		if err := os.WriteFile(briefPath, []byte(fmt.Sprintf(mirroredBriefStub, name)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", briefPath, err)
		}
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", briefPath, err)
	}
	return nil
}
