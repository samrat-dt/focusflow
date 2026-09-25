// The About section and the Help documentation. Markdown, rendered by ui.markdown(). Every page carries
// the date it was last reviewed; update it whenever the page changes.

export const UPDATED = '25 September 2026';

export const ABOUT = [
  { id: 'about', title: 'What is Focusflow', body: `# What is Focusflow

<p class="stamp">Updated ${UPDATED}</p>

**Focusflow is the place where you hand work to Claude and stay in charge of it.**

You describe what you want. Claude works on it, in the background or in a terminal you open. Focusflow keeps track of
every task, remembers everything you decide, tells you only when you're needed, and lets you approve the result.

It's built for anyone who works with Claude Code on more than one thing at a time: engineers running several agents
across repositories, and people who aren't engineers but use Claude to write, research and build.

## The idea in one line

> **Hand it off. Stay in charge. Never say it twice.**

## What you get

- **Home**: one calm list of what needs you right now (a question, a finished piece of work to approve, a run that
  got stuck), ranked, with the reason each item is there.
- **Tasks and projects**: your work, organised the way you think about it, not the way sessions happen to run.
- **Conversations that start work**: message any task and Claude gets going in the background. No terminal needed.
- **Memory**: every answer and instruction you give is remembered and handed to every future session.
- **Approval**: finished work waits for you. Approve it, or ask for changes.
- **Project assistants**: one permanent Claude per project that triages new asks and can check in on a schedule.
- **Local and private**: everything lives on your Mac, in plain files and a small database you own.
` },
  { id: 'why', title: 'Why not just Claude Code?', body: `# Why not just run Claude Code?

<p class="stamp">Updated ${UPDATED}</p>

Claude Code is superb at doing the work. Focusflow is about **managing** the work: many pieces of it, over days and
weeks, without you having to hold it all in your head.

| On its own, Claude Code… | With Focusflow… |
|---|---|
| A session is a conversation. When it ends, what you decided lives in a transcript. | Work lives in **tasks** with a brief, notes, a stage and a history that outlast any one session. |
| You re-explain your preferences in each new session. | **Memory**: every answer and instruction is captured and given to every later session, in scope. |
| You check each session to see whether it needs you. | **Home** ranks everything that needs you across every task and says why. Silence otherwise. |
| Background work finishes silently in a log. | You get a **notification**, a **Ready for review** item and an **Approve** button. |
| Talking to a session means being in its terminal. | **Message any task or project** from anywhere, with receipts: sent, delivered, replied. |
| Organising is up to you. | **Projects, stages, priorities, due dates, tags, filters, board and list views.** |
| Nothing stops a run from going quiet. | Runs that stop making progress are flagged as **Might be stuck**, with a Stop button. |

Focusflow doesn't replace Claude Code. Every task *is* a Claude Code session, and you can open it in a terminal at any
moment. Focusflow is the layer on top that turns sessions into managed work.
` },
  { id: 'market', title: 'The landscape today', body: `# The landscape today

<p class="stamp">Researched ${UPDATED}. Based on public vendor pages and reviews; details change quickly.</p>

Agentic coding moved in 2026 from "one chat in a terminal" to "many things in flight, and you as the orchestrator". Tools
fall into five groups:

| Kind | Examples | Great at | What's missing for this job |
|---|---|---|---|
| **Claude's own apps** | Claude Code desktop (multi-session sidebar, diffs, worktree per session, April 2026), Agent View and background sessions (research preview, May 2026), Routines (scheduled cloud runs) | Deep integration; runs in the cloud without your Mac | Organised around *sessions*, not *work*: no memory of your decisions across sessions, no approval step, no project-level assistant, no messaging with receipts |
| **Agent boards** | Vibe Kanban (now community-maintained), Nimbalyst, CodeAgentSwarm | A visual queue; a branch per card | Cards are prompts, not long-lived work with memory; you're still the coordinator |
| **Agent workspaces** | Conductor, Superset, Mux, Sculptor | Fast code review, isolation, remote hosts | Strong on reviewing *code*, light on planning, prioritising and remembering |
| **Terminal multiplexers** | Claude Squad, Paneflow, tmux scripts | Full control, no lock-in | You build and maintain the organisation yourself |
| **Hosted AI engineers** | Devin, Cursor background agents, Copilot and Codex agents | Fully unattended, cloud scale | Your context leaves your machine; not your Claude Code sessions |

**The gap every reviewer names:** the model is no longer the bottleneck; *the person is*. Seeing more agents doesn't help if
you still have to remember, check and chase each one.

Sources: [Nimbalyst, agent management tools 2026](https://nimbalyst.com/blog/best-agent-management-tools-2026/) ·
[Claude Code desktop redesign](https://blog.buildfastwithai.com/claude-code-desktop-redesign-2026) ·
[Claude Code Agent View](https://pasqualepillitteri.it/en/news/2384/claude-code-agent-view-cli-dashboard-sessions-2026) ·
[Claude Code desktop docs](https://code.claude.com/docs/en/desktop) ·
[Background agents compared](https://amux.io/guides/background-agents-compared/)
` },
  { id: 'approach', title: 'What we built', body: `# What we built, and what makes it different

<p class="stamp">Updated ${UPDATED}</p>

Focusflow is designed around the scarce thing: **your attention and your judgement**. Three ideas do the work.

## 1. It tells you when you're needed, and only then
Everything that could need you (questions, reviews, failed or stuck runs, deadlines, things you're waiting on) is
scored by urgency, priority, how long it has waited, what it blocks, and **what you usually act on quickly**. Each item
says why it's there. Snooze anything; it comes back on time, or as soon as something new happens. Quiet hours hold
non-urgent news for a single morning summary.

## 2. It remembers, so you never say it twice
Every message you send passes through Focusflow. When you answer an agent's question or give a task an instruction,
it's captured, with the question it answered, and handed to every later session in that task, that project, or
everywhere. Claude Code's own memory is written by the agent, per repository. Focusflow's is **written by you, about
your work, and shared across every session**. It grows every time you reply.

## 3. It learns your judgement
When you send work back, say what's wrong. Tick **Make this a house rule** and that standard is handed to every
future session in the project, which must check its work against it before calling it done. Reviews show your rules
as a checklist next to the proof of work. The more you review, the less you have to correct.

## 4. Work, not sessions
Tasks carry a brief, notes, a stage (Backlog → Up next → In progress → Ready for review → Done), priority, due date and
tags, and they belong to projects. Sessions come and go underneath. You can always drop into the live session in a
terminal, but you rarely need to.

## Use cases built in today

| You want to… | Focusflow gives you |
|---|---|
| **Hand off work and get on with your day** | Message a task; it runs in the background; you're notified when it needs you |
| **Approve finished work quickly** | Ready for review on Home, proof of work on the task, Approve (and Undo) |
| **Stop correcting the same mistakes** | Request changes → house rules every future session follows |
| **Answer an agent's question from anywhere** | Reply inline on Home or in the Inbox; the answer is remembered |
| **Stop repeating yourself** | Memory: answers, instructions and decisions, pinned or promoted to a whole project |
| **Juggle several projects** | Projects with progress, a board and list across everything, filters, bulk actions |
| **Delegate a vague ask** | Ask the project: its assistant creates or routes the right task |
| **Keep a project moving without you** | Scheduled check-ins by the project assistant |
| **Catch runs that went quiet** | Might be stuck, with Stop |
| **Watch or steer directly** | Open in terminal, any time |
| **Protect your focus** | Snooze, dismiss, quiet hours, urgent-only interruptions |
| **Never lose anything** | Local files, a nightly backup, Archive instead of delete |
` },
  { id: 'story', title: 'From Flow to Focusflow', body: `# From Flow to Focusflow

<p class="stamp">Updated ${UPDATED}</p>

Focusflow grew out of **Flow**, an earlier personal tool: a task list where each task could open its own Claude
session with the task's brief loaded. That seed (tasks with briefs, one session per task) proved the idea. Focusflow
is what it became once the goal changed from *starting* sessions to *managing* the work they do.

| When | What changed |
|---|---|
| 22 Sep 2026 | Focusflow starts from Flow's code under its own name. |
| 23 Sep 2026 | Background runs, the message bus (message any task, one inbox), session hooks, playbooks. |
| 24 Sep 2026 | Six stages with pause and approval, project assistants, delivery receipts, backups, health checks. Flow is retired and its data migrated. |
| 24 Sep 2026 | The dashboard and Mac app become part of Focusflow: one product, one name. |
| 25 Sep 2026 | Needs you, background-run visibility, Undo, two-pane Inbox. |
| 25 Sep 2026 | **Memory** and the **attention engine**. The redesigned app you're using now. |

What stayed from Flow: tasks with briefs, and a Claude session per task. Everything else (stages, approval, memory,
attention, messaging, assistants, the app) is Focusflow's.
` },
  { id: 'vision', title: 'Vision', body: `# Where Focusflow is going

<p class="stamp">Written ${UPDATED}. A vision, not a promise: dates are intentions.</p>

**Our bet:** in a few years everyone who works with a computer will direct several AI agents. The winners won't be the
people with the most agents; they'll be the people whose agents need them least and learn from them most. Focusflow
is how that person works.

## Shipped
- **Memory from the human channel.** Every answer and instruction, remembered and recalled.
- **Attention engine.** Ranked, explained, snoozable, learning from you; quiet hours.
- **Judgement that compounds.** "Request changes" turns your review feedback into **house rules** every future session
  follows and checks itself against. The longer you use it, the less you correct.
- **Proof of work.** Reviews arrive with what Claude says it did, its notes, the commits and files changed, and your
  rules as a checklist.

## Building next (Q4 2026)
- **Rules that write themselves.** Focusflow suggests a house rule when you send similar feedback twice, and shows
  which rules a piece of work was checked against.
- **Tests as evidence.** Reviews that show which checks ran and passed.
- **A worktree per task.** Parallel work without collisions, and a clean diff to approve.

## Exploring (2027)
- **Plans, not just tasks.** Describe an outcome; the project assistant drafts a plan of tasks, runs them in the right
  order, and brings you only the decisions.
- **Approve from anywhere.** A phone companion, so the banners you can already act on at your desk follow you.
- **Any agent.** The same memory and attention for every coding agent you use, not only Claude.
- **Weekly review, written for you.** What shipped, what stalled, what you decided, what changed in your rules.
- **Teams.** Shared projects where house rules become team standards, and one Home for the whole team.
- **An open protocol.** The way agents ask, humans answer and memory flows, published so any tool can take part.
` },
];

export const HELP = [
  { id: 'getting-started', group: 'Start here', title: 'Getting started', body: `# Getting started

<p class="stamp">Updated ${UPDATED}</p>

1. **Create a project.** Projects → New project. Pick the folder Claude should work in (a codebase, a documents
   folder). Use **Choose…** to open Finder, where you can also make a new folder.
2. **Add a task.** Press **N** anywhere, or type in the box on Home. Say what you want; add details in the brief.
   New tasks go to **Up next**, ready to hand over.
3. **Start it with a message.** Open the task and tell Claude what to do. It starts working **in the background**:
   no terminal opens. You can keep working on something else.
4. **Answer questions.** If Claude needs you, it appears on **Home** (and as a notification). Reply right there.
   Your answer is **remembered** for every future session.
5. **Approve.** When Claude is done, the task moves to **Ready for review**. Look at the result and press
   **Approve**, or send a message asking for changes.

Want to watch or type directly? Press **Open in terminal** on any task.
` },
  { id: 'concepts', group: 'Start here', title: 'How it works', body: `# How it works

<p class="stamp">Updated ${UPDATED}</p>

## Tasks
A task is one piece of work: a name, a **brief** (what, why, what done looks like), **notes** (progress written as it
goes), a **stage**, priority, due date, tags and a conversation. Each task has its own Claude session.

## Stages
| Stage | Means |
|---|---|
| **Backlog** | Ideas and later work. |
| **Up next** | Ready to hand over. |
| **In progress** | Claude is working on it, or has worked on it and is waiting on you. |
| **Paused** | Set aside on purpose. A running session is told to wrap up. |
| **Ready for review** | Claude says it's finished. Your turn. |
| **Done** | You approved it. What was learned goes to the knowledge base. |

Drag cards between columns on the board, or use the **⋯** menu on any task.

## Projects
A project is a folder Claude works in, with its own tasks, brief, memory and **assistant**. The assistant is one
permanent Claude session for the whole project: ask it anything that isn't one task's job.

## Background runs and terminals
Messaging a task that isn't open in a terminal starts a **background run**: Claude works unattended in the project
folder. **Open in terminal** shows the same session in your terminal app so you can watch or type. Both are the same
Claude session; you choose how to look at it.

## On your Mac
The Focusflow app adds a **menu-bar item** showing how many things need you, a **Dock badge**, and **notifications**
you can act on: **Reply** to a question right from the banner, **Approve** finished work, or click to open the task.
Press **⌃⌥N** anywhere to capture a task without switching apps.

## Home
Everything that needs you, ranked, with the reason: questions, work ready for review, stopped or stuck runs, overdue
tasks and things you're waiting on. Nothing there? You're all caught up.

## Memory
What you tell tasks (answers and instructions) is remembered automatically and handed to later sessions. Manage it in
**Memory** or on a task's **Memory** tab.
` },
  { id: 'hand-off', group: 'Guides', title: 'Hand off work', body: `# Hand off work

<p class="stamp">Updated ${UPDATED}</p>

- **Write a good brief.** Say what you want, why, and what "done" looks like. Claude reads the brief first in every
  session. Edit it any time on the task's **Brief** tab.
- **Send the first message.** "Start with the pricing table. Keep the current design." Claude starts in the background.
- **Keep an eye on it (optional).** The task page shows what Claude is doing right now and how long it's been running.
  **Show log** has the raw output. **Stop** ends the run.
- **Get on with your day.** You'll be notified if Claude asks a question, finishes, or stops.

Several tasks can run at the same time. Each works in its own session.
` },
  { id: 'review', group: 'Guides', title: 'Review and approve', body: `# Review and approve

<p class="stamp">Updated ${UPDATED}</p>

When Claude finishes, the task moves to **Ready for review** and appears on Home.

The task page shows **proof of work**: what Claude says it did, its latest progress note, the commits and files changed
in the project folder since the task started, and your **house rules** as a checklist.

- **Approve** closes it (stage → Done) and saves what was learned. You have a few seconds to **Undo**.
- **Request changes** sends it back with what to change; Claude gets straight back to work. Tick **Make this a house
  rule** and it becomes a standard every future session in the project (or everywhere) must follow and check its work
  against. That is how Focusflow learns your judgement: the more you review, the less you correct.
- **Several at once?** Tasks → List, select them, **Approve**.
` },
  { id: 'memory', group: 'Guides', title: 'Memory', body: `# Memory: never say it twice

<p class="stamp">Updated ${UPDATED}</p>

**What's remembered automatically:** every message you send to a task. If Claude had asked a question, it's stored as
your *answer* together with the question; otherwise as an *instruction*. Messages to a project assistant apply to the
whole project.

**How it's used:** at the start of every session, Claude is given "what you already know": things remembered for
everywhere, for the project, and for this task. It's told not to ask them again.

**House rules** are the strongest kind of memory: standards every session must follow and check its work against.
They come from **Request changes** at review, or add them on a project's **House rules** tab.

**Your controls** (Memory page, or a task's Memory tab):
- **Remember something** directly, for this task, the whole project, or everywhere.
- **Pin** what must always be included.
- **Apply to the whole project** (or everywhere) when something is true beyond one task.
- **Forget** anything that's wrong or outdated. Forgotten memories stop being used immediately.

From the command line: \`focusflow remember "…"\`, \`focusflow memories\`, \`focusflow forget <id>\`.
` },
  { id: 'focus', group: 'Guides', title: 'Stay focused', body: `# Stay focused

<p class="stamp">Updated ${UPDATED}</p>

- **Later** on any Home item: remind me in an hour, in three hours, tomorrow morning, or when something changes.
  Snoozed items come back on their own, or as soon as there's news (a new question, a new failure).
- **Dismiss** removes an item until something new happens.
- **Quiet hours** (Settings): non-urgent notifications wait and arrive as one summary. Urgent questions still get through.
- **Why is this here?** Every Home item lists its reasons: urgent, high priority, how long it's waited, what it's
  blocking, and whether you usually act on these quickly. Focusflow learns from what you act on.
` },
  { id: 'organise', group: 'Guides', title: 'Organise your work', body: `# Organise your work

<p class="stamp">Updated ${UPDATED}</p>

- **Board or list.** Tasks → Board for stages at a glance; List to group by stage, project or priority.
- **Filter** by project, priority or text. Your choice of view is remembered.
- **Quick add** with shorthand: "Write the launch post **#website !high**" sets the project and priority.
- **Bulk actions.** In List, tick tasks to move, approve or archive them together.
- **Edit anything** on the task page: name, priority, due date, waiting on, tags, brief, notes.
- **Archive, never delete.** Archived work is one click from coming back.
` },
  { id: 'assistant', group: 'Guides', title: 'Ask the project', body: `# Ask the project

<p class="stamp">Updated ${UPDATED}</p>

Each project can have an **assistant**: one permanent Claude session that knows the whole project.

- Open a project → **Ask the project**. Describe what you want in plain words.
- The assistant decides whether it belongs to an existing task (and passes it on) or needs a new task (and creates
  one), then tells you what it did.
- Tick **Also tell every open task** to broadcast something everyone should know.
- **Check-ins:** project ⋯ → "Assistant checks in every day" (or 12h, 3d, 1w). The assistant reviews the project on
  that rhythm and nudges what's stuck.
` },
  { id: 'background-runs', group: 'Guides', title: 'Background runs and safety', body: `# Background runs and safety

<p class="stamp">Updated ${UPDATED}</p>

A background run is Claude working on a task **without a terminal and without asking permission for each step**, in
the task's project folder. That's what lets you hand work off and walk away, so use it with folders you trust Claude to
change.

- **What it can do:** anything Claude Code can do in that folder: read and edit files, run commands.
- **How you know what it's doing:** the task page shows the current step and elapsed time; **Show log** has the output.
- **How to stop it:** **Stop** on the task, on Home, or Pause the task. Pausing a project stops all of its runs.
- **When it goes quiet:** after 15 minutes without visible progress (change in Settings) it appears on Home as
  **Might be stuck**.
- **Per project, you decide.** Project ⋯ → **Turn background runs off** makes messages wait until you open the task
  in a terminal. **Time limit per run** stops any run that goes on longer than 15 minutes to 2 hours; it shows on Home
  as stopped at the time limit.
- **If a terminal is open** for the task, messages go to that session instead: no second run starts.
- Everything a run does is in the task's history and your git history. Nightly backups keep Focusflow's own data safe.
` },
  { id: 'shortcuts', group: 'Reference', title: 'Keyboard shortcuts', body: `# Keyboard shortcuts

<p class="stamp">Updated ${UPDATED}</p>

| Keys | Does |
|---|---|
| ⌃⌥N (anywhere on your Mac) | Quick capture: add a task without leaving what you're doing |
| ⌘K or / | Search and jump anywhere, run any command |
| N | New task |
| G then H | Home |
| G then I | Inbox |
| G then T | Tasks |
| G then P | Projects |
| G then M | Memory |
| G then S | Settings |
| ? | This page |
| ⌘ Enter | Send a message / add and open a new task |
| Esc | Close a dialog or menu |
| ↑ ↓ Enter | Move through menus and search results |
` },
  { id: 'cli', group: 'Reference', title: 'Command line', body: `# Command line

<p class="stamp">Updated ${UPDATED}</p>

Everything in the app is also available as \`focusflow\` commands, which Claude sessions use too.

| Command | Does |
|---|---|
| \`focusflow add task "…" --project <p>\` | Add a task |
| \`focusflow do <task>\` | Open the task in a terminal |
| \`focusflow do <task> --auto --with "…"\` | Start a background run |
| \`focusflow message user/<task> "…"\` | Message a task |
| \`focusflow next / review / pause / done <task>\` | Change stage (done = approve) |
| \`focusflow remember "…"\` (add --project or --global) | Teach memory |
| \`focusflow memories\` · \`focusflow forget <id>\` | See or remove memory |
| \`focusflow list tasks\` · \`focusflow show task <t>\` | Read |
| \`focusflow backup\` · \`focusflow restore <file> --force\` | Back up and restore |
| \`focusflow doctor [--fix]\` | Check health |
| \`focusflow dashboard open\` | Open this app in a browser |
` },
  { id: 'troubleshooting', group: 'Reference', title: 'Troubleshooting', body: `# Troubleshooting

<p class="stamp">Updated ${UPDATED}</p>

### The app says Focusflow isn't answering
The background service stopped. In Terminal run \`focusflow dashboard start\`, then reload. If that fails,
\`focusflow doctor\` shows what's wrong and \`focusflow doctor --fix\` repairs the usual problems.

### It asks for an access token
The page lost its key. Run \`focusflow dashboard url\` and open the link it prints, or reopen the Focusflow app.

### I sent a message but nothing happened
Open the task. Under the conversation you'll see **Sent** (waiting for the session to pick it up) or **Delivered**. If
it stays on Sent for a long time, the session may not be listening: press **Open in terminal**, or send again. If the
task was already open in a terminal, the message went to that session.

### A background run is stuck or stopped
It shows on Home as **Might be stuck** or **Background run stopped**. Open the task → **Show log** to see the last
output. Press **Stop**, then send a message to try again, or **Open in terminal** to take over.

### "Open in terminal" doesn't open anything
Check Settings → Terminal app (iTerm2 or Terminal). macOS may ask once for permission to control it: allow it in
System Settings → Privacy & Security → Automation.

### No notifications
Settings → Notifications must be on, and it may be quiet hours (only urgent questions come through then). macOS must
allow notifications from **Focusflow** in System Settings → Notifications. When the Focusflow app isn't running,
notifications come from Script Editor instead, and have no Reply or Approve buttons.

### The ⌃⌥N shortcut does nothing
The Focusflow app must be running (it lives in the menu bar too). Another app may already use the same shortcut.

### The Dock badge is wrong
It updates every 15 seconds. Refresh (Settings → Status → Refresh) or relaunch the app.

### Something looks out of date
Settings → Status → **Refresh** re-reads everything immediately.

### I need to go back to yesterday
See **Backups and restore**.
` },
  { id: 'backups', group: 'Reference', title: 'Backups and restore', body: `# Backups and restore

<p class="stamp">Updated ${UPDATED}</p>

- Every night at 02:30 Focusflow saves a full copy (database, briefs, notes, memory) to
  \`~/.focusflow/backups/\`. Make one any time with \`focusflow backup\`.
- To restore: quit the app, then run \`focusflow restore ~/.focusflow/backups/<file>.tar.gz --force\`, then reopen.
- Archiving never deletes anything; see **Archive** to restore tasks and projects.
` },
  { id: 'faq', group: 'Reference', title: 'FAQ', body: `# FAQ

<p class="stamp">Updated ${UPDATED}</p>

**Does my data leave my Mac?** Focusflow stores everything locally. Claude itself sends what a session needs to
Anthropic, exactly as Claude Code always does.

**Do I need to know how to code?** No. You can describe work in plain language, answer questions and approve results.
Open in terminal is there if you want it.

**What's the difference between Pause and Archive?** Pause sets work aside and stops any run; it stays visible.
Archive puts it away entirely; restore it from Archive.

**Can I use it without the app?** Yes. Everything works from the \`focusflow\` command line too.

**What happens when I approve?** The task moves to Done, and what was learned is saved to the knowledge base. You
have a few seconds to Undo.

**Why is something on Home?** Every item lists its reasons underneath.
` },
  { id: 'whats-new', group: 'Reference', title: 'What’s new', body: `# What's new

<p class="stamp">Updated ${UPDATED}</p>

### 25 September 2026 (night)
- Per-project control of background runs: turn them off, or set a time limit per run.

### 25 September 2026 (evening)
- Notifications from the Focusflow app: click to open, **Reply** inline, **Approve** from the banner.
- A menu-bar item with everything that needs you.
- **⌃⌥N** quick capture from anywhere on your Mac.

### 25 September 2026 (later)
- **Request changes** and **house rules**: review feedback becomes a standard every future session follows.
- **Proof of work** on every review: Claude's summary, the latest note, commits and files changed, your rules.
- Background runs that finish normally are no longer reported as failures.

### 25 September 2026
- A completely redesigned app: sidebar, Home, board and list, task pages you can edit, projects with progress,
  a two-pane Inbox, Settings, and About & Help.
- **Memory**: answers and instructions are remembered and given to every future session.
- **Attention engine**: Home ranks what needs you, explains why, flags stuck runs, and supports snooze and quiet hours.
- ⌘K search and commands everywhere; keyboard shortcuts.

### 24 September 2026
- Six stages with pause and approval; project assistants and check-ins; message receipts; the Mac app and Dock badge;
  backups and health checks.
` },
];
