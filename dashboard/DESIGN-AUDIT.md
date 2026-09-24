# Focusflow: full product audit (design, UX, naming, architecture)

Audited 2026-09-25, against `aca0b98`. Method: a walk through every screen on realistic data (light and dark, wide and
narrow), a line-by-line read of `public/{index.html,app.js,style.css,logic.js}`, the server routes and data model, the
Mac app, the CLI surface, and the live database (97 tasks: 16 in progress, 22 backlog, 59 done, 6 projects).

This is the *before* picture. Section 9 proposes the direction; nothing here has been changed.

---

## 0. The one-paragraph verdict

Focusflow works and is honest, but it looks and feels like a **developer's control panel**: every task is a card with
three or four pill buttons, colour is used for everything, and the vocabulary comes straight from the CLI
(`headless`, `coordinator`, `harness`, `stage`, slugs). A first-time user is shown *machinery* before they are shown
*what it is for*. The product's real promise is "hand work to Claude, come back, and only act where needed"; the
interface currently makes you operate the machinery yourself. The redesign should hide the machinery, lead with the
promise, and give the user one calm place to look.

---

## 1. What we show: every control, and where

**Header (always visible): 9 controls.** Five view tabs (Needs you, Board, Projects, Archive, Inbox), search, a
"Sound" checkbox, `?`, Refresh, Capture. A stats strip sits under it (in progress / paused / in review / done).

| Surface | Controls | Notes |
|---|---|---|
| Board, top | Quick-add input + Add; 3 filters + Clear | Fourth way to create something (see §3) |
| Board card | 2–4 buttons **per card**: Start/Open terminal/Show terminal · Pause · Back to backlog · Ready for review · Approve · Move to Next · Resume work · Reopen | With 16 tasks in progress that is ~48 buttons on one screen |
| Project row | Message · Open terminal · Pause all · Schedule… · Archive, plus an "Add a subtask" form and the task cards | Five buttons per project, each ambiguous (§4) |
| Needs you row | Approve · Open · Reply · Cancel run · Watch | Good: one or two, and the primary is clear |
| Inbox | Mark all read; a row per thread; composer: Send · Leave for later · (Run headless…) | Duplicates Needs you's questions |
| Drawer | Close, Open terminal, 1–3 stage buttons, chips, 4 tabs, composer buttons, Cancel run, Details/Session folds | Two "open" concepts in one header |
| Dialogs | Capture (Choose folder…, Cancel, Save & close, Save & open); New project (Choose folder…, Cancel, Create project); Legend (Got it) | "Save & close" vs "Save & open" is unclear |
| Toasts / banners | Undo, ×, Dismiss, digest: See what needs you / Dismiss | Fine |
| Footer | task count, "Updated …", health chip | Health chip opens a toast, not a page |
| Mac app | Menu: Reload, Toggle Mini Board, Mini Board Always on Top; Dock badge; mini window | No menu items for Help, Capture, Search, Preferences |

**Count: ~45 distinct labelled controls; 5 top-level views; 3 dialogs; 1 drawer; 1 mini window.** There is no
Settings screen at all (sound, notifications, terminal choice, folders, schedule defaults all live in
`instance.json` or a checkbox in the header).

**Missing controls a user will look for:** Settings/Preferences; Help/About; Undo history; a Today view; bulk select
(archive/approve several); edit task name/priority/due/notes from the UI (today you can only read them); set due
date, tags, assignee; delete/restore; open the project's folder in Finder/editor; copy a task's link; keyboard
navigation; sign-out-style "pause everything".

## 2. Where we show them (placement problems)

- **Actions live on the object, in bulk.** Every card repeats its buttons. Repetition is the main source of clutter;
  the fix is progressive disclosure: one primary action per card, everything else in a `…` menu or the detail view.
- **The same action is offered in many places.** Approve appears on the Review card, the Needs you row, the drawer,
  and (as "Approved…") the toast. Chat appears in the drawer and in the Inbox pane. Open terminal appears on every
  card, project row and drawer.
- **The primary button is inconsistent.** On Board cards the first button is the terminal; on Review it is Approve;
  on Needs you it is Reply/Approve; in the drawer the terminal is top-right and the stage action is second row.
- **The header mixes three levels**: navigation (tabs), tools (search, sound, help, refresh) and the main creation
  action (Capture). Sound and Refresh do not belong at this level.

## 3. How we categorise things

Four separate classification systems are visible at once:

1. **Stage** (Backlog, Next, In progress, Paused, Review, Done), shown as lane, as chip and as verb.
2. **Priority** (high/medium/low), shown as a coloured chip.
3. **Runtime state** (open in terminal / headless run / live / working / idle), shown as chips *and* marks (`*`, `!`, `W`, `D`).
4. **Ownership** (project vs "No project", coordinator tasks, playbooks, owners), which the UI mostly hides.

Problems:
- **Backlog is not on the Board**, but is a stage. Users must go to Projects to find ideas. "Where did my task go?" is the top predictable confusion.
- **"Next" is jargon**; it is really "Up next" / "Queued".
- **Paused vs Backlog vs Archived vs Done** are four ways of "not working on it", with fuzzy differences.
- **Four ways to create**: Capture dialog, quick-add, "Add a subtask" (per project), "+ New project".
- **Status and runtime are two axes** (correct) but rendered with the same visual weight, so a card shows 5–7 chips.
- **Projects are a filter, a grouping and a hidden agent**. The "coordinator" concept is powerful and unexplained.

## 4. Naming audit (what the words say)

The rule: name things by *what the user gets*, not by what the machine does. Current → proposed:

| Today | Problem | Proposed |
|---|---|---|
| Focus / Show terminal / Open terminal / Start in terminal | Four labels for "bring up the live session"; "terminal" is a tool, not an outcome | **Open session** (a live conversation with Claude). One label, one behaviour; the tooltip says "Opens the task in a terminal window" |
| Headless run / "running headless" / Run headless… | Pure jargon | **Working in the background** |
| Needs you | Good | keep; add a count |
| Board | Generic | **Tasks** (or *Board* under Tasks) |
| Next | Ambiguous | **Up next** |
| Review / Ready for review / Approve | Three words for one step | Stage **Review**; action **Approve**; secondary **Request changes** |
| Paused / Pause / Pause all / Resume work | Fine, but "Pause all" is destructive-sounding | **Pause** / **Pause project** |
| Leave for later | Unclear what happens | **Save for later** (delivered when the task next runs) |
| Coordinator / Message (project) | Invisible concept | **Project assistant**; button **Ask the project** |
| Schedule… | What is scheduled? | **Check in on a schedule** |
| Capture | Generic | **New task** (with ⌘N) |
| Archive | Fine; but same word for tasks and projects with different effects | keep; show "Restore" |
| Inbox | Fine, but duplicated by Needs you | fold into **Inbox**, with Needs you as its default filter |
| Harness / "Runs in" / Alerts / Signals | Internal | "Runs with Claude", "Heads-up" |
| Health: 2 warnings | Alarming, opens a toast | **Status** page under Help |
| slugs (`website`, `pricing`) shown in mono type | Implementation detail | hide; show in Details only |

## 5. How the views look (screen by screen)

- **Needs you** *(best screen)*: a short, ordered list with a coloured left rule and one clear action. Weakness:
  four kinds of item share one look; there is no "today", no summary sentence, and counts duplicate the tab badge.
- **Board**: five equal lanes of dense cards. At 97 tasks (16 in progress) it becomes a wall of pills. Lane headers
  carry a glyph and count only. Filters and quick-add stack above it, so the actual board starts ~200px down.
- **Projects**: an accordion. Each row leads with five action pills before the content. Expanding shows an
  "add subtask" input and a card grid. The row does not answer "how is this project doing?" (no progress, no next step).
- **Archive**: two flat lists. Fine, low priority.
- **Inbox**: two panes, good bones; the empty right pane is a large blank rectangle; no avatars/status, no
  grouping by "waiting on you".
- **Drawer**: overlay (or side pane ≥1400px). Header holds title, slug, state text, terminal button and Close; then a
  summary line; then stage buttons; then tabs. Chat is first, which is right, but it competes with four rows of chrome.
- **Dialogs**: standard, functional, unstyled beyond the base card.
- **Mini board**: the board squeezed into 380px; useful concept, but it shows the same busy cards.
- **Mac window**: a browser page in a native frame. No native toolbar, no sidebar, no unified title bar.

## 6. How the flow works

Open → lands on **Needs you** (good) → to act you jump to Board/Inbox → click card → drawer → act → drawer closes →
back. Observed friction:
- **No spine.** Five peer tabs, no sense of "Today → Projects → Task".
- **Creating work is disconnected from running it**: capture → find it in Backlog under Projects → Move to Next →
  Start. The happy path has four hops.
- **Send-message = start-work** is the product's best idea, but the user is never told a run started or where to
  watch it, except by a chip.
- **Undo exists only for three actions**; everything else is immediate.
- **First run** is a single empty-state card; there is no tour, sample data, or permission explanation, although runs
  execute with permission prompts **off**, which the user must understand and consent to once, prominently.

## 7. How the components look (design system)

- **Type**: system stack (`-apple-system…`), 15px base, 12.5px chips, 11–13px meta. Two weights in practice (400/600).
  No type scale; sizes are set ad hoc (`11px`, `12px`, `12.5px`, `13px`, `14px`, `15px`, `16px`, `17px`, `18px`).
- **Spacing**: mostly 4/6/8/10/12/14/16/24px, unsystematic; cards feel tight (10–12px padding, 8px gaps).
- **Radius**: 4, 8, 10, 12, 14px and 999px pills. Six values.
- **Buttons**: one look (outlined pill/rounded rect) for ~45 controls; only `.primary` differs (filled accent).
  There is no tertiary/ghost/icon button, no menu, no segmented control, no tooltip component (native `title`).
- **Chips**: outline pills in seven colours doing three jobs (priority, stage, state).
- **Marks**: 18px filled squares with single letters (`!`, `W`, `*`, `D`), which need a legend to decode.
- **Icons**: none, except unicode glyphs (◔ ▶ ⏸ ◎ ✓ ⌨ ⚙). These render differently per OS font.
- **Motion**: a 1.2s "landed" outline and hover borders. Nothing else.

## 8. Colour and hierarchy

- **Palette**: warm-grey light theme (`#f6f5f2` bg, `#2f5d50` forest accent) and a near-black dark theme. Both are
  chosen by the OS setting; **the app opens dark for dark-mode users**, which is not the requested light-first look.
- **Semantic colours**: high (red), medium (amber), low (green), stale (red, same as high), waiting (purple), live
  (green, same family as low and accent), due (orange). Red means both "high priority" and "stale/failed";
  green means "low priority", "live" and "primary action". **Colour carries too many meanings, so it carries none.**
- **Contrast**: passes WCAG AA everywhere measured (lowest 4.58:1). Good; keep as a hard requirement.
- **Hierarchy**: on a card the title (15px/600) is only marginally stronger than the chips and buttons (12–13px, same
  border weight). The eye lands on the filled Approve button and on the coloured priority chip, not on the title or
  the thing that needs attention. Lane headers (uppercase 13px grey) are quieter than the cards they label, which is
  right, but nothing tells you which lane matters right now.
- **Density**: roughly 4 interactive elements per 100px of vertical space on the Board; a calm app targets 1–2.

## 9. How it feels

Competent, fast, trustworthy, and slightly intimidating: **a cockpit rather than a workspace**. Strengths: instant
responses, honest receipts (sent → delivered), safe defaults (nothing opens a terminal unasked), Undo, a real Needs you
page. Weaknesses: it assumes you already know the model (stages, sessions, runs, coordinator); it rewards power users
and does not welcome new ones; nothing celebrates finishing work; there is no sense of place or brand beyond the name.

---

## 10. Beyond the screens: infrastructure, data and process audit

| Area | Today | Finding / risk | Recommendation |
|---|---|---|---|
| Architecture | Go CLI (SQLite, pure Go) is the source of truth; Node dashboard reads it read-only and shells out to the CLI for writes; event log + disposable cache; launchd job; Swift WKWebView shell | Sound separation. The dashboard writes only through the CLI, which is right | Keep. Formalise a small JSON API contract (versioned) between CLI and dashboard |
| Frontend code | One 1,556-line `app.js` with global state, hand-built DOM, no modules | Every redesign change touches the same file; hard to test beyond pure helpers | Split into ES modules (state, api, views, components) *without* adding a build step; keep zero-dependency ethos |
| CSS | One 328-line file, ad hoc values, colours as flat tokens | No spacing/type/radius scale; dark mode is duplicated by hand | Introduce a token layer (colour, space, type, radius, shadow, motion) and rebuild components on it |
| Update model | Polling: 30s full reload (8 requests), 5s inbox/thread/run polls | Wasteful, and a state change can take up to 30s to appear | Server-Sent Events from the dashboard server ("something changed"), fall back to polling |
| Notifications | `osascript display notification` | **Banners cannot be clicked through** and open Script Editor; no actions | Move notifications into the Swift app (`UNUserNotificationCenter`): click opens the task, actions Approve/Reply |
| Dock / menu bar | Dock badge (polled every 15s) | No menu-bar item, no Help/Preferences/New task menu entries, no keyboard shortcut for New task in the native menu | Add native menu items, global shortcut, optional menu-bar extra |
| Mac app | Built locally by `make app`, ad hoc | **Unsigned, un-notarised**; auto-launch not offered; no first-run permission explainer | Sign + notarise for distribution; "Open at login"; first-run consent screen |
| Safety | Headless runs use `--dangerously-skip-permissions` | Correctly confirmed once per task, but the wording is technical and the risk is not explained on first launch | A first-run "How runs work and what they can touch" screen; per-project "allow unattended runs" switch |
| Data | `focusflow.db` schema v3 (projects, playbooks, tasks, workdirs, bus_*, owners, task_tags); cache with tasks/messages/snapshots/headless_runs | Healthy; migrations versioned; daily backup job | Add an export (JSON/Markdown) and a visible "last backup" in Status |
| Security | Localhost only, token header, Host/Origin checks, token in URL fragment | Good for a single-user tool | Keep; add an explicit "Reset access token" in Settings |
| Settings | `instance.json` by hand | Users cannot change port, terminal app, notifications, working-window, due-soon days, sound | A real Settings screen |
| Observability | `/api/health`, `/api/doctor`, server log | No product analytics; the north-star metric in the first audit is not measured | Local-only event counters (opt-in), shown in a Status/Insights page |
| Quality | 350 dashboard + 380 Go tests; CI has gofmt/vet/staticcheck/race/coverage; headless-Chrome screenshots done by hand | **No automated visual or browser-flow tests**; layout regressions are caught by eye | Add a scripted screenshot suite (Chrome headless, sandbox data) and an interaction test for the top 5 journeys |
| Accessibility | Landmarks, focus ring, AA contrast, drawer focus trap, tablist roles | Colour-only encodings, glyph-only marks, no skip link, no keyboard board navigation, no reduced-motion for all animations | Adopt a checklist; full keyboard model (`j/k`, `⌘K` palette) |
| Docs | README, JOURNEYS, UX-AUDIT, `help.js` (token recovery only) | No in-app documentation | The About/Help section (§12) |
| Naming (repo) | Product "Focusflow"; internal words `flow`, `harness`, `coordinator`, `playbook`, `owner`, `stage`, `kb` | Mixed vocabulary between CLI, code, and UI | A one-page glossary; the UI uses only the user-facing terms |

---

## 11. What "stunning, calm, Notion-like, light" implies (design direction, for approval)

**Principles.** (1) *One thing to do at a time.* (2) *Show outcomes, hide machinery.* (3) *Whitespace is a feature.*
(4) *Colour is rare and means one thing.* (5) *Every screen has one primary action.* (6) *Words a non-developer
would use.*

**Information architecture.** Replace the five top tabs with a **left sidebar** (Notion/Linear pattern):
- **Home** (was Needs you): a greeting, a one-line summary ("2 ready to review · 1 question"), then the list.
- **Inbox** (messages) · **Tasks** (Board / List switcher; Backlog is a *view*, not hidden) · **Projects**
  (a page per project: brief, progress, up-next, running, done, ask-the-project) · **Archive**
- Bottom of sidebar: **Search ⌘K**, **New task ⌘N**, **Help & About**, **Settings**.
- Header only shows the page title, a view switcher and one primary button.

**Cards.** Title, one subtle line of meta ("Website · High · updated 2h ago"), a single status dot with a label.
Actions: one primary (contextual) plus a `…` menu for the rest. No per-card rows of buttons. Rich detail opens as a
centred page/peek panel rather than a cramped drawer.

**Visual system.** Light only by default (dark as an explicit option). Warm off-white canvas, white surfaces, hairline
borders, soft shadows. One accent (keep the forest green, refined) used for the primary action and focus only.
Status uses a small dot, never a coloured border. Priority becomes a quiet text label (or a bar), not a chip.
Typography: a clean humanist sans for UI (Inter or the system UI font tuned), a modest 5-step scale (12/14/16/20/28),
generous line-height and 24–32px section spacing, ~720px reading width for text pages. Real icons (SVG line set,
inline, no dependency), consistent 16/20px. Motion: 150–200ms fades/slides, honouring reduced-motion.

**First-run journey.** Welcome → "What is Focusflow" in three cards → choose a working folder (native picker) →
create the first project and task (with an example) → explain background runs and permissions once, clearly →
land on Home with a single suggested next action. Sample data can be offered and removed with one click.

**Naming.** Adopt §4 across UI, tooltips, CLI help and docs.

## 12. About and Help (new section, one place)

**About** (reads like a short product essay, date-stamped):
1. *What Focusflow is.* Your personal control room for Claude Code work.
2. *Why not just run Claude Code?* One session is a conversation; real work is many sessions across projects.
   Focusflow adds memory (briefs, notes, knowledge base), queueing, unattended runs, review/approve, messaging,
   notifications and a single place to see what needs you.
3. *What exists in the market* (researched and cited, dated): agent runners and kanban orchestrators for coding
   agents, IDE background agents, hosted autonomous engineers, generic task managers. What each is good at and
   where each leaves a gap for a solo, local-first, Claude-Code-centred workflow.
4. *What we built and how it differs*: local-first, no account, Claude Code sessions as the unit of work, hooks-based
   context injection, message bus, stage model with approval, headless runs with receipts.
5. *The story of Flow → Focusflow*: what the earlier tool (`flow`) was, what we kept, what we replaced, how it was
   retired and migrated (dated timeline of iterations).
6. *Use cases built in today*, each with a "how to" link: solo builder with several repos; planning and delegating;
   overnight/background runs; review and approve; project assistants that triage new asks; scheduled check-ins;
   playbooks (repeatable runs); backups and recovery.
7. *Roadmap as vision* (bold, dated): see §13.

**Help** (real documentation): Getting started · Concepts (task, project, stage, session, background run, project
assistant, playbook, knowledge base) · How-to guides (one per journey) · Keyboard shortcuts · Troubleshooting
(server not running, token lost, terminal will not open, run stalled/failed, notifications silent, Dock badge
wrong, restore from backup) · FAQ · Status (health checks, backups, versions) · What's new (changelog).

## 13. Vision for the roadmap (to be written boldly, with research)

Directions to develop in the About page: *ambient progress* (Focusflow tells you when you are needed, otherwise
stays quiet); *agent teams per project* (assistants that plan, delegate and review across sub-agents); *proof of
work* (every completed task carries a summary, diff and test evidence you can approve in one glance); *time-aware
planning* (due dates, capacity, "today" plans, weekly review generated from the event log); *voice and mobile
companion* (approve from a phone, dictate a task); *shareable playbooks* and a template gallery; *knowledge that
compounds* (the KB becomes searchable memory that agents cite); *a team edition* built on the same local-first core.
Each item will carry a date and a status (shipped / building / exploring).

---

## 14. Proposed order of work

1. **Foundation**: design tokens, type/space/radius scale, icon set, component library (button, menu, chip→status
   dot, card, list row, tabs, dialog, toast, tooltip, empty state) on a modular front end; screenshot test suite.
2. **Shell**: sidebar navigation, page headers, ⌘K palette, Settings screen, native menus and notifications.
3. **Screens**: Home, Tasks (board + list), Task page, Projects and project page, Inbox, Archive: rebuilt on the system.
4. **First run and About/Help**: onboarding journey; About essay with market research; documentation and troubleshooting;
   What's new.
5. **Polish and proof**: motion, accessibility pass, empty/error states, dark option, sign and notarise the app,
   performance (SSE), visual and journey tests, final audit against this document.

## 15. Decisions (answered 2026-09-25)

Sidebar navigation; **light only**; ES modules with bundled Inter; edit tasks in the app; live market research
(done: `RESEARCH.md`); Flow history kept high-level; plus "find more use cases for organisation, cleanliness and
freedom" (done: `RESEARCH.md` §3). The list below is the original question set.


1. **Sidebar navigation** replacing the top tabs (recommended), or keep tabs?
2. **Light-only**, or light default plus an optional dark mode (recommended)?
3. **Front end**: stay dependency-free with ES modules (recommended, matches the project), or adopt a framework?
4. **Font**: bundle Inter (offline, ~100KB) or use the system font? (Recommended: bundle Inter for a consistent look.)
5. **Scope of editing in the UI**: allow editing task name, priority, due date, notes from the app (recommended), or
   stay read-and-act only?
6. **Market research**: do it live now with web search and cite sources in About (recommended), dated today.
7. **Flow history**: which details are safe to publish in About (it is a personal tool): keep it high-level?

---

## 16. Status after the redesign (2026-09-25)

**Shipped:** light-only design system (tokens, Inter, 5-step type scale, one accent, status dots instead of coloured
chips, line icons); left sidebar with Home · Inbox · Tasks · Projects · Memory, pinned projects, Archive, Settings,
About & Help; ⌘K palette and shortcuts; Home as ranked attention with reasons, inline reply, approve, snooze;
Tasks board (six stages, drag) and list (group, filter, bulk); task page with editable name, stage, priority, due,
waiting-on and tags, a run panel with stop and log, and Conversation · Memory · Brief · Notes tabs; projects grid with
progress and a project page with tasks, assistant, brief and memory; two-pane Inbox; Memory page; Settings
(notifications, quiet hours, stuck threshold, stale/due days, terminal app, status, backups); About (what it is, why
not just Claude Code, the landscape, what we built, Flow → Focusflow, vision) and Help (getting started, concepts,
guides, shortcuts, CLI, troubleshooting, backups, FAQ, what's new), all dated; native Mac menus (File/Edit/Go/Help)
with the app opening on Home.

**Naming changes:** Focus/Resume/Start → *Open in terminal*; headless → *background run*; Next → *Up next*;
Review → *Ready for review*; coordinator → *project assistant*; Capture → *New task*; Queue only → handled
automatically (messages go to the open terminal, else start a background run).

**Correctness fixed on the way:** background runs that exited normally were marked `dead` (so Home would have called
every answered message a failure); runs now record their exit code (`finished` vs `dead`) and stopping a run reaches
claude itself. All background runs now go through `focusflow do --auto`, so they are tracked, stoppable and remembered.

**Verified:** unit tests, and `npm run e2e`, which drives 13 real journeys in headless Chrome (approve and undo,
snooze, inline reply and memory, clean-run status, quick add with shorthand, editing, memory, drag between stages,
⌘K, settings, every page without console errors) plus 400px and mini-window layouts without horizontal overflow.

**Still open:** removing `/classic`; judgement loop (Request changes → house rules); proof of work on reviews;
worktree per task; click-through notifications from the Mac app; signing and notarising the app.
