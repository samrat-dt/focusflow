# Moat build: Attention and Memory

Written 2026-09-25. Implements pillars 1 and 2 of `POSITIONING.md`. Pillar 3 (judgement: rules learned from
approvals) builds on both and comes next.

## What Anthropic does not provide, and why this is hard to copy

Claude Code remembers per repository (CLAUDE.md, auto-memory the agent writes) and shows which sessions are waiting.
It does **not** know about *your work across sessions*: which question you already answered, which instruction you gave
another session on the same project last week, or what deserves your attention across every agent right now.
Focusflow sits on the only channel where that information flows: every message between you and every agent goes
through its bus, and every session starts through its hooks. **Memory captured from that channel and fed back into
every session is the product's compounding asset.** A copy would need the channel, the hooks, the work graph *and*
months of the user's own history.

The user-facing promise: **"You never have to say it twice, and you only hear from Focusflow when it matters."**

---

## Pillar 2: Memory ("You never have to say it twice")

### Model (focusflow.db, schema v4)

`memories`: one row per thing the human said or decided that future work should know.

| column | meaning |
|---|---|
| `id` | short id (8 hex) |
| `kind` | `answer` (reply to an agent's question), `instruction` (you told a task to do something), `decision` (recorded explicitly), `note` |
| `scope` / `task_slug` / `project_slug` | `task`, `project` or `global`; a project scope applies to every task in that project |
| `question` | the agent's question it answers (for `answer`), trimmed |
| `body` | what you said, trimmed, without the dashboard's delivery cues |
| `source` | `message`, `do`, `dashboard`, `agent`, `cli` |
| `pinned` | always injected |
| `status` | `active` or `forgotten` (never hard-deleted by default) |
| `created_at` | RFC3339 |

### Capture (automatic, zero effort)
- `focusflow message user/<task> "…"` sent **by the human** (not from inside a task session): recorded. If the task's
  latest message to the human came after your previous message to it, this is an **answer** and the question is stored.
- `focusflow do <task> --with "…"` (including `--auto`): recorded as an instruction/answer the same way.
- The dashboard's direct headless continuation (which bypasses the CLI) calls `focusflow remember`.
- Messages to a **project assistant** are scoped to the project automatically.
- Inside a terminal session, the skill tells the agent to run `focusflow remember "…"` whenever the user states a
  decision or preference that should outlive the session.

### Recall (automatic)
At every session start, the hook injects a compact **"What you already know"** block into the session: pinned and
global memories, the project's memories, then this task's memories (newest first, capped at about 4 KB), each with
its date and the question it answered. The agent is told not to re-ask what is answered there.

### Control (the user stays in charge)
- `focusflow remember "<text>" [--task <slug> | --project <slug> | --global] [--kind decision|note] [--pin]`
- `focusflow memories [--task|--project|--global|--all] [--json]`, `focusflow forget <id>`, `focusflow memory pin|unpin|promote <id>`
- In the app: a **Memory** page per project and a global one: search, pin, promote to project, forget.

---

## Pillar 1: Attention ("Only when it matters")

### Ranking
Each item on Home gets a score and plain-language reasons (shown as "Why is this here?"):

`score = kind weight + urgency + priority + age + blocking + learned bias`

- kind weight: question 60, failed run 55, review 50, stalled 45, overdue 40, waiting 20
- urgency +30 (urgent message); priority high +10, medium +4; age +2 per hour, capped at 20
- blocking +10 per other task whose `waiting_on` names this task (capped 20)
- learned bias: from how fast you usually act on this kind in this project (see below), between −10 and +10

### New signals
- **Stalled**: a background run with no transcript activity for 15 minutes, or a sent message that is still
  undelivered after the stall window.

### Snooze and dismiss
Snooze an item for 1 hour / until tomorrow morning / until something new happens; dismiss it for good. These are
events in the dashboard's event log (`attention.snoozed`, `attention.dismissed`), so they survive restarts, and an
item comes back automatically when its content changes (a new question, a new failure).

### Learning (local, private)
When you act on an item (approve, reply, open), the dashboard records `attention.acted` with how long it had been
waiting. The median time-to-act per kind and project becomes the bias: what you always handle fast rises; what you
consistently leave sinks. Nothing leaves the machine.

### Notifications and quiet hours
Banners are sent only for items above a threshold (urgent questions, failed runs, reviews after a long run).
During **quiet hours** (tunable, default 22:00–08:00) nothing is announced; a single summary is sent when they end.

---

## Build order
1. Go: schema v4 `memories`, store functions, capture in `message` and `do --with`, `remember/memories/forget/memory`
   commands, injection in the session-start hook, skill update, tests.
2. Dashboard: capture on headless continuation; `/api/memories` (read) and actions (pin, promote, forget via CLI).
3. Dashboard: attention scoring and reasons, stalled signal, snooze/dismiss events, learned bias, notification
   gating and quiet hours, tests.
4. UI: Home ranking with "why", snooze menu, Memory pages (lands with the redesign).

## Status (2026-09-25)

Shipped: schema v4 `memories`; automatic capture from `message`, `do --with` and the dashboard's headless
continuation; `remember`, `memories`, `memory pin|unpin|promote|restore|forget`, `forget`; recall injected at session
start; skill §4.17; `/api/memories` routes and a Memory tab. Attention: scoring with reasons, stalled runs, blocking,
snooze/dismiss/restore events, learned bias from time-to-act, quiet hours gate (`quiet_hours`, default 22:00-08:00;
`stall_minutes`, default 15). The polished UI for both lands with the redesign.
