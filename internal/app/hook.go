package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"focusflow/internal/store"
	"os"
	"strings"
	"time"
)

// cmdHook dispatches `focusflow hook <subcommand>`. session-start,
// user-prompt-submit, and stop are wired as Claude Code hooks (see
// ~/.claude/settings.json) so a bound session's task context, scope-creep
// anchor, and pending-reply nudge all stay live without the agent having
// to remember to check any of them itself.
func cmdHook(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: hook requires a subcommand (session-start|user-prompt-submit|stop)")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "session-start":
		return cmdHookSessionStart(rest)
	case "user-prompt-submit":
		return cmdHookUserPromptSubmit(rest)
	case "stop":
		return cmdHookStop(rest)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown hook subcommand %q\n", sub)
		return 2
	}
}

// cmdHookSessionStart emits a Claude Code SessionStart hook response.
// Wired via ~/.claude/settings.json with a matcher of "startup|resume"
// so it fires for both fresh spawns and `claude --resume`.
//
// Two modes:
//   - $FOCUSFLOW_TASK set (spawned by `focusflow do`): emit the full task-context
//     reload instructions. On a fresh spawn this is redundant with the
//     bootstrap prompt but harmless; on a resume it's the only way to
//     force the agent to re-read potentially-updated briefs and updates.
//   - $FOCUSFLOW_TASK unset (ad-hoc session, e.g. bare `claude`): emit a
//     short hint that the focusflow skill is installed and should be used
//     when the request touches task / project / session management.
//     Without this, Claude Code may not auto-invoke the skill on the
//     user's first message.
func cmdHookSessionStart(args []string) int {
	fs := flagSet("hook session-start")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	slug := os.Getenv("FOCUSFLOW_TASK")
	if slug == "" {
		return emitAmbientSkillHint()
	}

	instructions := fmt.Sprintf(
		"You are running inside a focusflow execution session for task %q. "+
			"Before doing anything else in this turn, re-load your task context — "+
			"the brief and update files may have been edited since your previous "+
			"session. Do these in order: "+
			"(1) invoke the `focusflow` skill via the Skill tool. That skill is your "+
			"operating manual for this session: it defines the bootstrap contract, "+
			"the workflows for starting/saving/logging/archiving work, KB scoop "+
			"discipline, and the scope-creep detection that keeps unrelated work "+
			"from landing in the wrong task. "+
			"(2) run `focusflow show task` and use your Read tool on the file at the "+
			"`brief:` path AND every file listed under `updates:`; "+
			"(3) if a project is listed on the task, run `focusflow show project <that-slug>` "+
			"and Read its brief and updates too; "+
			"(4) Read `CLAUDE.md` in your work_dir and any nested CLAUDE.md under "+
			"subdirectories you plan to modify. "+
			"Only then proceed with the user's request. "+
			"If any brief section is blank or unclear, ASK — do not infer. "+
			"The `kb:` section of `focusflow show task` lists the knowledge-base files "+
			"(durable facts about the user, org, products, processes, business). "+
			"DO NOT read these eagerly on every turn — lazy-load only when the current "+
			"task requires that context (e.g. a brief that uses domain-specific terminology "+
			"you don't recognize, a question about who someone is, a request for org context). "+
			"Throughout the session, if the user shares a durable fact about themselves, "+
			"the org, products, processes, or business, append it to the matching kb "+
			"file on the fly — no permission needed — per the focusflow skill's §4.10. "+
			"Message-bus listener discipline: arm ONE persistent Monitor NOW (Monitor "+
			"streams stdout lines as events): Monitor(command: 'while true; do focusflow "+
			"inbox pop --wait --timeout 300 --json || true; done', persistent: true). "+
			"Every message then wakes you as an event, no re-arming ever (--json is silent "+
			"on timeouts). Only if Monitor is unavailable, fall back to a background Bash "+
			"`focusflow inbox pop --wait` and re-arm it after every wake.",
		slug,
	)

	return emitSessionStartContext(instructions)
}

// emitAmbientSkillHint is the FOCUSFLOW_TASK-unset branch of the SessionStart
// hook. Used for ad-hoc `claude` sessions where there is no specific
// task to load. It nudges Claude to invoke the focusflow skill when
// the user's request touches focusflow-managed concerns, and — critically —
// to offer to create a new focusflow task or switch to an existing one when
// the user starts substantive work without a task attached. Otherwise
// the transcript of this session is homeless: no brief, no updates, no
// resumability tomorrow.
func emitAmbientSkillHint() int {
	hint := "This session is not bound to any focusflow task (FOCUSFLOW_TASK is unset).\n" +
		"When substantive work emerges, run §4.14 of the focusflow skill to offer the user a focusflow task.\n" +
		"The check is ongoing, not one-shot — re-evaluate on every turn."
	return emitSessionStartContext(hint)
}

// emitSessionStartContext marshals the SessionStart hookSpecificOutput
// shape to stdout. Shared by both the per-task and ambient paths.
func emitSessionStartContext(ctx string) int {
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": ctx,
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode hook json: %v\n", err)
		return 1
	}
	return 0
}

// cmdHookUserPromptSubmit emits a Claude Code UserPromptSubmit hook
// response. Wired via ~/.claude/settings.json so EVERY user prompt in a
// bound session re-injects a one-line anchor naming the task (with the
// skill's "§4.11/§4.7" citation), since without this reminder
// scope-creep detection (§4.11) and close-out offers (§4.7) only ever get
// checked once, at session start, and silently drift out of the agent's
// attention over a long session.
//
// Silent (no output) when the session isn't bound to any task — the
// anchor only makes sense once there's a task to name.
func cmdHookUserPromptSubmit(args []string) int {
	fs := flagSet("hook user-prompt-submit")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dbPath, err := dataDBPath()
	if err != nil {
		return 0 // focusflow not initialized — nothing to anchor
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		return 0
	}
	defer db.Close()

	slug := resolveCurrentTaskSlug(db)
	if slug == "" {
		return 0
	}
	task, err := store.GetTask(db, slug)
	if err != nil {
		return 0 // stale $FOCUSFLOW_TASK or dangling session_id — say nothing rather than guess
	}

	ctx := fmt.Sprintf(
		"focusflow session → task %q (%s). Per §4.11/§4.7: if this prompt is unrelated work, "+
			"offer a new task; if it signals the task is done, offer to close it out. Else proceed silently.",
		task.Name, slug,
	)
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": ctx,
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode hook json: %v\n", err)
		return 1
	}
	return 0
}

// stopHookInput is the subset of Claude Code's Stop-hook stdin JSON this
// command reads. stop_hook_active is true when this invocation is
// itself the forced continuation from a PRIOR block — without checking
// it, a still-unread reply would block every single "stop" forever.
type stopHookInput struct {
	StopHookActive bool `json:"stop_hook_active"`
}

// cmdHookStop emits a Claude Code Stop hook response. Wired via
// ~/.claude/settings.json. The block reason starts with "focusflow-bus:"
// so an agent (and the skill, §4.18) can recognize it.
//
// Purpose: the armed Monitor loop (`focusflow inbox pop --wait`) is the
// primary way a session notices a reply arrived, but it's a separate
// process the agent could fail to arm or that could die silently. This
// hook is the belt-and-braces backstop: right as the agent's turn is
// about to end, if a reply has arrived to a question this task asked
// the user (`focusflow message user "..."`) and that reply is still
// unread, block the stop and surface it — forcing the agent to consume
// it via `focusflow inbox pop` before the turn is allowed to end. Once
// consumed (mail flips to 'read'), the condition stops matching, so the
// nudge fires exactly until it's acted on, never after.
func cmdHookStop(args []string) int {
	fs := flagSet("hook stop")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var in stopHookInput
	_ = json.NewDecoder(os.Stdin).Decode(&in) // best-effort; absent/malformed stdin just means in.StopHookActive stays false
	if in.StopHookActive {
		return 0
	}

	dbPath, err := dataDBPath()
	if err != nil {
		return 0
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		return 0
	}
	defer db.Close()

	slug := resolveCurrentTaskSlug(db)
	if slug == "" {
		return 0
	}

	asked, err := store.LatestOutgoingMessage(db, slug, "user")
	if err != nil || asked == nil {
		return stopBroadcastNudge(db, slug)
	}
	reply, err := store.UnreadReplyTo(db, asked.ID)
	if err != nil || reply == nil {
		return stopBroadcastNudge(db, slug)
	}

	elapsed := "an unknown amount of time"
	if askedAt, err1 := time.Parse(time.RFC3339, asked.CreatedAt); err1 == nil {
		if repliedAt, err2 := time.Parse(time.RFC3339, reply.CreatedAt); err2 == nil {
			elapsed = formatHookElapsed(repliedAt.Sub(askedAt))
		}
	}

	reason := fmt.Sprintf(
		"focusflow-bus: the user has just returned. Your message [%s] (%q) was answered after %s. "+
			"Factor the elapsed time in; re-verify stale state after long waits. Consume it with "+
			"`focusflow inbox pop` now.",
		asked.ID, asked.Body, elapsed,
	)
	out := map[string]any{
		"decision": "block",
		"reason":   reason,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode hook json: %v\n", err)
		return 1
	}
	return 0
}

// formatHookElapsed renders a duration the way the nudge text expects
// (e.g. "3m", "1h5m") — coarse enough to read at a glance, no need for
// second-level precision in a "how long were you gone" framing.
func formatHookElapsed(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// nudgeBackoffFor is how long to wait after the Nth nudge before nudging
// a task about broadcasts again: a quick first follow-up, then
// progressively rarer.
func nudgeBackoffFor(attempts int) time.Duration {
	switch {
	case attempts <= 0:
		return 0
	case attempts == 1:
		return 2 * time.Minute
	case attempts == 2:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// stopBroadcastNudge is the Stop hook's second job (after the reply
// nudge): if broadcasts fanned out to this task's mailbox by a watch are
// still unread and haven't been surfaced to it yet, block the stop once
// and list them, marking them surfaced so they're not repeated. The
// bus_nudges row rate-limits this per task with a growing backoff; it is
// cleared whenever the task consumes mail (`inbox pop`).
func stopBroadcastNudge(db *sql.DB, slug string) int {
	pending, err := store.PendingUnsurfacedForTask(db, slug)
	if err != nil || len(pending) == 0 {
		return 0
	}
	st, err := store.GetNudgeState(db, slug)
	if err != nil {
		return 0
	}
	if st.Attempts > 0 {
		if at, perr := time.Parse(time.RFC3339, st.NudgedAt); perr == nil && time.Since(at) < nudgeBackoffFor(st.Attempts) {
			return 0
		}
	}

	var lines []string
	ids := make([]string, 0, len(pending))
	for _, m := range pending {
		from := m.FromAssignee
		if m.FromTaskSlug.Valid {
			from = m.FromTaskSlug.String
		}
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", m.ID, from, m.Body))
		ids = append(ids, m.ID)
	}
	if err := store.MarkSurfaced(db, slug, ids...); err != nil {
		return 0
	}
	if err := store.RecordNudge(db, slug); err != nil {
		return 0
	}
	reason := "focusflow-bus: broadcasts from tasks you watch are waiting:\n" + strings.Join(lines, "\n") +
		"\nConsume them with `focusflow inbox pop` (or `focusflow inbox`)."
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"decision": "block", "reason": reason}); err != nil {
		fmt.Fprintf(os.Stderr, "error: encode hook json: %v\n", err)
		return 1
	}
	return 0
}
