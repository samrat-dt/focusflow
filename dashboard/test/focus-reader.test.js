import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { DatabaseSync } from 'node:sqlite';
import { readTasks, readProjects, readWorkDir, readSession } from '../lib/focus-reader.js';
import { STATE_FIELDS } from '../lib/task-state.js';
import { tmpdir } from './helpers.js';

const SCHEMA = `
CREATE TABLE tasks (
  slug TEXT PRIMARY KEY, name TEXT NOT NULL, project_slug TEXT, status TEXT NOT NULL DEFAULT 'backlog',
  kind TEXT NOT NULL DEFAULT 'regular', playbook_slug TEXT, priority TEXT NOT NULL DEFAULT 'medium',
  work_dir TEXT NOT NULL, waiting_on TEXT, due_date TEXT, assignee TEXT, status_changed_at TEXT,
  session_id TEXT, session_started TEXT, session_last_resumed TEXT, harness TEXT, auto_run_status TEXT,
  auto_run_pid INTEGER, auto_run_started TEXT, auto_run_finished TEXT, auto_run_log TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, archived_at TEXT);
CREATE TABLE task_tags (task_slug TEXT NOT NULL, tag TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (task_slug, tag));
CREATE TABLE projects (slug TEXT PRIMARY KEY, name TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', priority TEXT NOT NULL DEFAULT 'medium', work_dir TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, archived_at TEXT);`;

function fixture() {
  const root = tmpdir('ff-focusflow-');
  const db = new DatabaseSync(path.join(root, 'focusflow.db'));
  db.exec(SCHEMA);
  const ins = db.prepare(`INSERT INTO tasks (slug,name,project_slug,status,kind,priority,work_dir,waiting_on,due_date,assignee,status_changed_at,session_id,session_started,session_last_resumed,harness,auto_run_status,created_at,updated_at,archived_at)
    VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`);
  ins.run('alpha', 'Alpha', 'proj', 'in-progress', 'regular', 'high', '/w', 'legal', '2026-10-01', 'sam', '2026-09-02T09:00:00+05:30', 'SECRET-SESSION-ID', '2026-09-02T09:00:00+05:30', '2026-09-03T09:00:00+05:30', 'claude', 'running', '2026-09-01T09:00:00+05:30', '2026-09-03T09:00:00+05:30', null);
  ins.run('beta', 'Beta', null, 'backlog', 'regular', 'low', '/w', null, null, null, null, null, null, null, null, null, '2026-09-01T10:00:00+05:30', '2026-09-01T10:00:00+05:30', '2026-09-10T00:00:00+05:30');
  const pr = db.prepare('INSERT INTO projects (slug,name,status,work_dir,created_at,updated_at,archived_at) VALUES (?,?,?,?,?,?,?)');
  pr.run('proj', 'Proj Name', 'active', '/p', 'x', 'x', null);
  pr.run('zeta', 'Zeta', 'active', '/z', 'x', 'x', null);
  pr.run('finished', 'Finished', 'done', '/f', 'x', 'x', null);
  pr.run('gone', 'Gone', 'active', '/g', 'x', 'x', '2026-09-01');
  db.prepare('INSERT INTO task_tags VALUES (?,?,?)').run('alpha', 'ui', 'x');
  db.prepare('INSERT INTO task_tags VALUES (?,?,?)').run('alpha', 'backend', 'x');
  db.close();

  fs.mkdirSync(path.join(root, 'tasks', 'alpha', 'updates'), { recursive: true });
  fs.writeFileSync(path.join(root, 'tasks', 'alpha', 'brief.md'), '# Alpha\n');
  fs.writeFileSync(path.join(root, 'tasks', 'alpha', 'updates', '2026-09-02-a.md'), 'n1');
  fs.writeFileSync(path.join(root, 'tasks', 'alpha', 'updates', '2026-09-03-b.md'), 'n2');
  fs.writeFileSync(path.join(root, 'tasks', 'alpha', 'updates', 'ignore.txt'), 'x');

  const json = [
    { slug: 'alpha', name: 'Alpha', status: 'in-progress', priority: 'high', project: 'proj', live: true, age_days: 2, updated: 'x', tags: ['backend', 'ui'] },
    { slug: 'beta', name: 'Beta', status: 'backlog', priority: 'low', archived: true, updated: 'x' },
  ];
  const jsonFile = path.join(root, 'fixture.json');
  fs.writeFileSync(jsonFile, JSON.stringify(json));
  const argsFile = path.join(root, 'args.txt');
  const bin = path.join(root, 'focusflow-stub');
  fs.writeFileSync(bin, `#!/bin/sh\necho "$@" > "${argsFile}"\ncat "${jsonFile}"\n`, { mode: 0o755 });
  return { root, bin, argsFile };
}

test('readTasks returns full states from focusflow JSON, focusflow.db and the files', () => {
  const f = fixture();
  const { tasks, live } = readTasks({ focusBin: f.bin, focusRoot: f.root });
  assert.equal(tasks.length, 2);
  const a = tasks.find(t => t.slug === 'alpha');
  assert.deepEqual(Object.keys(a), STATE_FIELDS);
  assert.deepEqual(a, {
    slug: 'alpha', name: 'Alpha', project: 'proj', kind: 'regular', status: 'in-progress', priority: 'high',
    tags: ['backend', 'ui'], due_date: '2026-10-01', waiting_on: 'legal', assignee: 'sam', harness: 'claude',
    has_session: true, session_started: '2026-09-02T09:00:00+05:30', session_last_resumed: '2026-09-03T09:00:00+05:30',
    status_changed_at: '2026-09-02T09:00:00+05:30', created_at: '2026-09-01T09:00:00+05:30',
    updated_at: '2026-09-03T09:00:00+05:30', archived: false, auto_run_status: 'running', notes_count: 2,
    brief_sha256: crypto.createHash('sha256').update('# Alpha\n').digest('hex'),
  });
  const b = tasks.find(t => t.slug === 'beta');
  assert.equal(b.archived, true);
  assert.equal(b.has_session, false);
  assert.equal(b.notes_count, 0);
  assert.equal(b.brief_sha256, null);
  assert.deepEqual(b.tags, []);
  assert.deepEqual([...live], ['alpha']);
});

test('the session id itself is never part of a task state', () => {
  const f = fixture();
  const { tasks } = readTasks({ focusBin: f.bin, focusRoot: f.root });
  assert.ok(!JSON.stringify(tasks).includes('SECRET-SESSION-ID'));
});

test('focusflow is called with the flags the spec relies on', () => {
  const f = fixture();
  readTasks({ focusBin: f.bin, focusRoot: f.root });
  assert.equal(fs.readFileSync(f.argsFile, 'utf8').trim(), 'list tasks --format json --include-done --include-archived --kind all');
});

test('a task that is in focusflow JSON but missing from focusflow.db still yields a state', () => {
  const f = fixture();
  const j = JSON.parse(fs.readFileSync(path.join(f.root, 'fixture.json'), 'utf8'));
  j.push({ slug: 'orphan', name: 'Orphan', status: 'backlog', priority: 'medium', updated: 'x' });
  fs.writeFileSync(path.join(f.root, 'fixture.json'), JSON.stringify(j));
  const { tasks } = readTasks({ focusBin: f.bin, focusRoot: f.root });
  assert.equal(tasks.find(t => t.slug === 'orphan').has_session, false);
});

test('a missing focusflow binary or database fails with a clear error', () => {
  const f = fixture();
  assert.throws(() => readTasks({ focusBin: path.join(f.root, 'nope'), focusRoot: f.root }), /focusflow/i);
  fs.rmSync(path.join(f.root, 'focusflow.db'));
  assert.throws(() => readTasks({ focusBin: f.bin, focusRoot: f.root }), /focusflow\.db/);
});

test('focusflow.db is opened read-only', () => {
  const f = fixture();
  const before = fs.statSync(path.join(f.root, 'focusflow.db')).mtimeMs;
  readTasks({ focusBin: f.bin, focusRoot: f.root });
  assert.equal(fs.statSync(path.join(f.root, 'focusflow.db')).mtimeMs, before);
});

test('readProjects lists active, non-archived projects only, sorted', () => {
  const f = fixture();
  assert.deepEqual(readProjects({ focusRoot: f.root }), [{ slug: 'proj', name: 'Proj Name' }, { slug: 'zeta', name: 'Zeta' }]);
});

test('readWorkDir returns a task\'s work_dir, or null when unknown', () => {
  const f = fixture();
  assert.equal(readWorkDir({ focusRoot: f.root, slug: 'alpha' }), '/w');
  assert.equal(readWorkDir({ focusRoot: f.root, slug: 'nope' }), null);
});

test('readSession returns the session id and harness for a task, or null when unknown', () => {
  const f = fixture();
  assert.deepEqual(readSession({ focusRoot: f.root, slug: 'alpha' }), { sessionId: 'SECRET-SESSION-ID', harness: 'claude' });
  assert.equal(readSession({ focusRoot: f.root, slug: 'beta' }), null);
  assert.equal(readSession({ focusRoot: f.root, slug: 'nope' }), null);
});
