package app

import (
	"encoding/json"
	"fmt"
	"os"
)

// cmdHook dispatches `focusflow hook <subcommand>`. The only subcommand in
// v2 is `session-start`, which is intended to be wired as a Claude Code
// SessionStart hook so that EVERY session start (fresh spawn AND resume)
// re-injects the "load your task context" instruction. Without the hook,
// resumed sessions never re-read briefs and updates that may have been
// edited since the previous session.
func cmdHook(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: hook requires a subcommand (session-start)")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "session-start":
		return cmdHookSessionStart(rest)
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

	// Best-effort, one-way flow→focusflow mirror. Runs on EVERY session
	// start regardless of $FOCUSFLOW_TASK — a session can be bound in real
	// flow (and therefore mirror-eligible) whether or not focusflow itself
	// spawned it. See mirror.go for the full contract: never blocks, never
	// writes to real flow, never touches a focusflow-native row.
	mirrorFromFlow(currentHarnessSessionID())

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
			"file on the fly — no permission needed — per the focusflow skill's §4.10.",
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
