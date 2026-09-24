import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { DatabaseSync } from 'node:sqlite';
import { focusEnv } from './focus-env.js';

const ARGS = ['list', 'tasks', '--format', 'json', '--include-done', '--include-archived', '--kind', 'all'];

function focusJson(focusBin, env) {
  const r = spawnSync(focusBin, ARGS, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, env });
  if (r.error) throw new Error(`cannot run focusflow (${focusBin}): ${r.error.message}`);
  if (r.status !== 0) throw new Error(`focusflow exited ${r.status}: ${(r.stderr || '').trim()}`);
  try {
    return JSON.parse(r.stdout);
  } catch {
    throw new Error('focusflow returned output that is not JSON');
  }
}

// Read-only. Never writes focusflow.db or any focusflow file.
function withFocusDb(dbPath, busyTimeoutMs, fn) {
  if (!fs.existsSync(dbPath)) throw new Error(`focusflow.db not found at ${dbPath}`);
  const db = new DatabaseSync(dbPath, { readOnly: true });
  try {
    db.exec(`PRAGMA busy_timeout = ${Number(busyTimeoutMs) | 0}`);
    return fn(db);
  } finally {
    db.close();
  }
}

function focusDbRows(dbPath, busyTimeoutMs) {
  return withFocusDb(dbPath, busyTimeoutMs, db => {
    const rows = new Map(db.prepare('SELECT * FROM tasks').all().map(r => [r.slug, r]));
    const tags = new Map();
    for (const t of db.prepare('SELECT task_slug, tag FROM task_tags ORDER BY tag').all()) {
      if (!tags.has(t.task_slug)) tags.set(t.task_slug, []);
      tags.get(t.task_slug).push(t.tag);
    }
    return { rows, tags };
  });
}

const defaultRoot = () => process.env.FOCUSFLOW_ROOT || path.join(os.homedir(), '.focusflow');
const defaultDbName = () => process.env.FOCUSFLOW_DB_NAME || 'focusflow.db';

// Active, non-archived projects, for the capture dialog.
export function readProjects({ focusRoot = defaultRoot(), dbName = defaultDbName(), busyTimeoutMs = 3000 } = {}) {
  return withFocusDb(path.join(focusRoot, dbName), busyTimeoutMs, db =>
    db.prepare("SELECT slug, name FROM projects WHERE status = 'active' AND archived_at IS NULL ORDER BY slug")
      .all().map(r => ({ slug: r.slug, name: r.name })));
}

export function readWorkDir({ focusRoot = defaultRoot(), dbName = defaultDbName(), slug, busyTimeoutMs = 3000 }) {
  return withFocusDb(path.join(focusRoot, dbName), busyTimeoutMs, db => {
    const r = db.prepare('SELECT work_dir FROM tasks WHERE slug = ?').get(slug);
    return r ? r.work_dir : null;
  });
}

// On-demand, single-slug, read-only — never folded into readTasks()'s broad per-task state (the
// session id is deliberately excluded from that; see test/focus-reader.test.js).
export function readSession({ focusRoot = defaultRoot(), dbName = defaultDbName(), slug, busyTimeoutMs = 3000 }) {
  return withFocusDb(path.join(focusRoot, dbName), busyTimeoutMs, db => {
    const r = db.prepare('SELECT session_id, harness FROM tasks WHERE slug = ?').get(slug);
    if (!r || r.session_id == null) return null;
    return { sessionId: r.session_id, harness: r.harness ?? null };
  });
}

function notesCount(dir) {
  try {
    return fs.readdirSync(dir).filter(f => f.endsWith('.md')).length;
  } catch {
    return 0;
  }
}

function briefSha(file) {
  try {
    return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  } catch {
    return null;
  }
}

// focusflow owns tasks: identity, status, priority, project, tags and archived come
// from its JSON; the rest comes from a read-only focusflow.db read plus the files.
export function readTasks({
  focusBin = process.env.FOCUSFLOW_BIN || 'focusflow',
  focusRoot = process.env.FOCUSFLOW_ROOT || path.join(os.homedir(), '.focusflow'),
  dbName = defaultDbName(),
  busyTimeoutMs = 3000,
  env = focusEnv({ focusBin, terminalApp: process.env.FOCUSFLOW_TERM || 'Terminal.app' }),
} = {}) {
  const list = focusJson(focusBin, env);
  const { rows, tags } = focusDbRows(path.join(focusRoot, dbName), busyTimeoutMs);
  const live = new Set();
  const tasks = list.map(j => {
    const r = rows.get(j.slug) ?? {};
    const dir = path.join(focusRoot, 'tasks', j.slug);
    if (j.live) live.add(j.slug);
    return {
      slug: j.slug,
      name: j.name,
      project: j.project ?? null,
      kind: r.kind ?? 'regular',
      status: j.status,
      priority: j.priority,
      tags: [...(j.tags ?? tags.get(j.slug) ?? [])].sort(),
      due_date: r.due_date ?? null,
      waiting_on: r.waiting_on ?? null,
      assignee: r.assignee ?? null,
      harness: r.harness ?? null,
      has_session: r.session_id != null,
      session_started: r.session_started ?? null,
      session_last_resumed: r.session_last_resumed ?? null,
      status_changed_at: r.status_changed_at ?? null,
      created_at: r.created_at ?? null,
      updated_at: r.updated_at ?? null,
      archived: !!j.archived,
      auto_run_status: r.auto_run_status ?? null,
      notes_count: notesCount(path.join(dir, 'updates')),
      brief_sha256: briefSha(path.join(dir, 'brief.md')),
    };
  });
  return { tasks, live };
}

// Every project, archived ones included, with the fields the Projects and Archive screens need.
// `agent_task_slug` is the project's hidden coordinator task (null until it is first used).
export function readProjectsFull({ focusRoot = defaultRoot(), dbName = defaultDbName(), busyTimeoutMs = 3000 } = {}) {
  return withFocusDb(path.join(focusRoot, dbName), busyTimeoutMs, db =>
    db.prepare('SELECT slug, name, status, priority, work_dir, archived_at, agent_task_slug FROM projects ORDER BY slug')
      .all().map(r => ({
        slug: r.slug, name: r.name, status: r.status, priority: r.priority, work_dir: r.work_dir,
        archived: r.archived_at != null, agent_task_slug: r.agent_task_slug ?? null,
      })));
}

// Scheduled coordinators (focusflow owners), for the project rows' Schedule state.
export function readOwners({ focusRoot = defaultRoot(), dbName = defaultDbName(), busyTimeoutMs = 3000 } = {}) {
  return withFocusDb(path.join(focusRoot, dbName), busyTimeoutMs, db => {
    try {
      return db.prepare('SELECT slug, project_slug, status, every, next_wake_at, last_tick_at FROM owners WHERE archived_at IS NULL ORDER BY slug')
        .all().map(r => ({ slug: r.slug, project: r.project_slug ?? null, status: r.status, every: r.every, next_wake_at: r.next_wake_at ?? null, last_tick_at: r.last_tick_at ?? null }));
    } catch { return []; } // a database from before owners existed
  });
}
