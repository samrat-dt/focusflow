# Journeys: how the screens fit together

Four screens, one rule: **nothing opens a terminal unless you click Start, Resume, Focus or "Open terminal".**
Messages are answered headlessly. Stage changes (Pause, Send to review, Approve, Next…) only change status.

Every task has two independent facts, shown side by side:

- its **stage**: backlog · next · in progress · paused · review · done
- whether it is **open in a terminal** (⌨) or **running headless** (⚙)

A paused task can still be open in a terminal ("open in terminal, but paused"); a task in review can be closed.

| Screen | Purpose |
|---|---|
| **Board** | What needs me right now. Five lanes: Next · In progress · Paused · Review · Done (last 7 days). Backlog is not on the board. |
| **Projects** | How my work is organized. Click a project to expand its subtasks (backlog included); click again to collapse. Message, open a terminal on, pause, schedule, archive a project; add draft subtasks; create projects. |
| **Archive** | Archived projects (with their tasks) and archived tasks, each with Unarchive. |
| **Inbox** | Conversations with tasks and project coordinators, unread first. |

Always available: search (`/`) across briefs, notes and the knowledge base; a stats strip; a health chip (`focusflow doctor`).
The Mac app (`npm run app`) hosts this in its own window and adds an always-on-top mini board (⇧⌘M).

## A morning

1. **Board.** Review lane: two things finished overnight. Read them, click **Approve** — they close out (the
   knowledge-base sweep runs in the background) and move to Done. In progress: one card is open in a terminal
   and idle; one is running headless. Next: today's literal queue.
2. Something does not belong to one task: **Projects → expand "Resume" → Message**. The project's coordinator (one
   permanent session per project) decides whether it is part of subtask B — and messages B — or needs a new
   subtask, and tells me what it chose. Tick "also tell every open subtask" to broadcast it instead.
3. A genuinely new ask: message the project the same way; the coordinator creates a properly-scoped subtask.
   Or add a **draft subtask** myself ("Add a subtask…") — it sits in the backlog until I say **Next** or **Start**.
4. Reply to a task from its drawer: the message runs headlessly and I see **sent → delivered** (a real receipt,
   stamped when the session actually surfaces it), then the reply.
5. End of day, **Board**: **Pause** what is not worth leaving running (a headless run stops; an open terminal is
   asked to wrap up and stays open, marked ⌨ + paused). **Send to review** what is done.
6. **Projects → Schedule…** puts a project on a rhythm (every 12h / 1d / 3d / 1w): a scheduled coordinator reviews it
   and nudges what is stuck.
7. Done with a project: **Archive** it; find it again under **Archive → Unarchive**.

The acceptance test for this walk-through is `test/journey.test.js`.
