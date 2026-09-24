import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export const tmpdir = (p = 'ff-') => fs.mkdtempSync(path.join(os.tmpdir(), p));

export function state(overrides = {}) {
  return {
    slug: 'task-a',
    name: 'Task A',
    project: null,
    kind: 'regular',
    status: 'backlog',
    priority: 'medium',
    tags: [],
    due_date: null,
    waiting_on: null,
    assignee: null,
    harness: null,
    has_session: false,
    session_started: null,
    session_last_resumed: null,
    status_changed_at: null,
    created_at: '2026-09-01T09:00:00+05:30',
    updated_at: '2026-09-01T09:00:00+05:30',
    archived: false,
    auto_run_status: null,
    notes_count: 0,
    brief_sha256: null,
    ...overrides,
  };
}

// Local wall-clock time, so tests do not depend on the machine's timezone.
export const at = (y, m, d, h = 10, min = 0) => new Date(y, m - 1, d, h, min, 0, 0);
