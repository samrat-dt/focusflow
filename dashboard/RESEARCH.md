# Market research and use-case backlog

Researched 2026-09-25 (web search; sources listed at the end are mostly vendor and blog pages, so treat individual
claims as "as reported", and re-verify before quoting them publicly). Feeds the **About** section and the redesign.

## 1. The landscape (as of September 2026)

Agentic coding has moved from "one chat in a terminal" to "many things in flight, and you in the orchestrator seat"
(Anthropic's own words for the April 2026 Claude Code desktop redesign). Tools fall into five shapes:

| Shape | Examples | What it is good at | Where it leaves a gap |
|---|---|---|---|
| **First-party** | Claude Code desktop app (multi-session sidebar, diff viewer, in-app terminal/editor, worktree per session, April 2026); **Agent View** and `claude --bg` background sessions (research preview, May 2026); **Routines** (scheduled / API / GitHub-triggered cloud runs) | Deep integration, always current, cloud runs that do not need your Mac | Session-centric, not task- or project-centric: no durable briefs/notes, no approval stage, no cross-session messaging with receipts; cloud routines need a paid plan and run off-machine |
| **Kanban / board orchestrators** | Vibe Kanban (company wound down April 2026, now community open source), CodeAgentSwarm, Nimbalyst (board + editors + iOS app) | Clear visual queue; a worktree/branch per card | Cards are prompts, not long-lived work with memory; mostly solo-developer and Mac-local; the operator remains the bottleneck |
| **Workspace apps** | Conductor (Mac, worktree per workspace, diff + PR flow), Superset, Mux, Sculptor (container isolation) | Fast diff review, isolation, remote hosts | Review of *code*, little support for *planning, prioritising and remembering* |
| **Terminal multiplexers** | Claude Squad, Paneflow, tmux scripts | Full control, zero lock-in | You build and maintain the organisation layer yourself |
| **Hosted autonomous engineers and IDE agents** | Devin, Cursor background agents (cloud VMs), Copilot / Codex agents | Fully unattended, cloud scale | Your code and context leave the machine; per-seat/usage cost; not Claude Code sessions |

Common unsolved problems named across the sources: **the operator is the bottleneck** (visibility alone does not
fix coordination), **planning, review, files and tasks are not integrated into one layer**, **oversight away from the
desk is weak** (only one tool has a native mobile review), and most tools are **Mac-local and solo-oriented**.

## 2. Honest positioning of Focusflow

**What overlaps with Anthropic's own direction (be candid in About):** multi-session overview, background runs,
scheduled runs, an "agents waiting for you" view. Focusflow should never claim these as unique.

**What is genuinely different today:**
1. **Work, not sessions.** The unit is a *task with a brief, notes and a stage*, grouped in *projects*, persisting
   across many sessions. Sessions come and go; the work and its memory stay.
2. **Approval as a first-class step.** Review → Approve closes the loop and sweeps learnings into a knowledge base.
3. **A message bus with receipts.** Message a task or a project; delivery is confirmed (sent → delivered → reply).
4. **Project assistants.** One permanent coordinator per project decides whether a new ask belongs to an existing
   task or needs a new one, and can check in on a schedule.
5. **Local-first, no account.** SQLite on your Mac, files you can read, daily backups, nothing leaves the machine
   except what Claude itself sends.
6. **A single "Needs you" page** that ranks reviews, questions, failed runs and overdue items.
7. **Playbooks**: repeatable runs from a saved brief.

**Genuine gaps versus the market (roadmap candidates):** no git-worktree isolation per task, no diff/PR view, no
mobile companion, no cloud runs, Mac-only, no calendar/time view, no team mode.

**The story of Flow → Focusflow (high-level, per your choice):** `flow` was the earlier personal task-and-session
tool. Focusflow kept its core (tasks, briefs, per-task sessions) and added stages and approval, a message bus,
headless runs, project assistants, backups and a dashboard; `flow` was then retired after its data was migrated.

## 3. Use-case backlog: organisation, cleanliness, freedom

Grouped by the outcome for the user. **[E]** = essential for the redesign, **[N]** = next, **[V]** = vision.

### Organise
- **Home with a plan for today**: pick up to three things, see them first; end-of-day "wrap up" [E]
- **Saved views and grouping**: group by project / priority / due / stage; sort; save as named views in the sidebar [E]
- **Board ⇄ List ⇄ Calendar** switcher for Tasks; Backlog as a first-class view, not hidden [E]
- **Tags and labels** with a picker; **priority and due date** editable inline; **recurring** tasks [E]
- **Subtasks / checklists** and a **definition of done** per task, shown at review [N]
- **Dependencies** ("blocked by…"), surfaced on Home when unblocked [N]
- **Pin and favourite** tasks and projects; **Recents** [E]
- **Bulk select**: approve, pause, move, archive several at once, each with Undo [E]
- **Project pages**: brief, progress bar, what is up next, what is running, what shipped, ask-the-project [E]
- **Templates**: task and project templates, plus a **playbook gallery** [N]

### Capture and freedom
- **Global quick-capture hotkey** and **menu-bar capture**, from anywhere on the Mac [E]
- **Capture from clipboard / a URL / a selected file**; **drag a folder** to create a project [N]
- **Edit everything in the app**: name, priority, due, tags, notes, brief (Markdown editor) [E]
- **⌘K command palette** for every action, task and setting [E]
- **Customisable sidebar** (reorder, hide sections), **density** (comfortable/compact), **default landing page** [N]
- **Import and export**: Markdown/JSON/CSV; **calendar (ICS) feed** for due dates; **GitHub issues / Linear import** [N]
- **Open in…**: Finder, editor, git branch, terminal profile of choice [E]

### Trust and control over runs
- **Run controls per project**: allow unattended runs, time and cost limits, "ask me before…" rules [N]
- **Proof of work** on every review: summary, files changed, tests run, a link to the diff [N]
- **Worktree per task** and PR status (market parity) [V]
- **Activity timeline per task** from the existing event log; **undo history** [N]
- **Quiet hours / Do-not-disturb** and per-type notification settings; click-through notifications with actions [E]
- **Status page**: health checks, backups (last/next), versions, "restore from backup" [E]

### Reach
- **Mobile companion**: approve and reply from a phone [V]
- **Weekly review** generated from the event log; **insights** (throughput, review latency, run success) [N]
- **Voice capture** [V]; **shareable playbooks** [V]; **team edition** on the same core [V]

## 4. Sources

- Nimbalyst, "Best Tools for Managing Parallel AI Coding Agents in 2026": https://nimbalyst.com/blog/best-agent-management-tools-2026/
- Nimbalyst, "Best Multi-Agent Coding Tools for Claude Code and Codex Users (2026)": https://nimbalyst.com/blog/best-multi-agent-coding-tools-2026/
- DEV Community, "Best Tools for Managing Parallel AI Coding Agents in 2026": https://dev.to/stravukarl/best-tools-for-managing-parallel-ai-coding-agents-in-2026-14l8
- Build Fast with AI, "Claude Code Desktop Redesign: Multi-Sessions + Routines (2026)": https://blog.buildfastwithai.com/claude-code-desktop-redesign-2026
- Claude Code Agent View (research preview, May 2026): https://pasqualepillitteri.it/en/news/2384/claude-code-agent-view-cli-dashboard-sessions-2026
- Claude Code docs, Desktop application: https://code.claude.com/docs/en/desktop
- amux, "Background Coding Agents Compared (2026)": https://amux.io/guides/background-agents-compared/
- Augment Code, "9 Open-Source Agent Orchestrators for AI Coding (2026)": https://www.augmentcode.com/tools/open-source-agent-orchestrators
- Design: Raw.Studio on Notion UX; Linear design refresh: https://raw.studio/blog/how-notion-ux-converts-100-million-users , https://linear.app/now/behind-the-latest-design-refresh
