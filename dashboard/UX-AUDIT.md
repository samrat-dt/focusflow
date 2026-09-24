# Focusflow dashboard — UX / UI audit

Audited 2026-09-24 from a product-management and a product-design point of view. Evidence: screenshots of every
screen (dark and light, 1500px, 420px and the 380px mini board) against realistic data, a code read of
`public/app.js`, `index.html`, `style.css`, computed WCAG contrast, and the real flows (capture → next → in progress →
review → approve; message a task; message a project; archive).

## 1. What the product is for

**Job to be done:** "I hand work to Claude sessions, come back later, and only step in where I'm needed."

The dashboard should answer four questions, in this order, in under five seconds:

1. **What needs me?** (work in review, unanswered questions from agents, overdue / waiting items)
2. **What is running without me?** (headless runs, open terminals, and whether they are healthy)
3. **What is next?** (the literal queue, per project)
4. **What got done / what is this costing?** (throughput, review latency, tokens)

Today it answers (3) well, (2) partly, (1) only by visiting three places, and (4) barely.

**Suggested north-star:** tasks approved per week that needed no terminal. Supporting metrics, all derivable from the
existing event log: time from *review* to *approve*, unanswered-question age, headless run success rate, number of
messages per approved task, share of tasks that reach review without a human-opened terminal.

## 2. What already works (keep it)

- The six-stage model, and the separate **terminal-open / running-headless** marker: it matches how people actually work.
- "Send is headless; a terminal opens only when you click one": a clear, trust-building principle.
- Sent → delivered → replied receipts on messages.
- Coordinator-per-project as the way to message a project.
- Visual system: calm, consistent dark and light themes, generous spacing; **all text and chip colours pass WCAG AA**
  (lowest is 4.58:1 muted-on-background in light mode).
- Fast: everything renders instantly from a local cache; search on `/`.

## 3. Findings

Severity: **P0** breaks a task or loses work · **P1** clear friction on a core flow · **P2** polish / delight.

### 3.1 Bugs and correctness

| # | Sev | Finding | Evidence |
|---|---|---|---|
| B1 | P0 | **Background refresh destroys what you are typing.** Every 30s the whole board / projects view is re-rendered, so text typed into "Add a subtask…", an open Schedule picker, and keyboard focus on a card button are lost. | `reload()` → `renderProjects()/renderBoard()` use `replaceChildren()` |
| B2 | P0 | **First headless send failed** (`do requires a task ref`): the dashboard passed flags before the task name. Fixed in `751cde8`, with a regression test. | real CLI output |
| B3 | P1 | **Footer still shows "sync: not configured"** (a removed feature) plus "gaps" and "last snapshot" — event-log jargon that means nothing to a user. | every screenshot |
| B4 | P1 | **Mini board clips its content**: cards and their primary buttons run off the right edge at 380px. The always-on-top window is unusable as a glance surface. | mini screenshot |
| B5 | P1 | **Not responsive below ~900px**: at 420px the tab bar loses "Inbox", card action buttons and the stats strip are clipped. The app window is resizable, so users will hit this. | narrow screenshot |
| B6 | P1 | **Drawer is `aria-modal` but has no focus trap**: Tab walks into the board behind it. Escape is the only exit. | no Tab handling in code |
| B7 | P1 | **Errors show raw CLI output** ("error: do requires a task ref") and error toasts never dismiss. | `toast()` |
| B8 | P2 | Details show raw ISO timestamps ("2026-09-24T22:43:44+05:30 (1m ago)"). | drawer screenshot |
| B9 | P2 | `aria-live` on the whole board that is replaced wholesale every 30s: screen readers re-announce everything. | `#board[aria-live]` |
| B10 | P2 | Small hit targets: card/stage buttons are ~22px tall (WCAG 2.2 asks for ≥24px; 44px is comfortable). | `.card-btn` |

### 3.2 Information architecture and flow (product view)

- **No "Needs you" surface (P1).** Approvals live in the Review lane, questions in the Inbox tab, waiting-on and
  overdue as tiny chips. Someone who opens the app cannot see in one glance what is blocked on them. *Proposal:* a
  top-of-board strip / default landing "Needs you" with three counts and the items: **Review (n) · Questions (n) ·
  Overdue/Waiting (n)**, each one click to act. This is the single highest-value change.
- **Inbox is a list of previews, not a conversation (P1).** No inline reply, urgent messages look like normal ones, no
  stage or project on the row, the unread badge exists only on the tab. *Proposal:* two-pane (list + thread), reply in
  place, urgent flagged, project/stage chips, mark-all-read, keyboard `j/k`.
- **Delegation is opaque (P1).** "⚙ running headless" says nothing about *what* it is doing or whether it is stuck.
  Trust in "delegate and forget" depends on visibility. *Proposal:* a run panel — last assistant message, elapsed time,
  files touched, log tail, and a Cancel — plus a completion notification.
- **Primary action language is inconsistent (P1).** Start / Resume / Focus / Reopen / Queue only / Unpause / Send to
  review all appear, three of them open terminals and the rest do not. On a **Review** card the big button is
  "Resume" (open a terminal) while the action that matters is **Approve**. *Proposal:* one verb per outcome —
  **Open terminal**, **Send**, **Approve**, **Pause**, **Resume work** (status only); make Approve the visual primary
  in Review; rename "Queue only" to "Leave for later".
- **Irreversible actions have no safety net (P1).** Approve (runs the knowledge-base sweep), Archive and "Pause all"
  act immediately. *Proposal:* toasts with **Undo** (5–8s), plus a confirm only for "Pause all".
- **Onboarding and empty states are missing (P1).** A new user sees five lanes reading "Nothing here." Nothing explains
  the stages, the ⌨/⚙ markers, or that messages run headlessly. *Proposal:* an empty-state that offers "Create a
  project", a one-screen legend of stages and markers, and tooltips on every chip.
- **The board has no direct manipulation (P2).** No drag between lanes (Next → In progress → Review), no multi-select,
  no keyboard navigation, no filters (project, priority, tag, waiting) or saved views. Kanban users expect these.
- **Capture is heavyweight (P2).** A modal with project, priority and note for every idea. *Proposal:* quick-add at the
  top of the Next lane and inside each project ("title ⏎"), with the last-used project as default; keep the dialog
  for the detailed case; later, inline shorthand (`#project !high tomorrow`).
- **Projects rows say little (P2).** Counts as a sentence. *Proposal:* a progress bar (done/total), last activity,
  "next action" ("2 in review — approve"), stale indicator, sort by activity, expand-all.
- **New-project needs a working folder (P1, in progress).** Solved in `751cde8`: a **Choose folder…** button opens the
  native macOS chooser (which has its own New Folder). Follow-ups: default folder suggestion from the project name, and
  show the folder's git branch/remote when it is a repo.
- **The event log is unused (P2).** It knows everything that happened. *Proposal:* a **"Since you were away"** digest on
  open (approved, replied, failed runs), and a per-task timeline in the drawer.
- **Notifications are weak (P2).** macOS banners have no click-through; nothing in the Dock or menu bar. *Proposal:* a
  Dock badge in Focusflow.app for (review + unread), a menu-bar count, and banners that open the task.

### 3.3 Visual and interaction design

- **Hierarchy is flat.** Every card carries the same weight: a title, three rows of chips and one or two buttons. The
  *stage chip repeats the lane title* on the Board, and the project chip repeats the project on the Projects screen.
  Show chips only when they add information; reveal secondary buttons on hover/focus and keep one primary.
- **Colour carries meaning alone.** Stage, priority and signal share a small palette (paused orange vs medium yellow vs
  due orange). Add a shape or icon per stage (◔ ▶ ⏸ ✓) and keep colour redundant.
- **The drawer is a long form.** Chat, then three definition lists of mostly "—" (Details, Signals "No signals.",
  Session), then the brief. *Proposal:* hide empty rows, collapse Session by default, lead with a one-line status
  summary ("In review · high · Resume · updated 1m ago"), tabs for **Chat · Brief · Notes · Activity**.
- **The drawer covers the board** instead of sitting beside it; on a wide window use a split layout so the lane you
  came from stays visible.
- **Type scale is small and undifferentiated** (12px chips and metadata, 13px stats). Introduce a 3-step scale and
  raise chip text to 12.5–13px.
- **Motion is absent.** Approving or pausing makes a card vanish with no feedback. Add a 150ms transition, optimistic
  updates, and a subtle highlight where the card landed.
- **Every action reloads six endpoints.** Patch the affected task instead of refetching everything (also fixes B1).
- **Stats strip shows empty numbers** ("0 output tokens", "0s median reply"). Hide a metric until it has data; show
  tokens as cost trend rather than a raw count.
- **Jargon:** "Harness", "Auto-run", "Signals", "Terminal: not open". Prefer plain words ("Runs in: Claude Code",
  "Headless run", "Alerts").

### 3.4 Accessibility summary

Passes: language attribute, landmarks (`header`, `main`, `nav`, `search`), contrast, visible focus ring, dialog
labelling. Needs work: focus trap (B6), targets (B10), tabs use `aria-pressed` instead of a `tablist`, live-region
noise (B9), colour-only encodings (§3.3), no skip link, keyboard shortcuts are undiscoverable (add a `?` sheet).

## 4. Prioritised roadmap

**Now — one week (correctness + trust)**
B1 keep state across refreshes · B3 clean footer · B4/B5 responsive layout and a proper mini board · B6 focus trap ·
B7 friendly errors · B8/B10 · Undo for Approve/Archive · make Approve the primary on Review · Dock badge.

**Next — two to four weeks (the "Needs you" experience)**
Needs-you strip/landing · two-pane inbox with inline reply and urgent styling · headless run panel with cancel and
completion notification · drawer redesign (tabs, hide empties, split view) · quick-add · filters and saved views ·
drag-and-drop and keyboard navigation · onboarding/empty states and a legend.

**Later**
"Since you were away" digest and task timelines · analytics page (throughput, review latency, cost) · project detail
pages with progress · natural-language capture · templates · menu-bar companion.

## 5. Instrumentation to add first

Emit (into the existing event log): `view.opened`, `action.clicked` (with result and latency), `review.approved`
(time in review), `message.sent` (mode), `run.finished` (success/fail, duration), `question.answered` (age when
answered). These give the north-star and tell you which improvements pay off.
