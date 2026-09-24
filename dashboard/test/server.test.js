import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { openStore } from '../lib/store.js';
import { createApp } from '../lib/server.js';
import { state, tmpdir, at } from './helpers.js';

const TOKEN = 'a'.repeat(64);
let app, port, focusRoot, tasks, live, clock;

before(async () => {
  focusRoot = tmpdir('ff-flowroot-');
  const dir = path.join(focusRoot, 'tasks', 'alpha');
  fs.mkdirSync(path.join(dir, 'updates'), { recursive: true });
  fs.writeFileSync(path.join(dir, 'brief.md'), '# Alpha\n\n## What\nDo the thing.\n');
  fs.writeFileSync(path.join(dir, 'updates', '2026-09-01-first.md'), 'first note');
  fs.writeFileSync(path.join(dir, 'updates', '2026-09-03-third.md'), 'third note');
  fs.writeFileSync(path.join(dir, 'updates', '2026-09-02-second.md'), 'second note');
  fs.writeFileSync(path.join(dir, 'updates', 'readme.txt'), 'not a note');
  tasks = [
    state({ slug: 'alpha', name: 'Alpha', status: 'in-progress', has_session: true, harness: 'claude', priority: 'high', tags: ['ui'], session_started: '2026-09-02T09:00:00+05:30', updated_at: at(2026, 9, 21).toISOString() }),
    state({ slug: 'beta', name: 'Beta', due_date: '2026-09-20', waiting_on: 'legal' }),
    state({ slug: 'old', name: 'Old', status: 'done', has_session: true, archived: true }),
  ];
  live = new Set(['alpha']);
  clock = at(2026, 9, 22, 12);
  const config = { token: TOKEN, device: 'd1', dataDir: tmpdir('ff-data-'), focusRoot, tunables: { stale_days: 5, due_soon_days: 3 } };
  app = createApp({ config, store: openStore(), readTasks: () => { if (!tasks) throw new Error('focusflow exploded'); return { tasks, live }; }, now: () => clock });
  app.refresh();
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  port = app.server.address().port;
});
after(() => app.server.close());

function req(p, { method = 'GET', headers = {}, token = TOKEN, host } = {}) {
  return new Promise((resolve, reject) => {
    const h = { host: host ?? `127.0.0.1:${port}`, ...headers };
    if (token) h['x-focusflow-token'] = token;
    const r = http.request({ host: '127.0.0.1', port, path: p, method, headers: h }, res => {
      let body = '';
      res.on('data', c => (body += c));
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body, json: () => JSON.parse(body) }));
    });
    r.on('error', reject);
    r.end();
  });
}

test('the API requires the right token', async () => {
  assert.equal((await req('/api/tasks', { token: null })).status, 401);
  assert.equal((await req('/api/tasks', { token: 'wrong' })).status, 401);
  assert.equal((await req('/api/tasks', { token: TOKEN.slice(0, -1) })).status, 401);
  assert.equal((await req('/api/tasks')).status, 200);
});

test('a foreign Host is rejected even with the token (DNS rebinding)', async () => {
  assert.equal((await req('/api/tasks', { host: 'evil.example.com' })).status, 403);
  assert.equal((await req('/api/tasks', { host: `evil.example.com:${port}` })).status, 403);
  assert.equal((await req('/', { host: 'evil.example.com' })).status, 403);
  assert.equal((await req('/api/tasks', { host: `localhost:${port}` })).status, 200);
});

test('a foreign Origin is rejected; a matching or absent one is fine', async () => {
  assert.equal((await req('/api/tasks', { headers: { origin: 'https://evil.example.com' } })).status, 403);
  assert.equal((await req('/api/tasks', { headers: { origin: 'null' } })).status, 403);
  assert.equal((await req('/api/tasks', { headers: { origin: `http://127.0.0.1:${port}` } })).status, 200);
  assert.equal((await req('/api/tasks', { headers: { origin: `http://localhost:${port}` } })).status, 200);
});

test('static pages need no token but are Host-checked, and only the allowlist is served', async () => {
  const idx = await req('/', { token: null });
  assert.equal(idx.status, 200);
  assert.match(idx.headers['content-type'], /text\/html/);
  assert.equal((await req('/app.js', { token: null })).status, 200);
  assert.equal((await req('/style.css', { token: null })).status, 200);
  assert.equal((await req('/package.json', { token: null })).status, 404);
  assert.equal((await req('/../package.json', { token: null })).status, 404);
  assert.equal((await req('/%2e%2e/package.json', { token: null })).status, 404);
  assert.equal(idx.headers['cache-control'], 'no-store');
});

test('GET /api/tasks returns every task with signals and live markers', async () => {
  const j = (await req('/api/tasks')).json();
  assert.deepEqual(j.tasks.map(t => t.slug), ['alpha', 'beta', 'old']);
  const alpha = j.tasks.find(t => t.slug === 'alpha');
  assert.equal(alpha.live, true);
  assert.equal(alpha.signals.live, true);
  assert.equal(alpha.status, 'in-progress');
  const beta = j.tasks.find(t => t.slug === 'beta');
  assert.equal(beta.signals.due, 'overdue');
  assert.equal(beta.signals.waiting, true);
  assert.equal(j.tasks.find(t => t.slug === 'old').archived, true);
  assert.ok(j.generated_at);
  assert.ok(!JSON.stringify(j).includes('session_id'));
});

test('GET /api/tasks/:slug returns brief, notes newest first, and session info', async () => {
  const j = (await req('/api/tasks/alpha')).json();
  assert.equal(j.task.slug, 'alpha');
  assert.match(j.brief, /Do the thing/);
  assert.deepEqual(j.notes.map(n => n.file), ['2026-09-03-third.md', '2026-09-02-second.md', '2026-09-01-first.md']);
  assert.equal(j.notes[0].text, 'third note');
  assert.deepEqual(j.session, { harness: 'claude', has_session: true, started: '2026-09-02T09:00:00+05:30', last_resumed: null, live: true, auto_run_status: null });
});

test('a task with no brief or notes still has a detail view', async () => {
  const j = (await req('/api/tasks/beta')).json();
  assert.equal(j.brief, null);
  assert.deepEqual(j.notes, []);
});

test('slugs are validated and must exist', async () => {
  for (const bad of ['..%2F..%2Fetc%2Fpasswd', 'Alpha', '-x', 'a%20b', 'a'.repeat(200)]) {
    assert.equal((await req(`/api/tasks/${bad}`)).status, 400, bad);
  }
  assert.equal((await req('/api/tasks/nope')).status, 404);
});

test('GET /api/health reports snapshot, gaps, cache age, sync and last tick', async () => {
  const j = (await req('/api/health')).json();
  assert.equal(j.device, 'd1');
  assert.equal(j.last_snapshot_date, '2026-09-22');
  assert.equal(j.gap_count, 0);
  assert.equal(j.task_count, 3);
  assert.equal(j.last_error, null);
  assert.equal(j.sync.status, 'not configured');
  assert.equal(typeof j.cache_age_seconds, 'number');
});

test('POST /api/refresh re-reads focusflow and updates the board; failures are reported', async () => {
  tasks = [...tasks, state({ slug: 'gamma', name: 'Gamma' })];
  clock = at(2026, 9, 22, 12, 5);
  const r = await req('/api/refresh', { method: 'POST' });
  assert.equal(r.status, 200);
  assert.equal((await req('/api/tasks')).json().tasks.length, 4);
});

test('a failing read keeps the last good board and surfaces the error in health', async () => {
  const before = (await req('/api/tasks')).json().tasks.length;
  const orig = tasks; tasks = null; // the stub reader throws while tasks is null
  const r = await req('/api/refresh', { method: 'POST' });
  tasks = orig;
  assert.equal(r.status, 200);
  assert.equal(r.json().skipped, true);
  assert.equal((await req('/api/tasks')).json().tasks.length, before);
  assert.ok((await req('/api/health')).json().last_error);
  await req('/api/refresh', { method: 'POST' });
  assert.equal((await req('/api/health')).json().last_error, null);
});

test('unknown routes 404 and wrong methods 405', async () => {
  assert.equal((await req('/api/nope')).status, 404);
  assert.equal((await req('/api/tasks', { method: 'POST' })).status, 405);
  assert.equal((await req('/api/refresh', { method: 'GET' })).status, 405);
});
