# Focusflow: positioning, moat and IP

Written 2026-09-25. Companion to `DESIGN-AUDIT.md` (what the app is today) and `RESEARCH.md` (the market). This is a
strategy draft: its claims are for you to test, not settled facts.

---

## 1. The uncomfortable starting fact

Running agents is becoming a commodity, and the model vendors are shipping the basics themselves. In 2026 Anthropic
added a multi-session desktop app, background sessions, an "agents waiting for you" view and scheduled cloud
routines. Worktree-per-agent boards (Conductor, Vibe Kanban, Nimbalyst, Claude Squad…) are plentiful, and one of the
best known was wound down within a year.

**So the moat cannot be "we run many Claude sessions and show them on a board".** Anything built on session
management alone will be copied by the first party for free.

## 2. Where the value actually sits

Every source in the research names the same unsolved problem: **the human is the bottleneck.** Agents produce work
faster than a person can direct, check and remember it. The scarce resource is not compute; it is **your attention
and your judgement**.

Focusflow's defensible territory is the layer *above* sessions:

> **Focusflow is the chief of staff for your agents.** It decides what needs you, remembers everything so you do not
> have to, and turns each of your decisions into better work next time.

Three jobs, in order of how hard they are to copy:

1. **Direct attention**: one ranked place for everything that needs a human (Needs you); silence otherwise.
2. **Keep memory**: work outlives sessions (briefs, notes, decisions, knowledge base, the full event history).
3. **Learn judgement**: every Approve, Request changes, reply and priority change is a labelled decision about your
   standards, and it can be fed back to every future agent.

## 3. The moat, layer by layer

| Layer | What it is | Why it compounds | Status today |
|---|---|---|---|
| **Personal work graph** | Tasks, projects, briefs, notes, messages, decisions, timestamps, all local and event-sourced | The longer you use it, the richer the context each agent starts with. Switching away means losing that history | Built (SQLite + event log + briefs/notes) |
| **Judgement loop** | Review → Approve / Request changes → knowledge-base sweep | Your decisions become "house rules" agents inherit; quality rises with use. Nobody else captures *why* you accepted work | Half built (approve + KB sweep; no "request changes" reasons, no rules distilled yet) |
| **Attention engine** | Ranking what needs you: reviews, questions, failures, deadlines, stalls | Tuned by your own responses (what you act on fast, what you ignore) | Built (rule-based Needs you); learning not yet |
| **Agent-to-human protocol** | The message bus: addressable tasks and projects, delivery receipts, urgency, hooks that inject context, stop-hook nudges | A neutral protocol can work across harnesses (Claude, Codex, future ones). A first party will only ever support its own agents | Built for Claude; Codex partly |
| **Project assistants** | One permanent coordinator per project that triages new asks, creates scoped subtasks and checks in on a schedule | Encodes delegation, not just execution; it improves as the work graph grows | Built (coordinator + schedule) |
| **Trust by design** | Local-first, no account, readable files, receipts, Undo, backups, explicit consent for unattended runs | Trust is slow to earn and quick to lose; it is a brand asset, not a feature | Built; needs to be visible in the product |

**The strongest single claim:** *Focusflow gets better the more you use it, because it remembers your work and learns
your judgement, and it does this on your machine.* Session managers do not compound. Cloud agents compound for the
vendor, not for you.

## 4. Positioning

- **Category:** personal agent operations: a *control room*, not a code editor and not a to-do list.
- **For** people who run several AI agents across several projects and want to stay in charge without babysitting.
- **Unlike** session boards (which show you agents) and cloud engineers (which take the work away), **Focusflow**
  manages *the work itself*: it tells you when you are needed, remembers everything, and learns your standards.
- **Proof points in the product:** Needs you, Approve with receipts, project assistants, messages that start work,
  everything local.

Tagline candidates (to test):
1. *Hand it off. Stay in charge.*
2. *Your agents' chief of staff.*
3. *Only step in where you're needed.*
4. *Work that remembers.*

## 5. Ease for beginners and experts, from one product

The same model at two depths. The beginner never sees the machinery; the expert can reach all of it.

| | Beginner sees | Expert can also |
|---|---|---|
| Starting work | "New task" → type what you want → **Start** | Choose the harness, worktree, permissions, a playbook |
| Watching | A plain sentence: "Working in the background · editing README.md · 4m" | Open the live session, read the log, jump to the transcript |
| Deciding | **Approve** / **Request changes** with a summary | Diff, tests, rules that were applied, re-run with a new instruction |
| Organising | Home, projects, Up next | Saved views, bulk actions, ⌘K for everything, CLI and API |
| Automating | "Check in on this project every day" | Playbooks, schedules, hooks, custom assistants |

Rule of thumb for every screen: **one obvious action; everything else one step away (menu, ⌘K, or the task page).**

## 6. What is ownable (the "IP")

Be realistic about software IP:
- **Patents** on workflow software are slow, expensive and often weak. Not the primary moat. If you do pursue one,
  the most novel candidate is the *judgement loop*: turning human review decisions on agent output into distilled
  rules injected into future agent sessions, combined with attention ranking. Get advice before any public
  disclosure, since publishing can limit patent options.
- **Brand and design system**: the name, the vocabulary (Needs you, Up next, Approve, project assistant), the calm
  visual language. Protect with a trademark search and consistent use.
- **Open protocol, owned implementation**: publish the agent-to-human message protocol (addresses, receipts,
  urgency, hooks) as an open spec, and be its best implementation. Being the standard is a moat; being the only
  implementer is not.
- **Data format**: a documented, portable work-graph format (Markdown + SQLite + event log). Portability sounds like
  giving the moat away, but it earns trust. The moat is what the product *does* with the data, not holding it hostage.
- **Accumulated know-how**: playbooks, templates, and distilled rules that users can share.

## 7. What to build to deepen the moat (maps to the redesign tasks)

1. **Request changes, with a reason** → a *Rules* page where distilled rules from your decisions are listed, edited
   and applied per project. *(the judgement loop; highest priority after the redesign)*
2. **Proof of work on every review**: summary, files changed, tests, rules applied. Makes approval fast, so users
   route more work through Focusflow.
3. **Attention engine v2**: learn from what you act on; quiet hours; one daily digest.
4. **Harness-neutral**: first-class Codex and any CLI agent through the protocol; worktree isolation per task.
5. **Away-from-desk approvals**: mobile companion or notification actions (Approve / Reply from the banner).
6. **Weekly review**: generated from the event log; shows what shipped, what stalled, and how your rules changed.

## 8. Risks and honest counter-arguments

- **First-party absorption**: Anthropic adds tasks, memory and approvals. *Mitigation:* be neutral across vendors and
  local-first, and go deeper on judgement and memory than a general tool will.
- **Solo market is small**: *Mitigation:* the same core supports a team edition later (shared projects, rules as
  team standards).
- **Unattended runs are risky**: one bad run damages trust. *Mitigation:* per-project run permissions, limits,
  worktrees, and receipts are core product, not settings.
- **Mac-only today**: acceptable for a wedge; the server is already cross-platform Node + Go.

## 9. How to tell if the moat is real (metrics)

- Share of agent work that reaches **Approve without a terminal being opened** (the north star from the first audit).
- **Time from "needs you" to action**: falling means the attention engine works.
- **Request-changes rate per project over time**: falling means the judgement loop works.
- **Retention after 30 days**, and how much memory each user has accumulated (tasks with briefs, notes and rules).
