import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { makeApp, focusRow } from './harness.js';
import { state, at } from './helpers.js';

let h;
afterEach(() => { h?.close(); });
const T = (slug, status, extra = {}) => state({ slug, name: slug.toUpperCase(), status, has_session: true, harness: 'claude', ...extra });

test('/api/needs-you lists review work and unanswered questions with a badge count', async () => {
  h = await makeApp({ live: [], tasks: [T('rev', 'review'), T('ask', 'in-progress'), T('calm', 'in-progress')] });
  h.inboxRows.rows = [focusRow('ask', 'Square or 4:5?')];
  await h.app.pollInbox();
  const n = (await h.get('/api/needs-you')).json();
  assert.deepEqual(n.items.map(i => `${i.kind}:${i.slug}`).sort(), ['question:ask', 'review:rev']);
  assert.equal(n.badge, 2);
  assert.equal((await h.get('/api/needs-you', { token: null })).status, 401);
});

test('/api/tasks/:slug/run reports elapsed time and tails only logs inside the task directory', async () => {
  const runs = {};
  h = await makeApp({ live: [], tasks: [T('bg', 'in-progress', { auto_run_status: 'running' })], runs });
  const dir = path.join(h.config.focusRoot, 'tasks', 'bg', 'auto-runs');
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'a.log'), 'step one\nstep two\n');
  const outside = path.join(h.config.focusRoot, 'secret.txt');
  fs.writeFileSync(outside, 'nope');
  const startedAt = at(2026, 9, 22, 11, 58).toISOString(); // two minutes before the harness clock
  runs.bg = { status: 'running', pid: process.pid, started: startedAt, finished: null, log: path.join(dir, 'a.log'), workDir: '/w', sessionId: null };
  const r = (await h.get('/api/tasks/bg/run')).json();
  assert.equal(r.running, true);
  assert.equal(r.elapsed_seconds, 120);
  assert.equal(r.pid_alive, true);
  assert.equal(r.log_tail, 'step one\nstep two');
  runs.bg.log = outside;
  assert.equal((await h.get('/api/tasks/bg/run')).json().log_tail, null, 'a log outside the task directory is never read');
  runs.bg = { ...runs.bg, status: 'done', finished: at(2026, 9, 22, 11, 59).toISOString() };
  const d = (await h.get('/api/tasks/bg/run')).json();
  assert.deepEqual([d.running, d.elapsed_seconds, d.pid_alive], [false, 60, false]);
  assert.deepEqual((await h.get('/api/tasks/bg/run')).status, 200);
  assert.equal((await h.get('/api/tasks/nobody/run')).status, 404);
});

test('/api/since summarises what happened after a timestamp', async () => {
  h = await makeApp({ live: [], tasks: [
    T('rev', 'review', { status_changed_at: at(2026, 9, 22, 11).toISOString() }),
    T('old', 'review', { status_changed_at: at(2026, 9, 20, 11).toISOString() }),
    T('fin', 'done', { status_changed_at: at(2026, 9, 22, 10).toISOString() }),
    T('ask', 'in-progress'),
  ] });
  h.inboxRows.rows = [focusRow('ask', 'one', [9, 30]), focusRow('ask', 'two', [9, 40])];
  await h.app.pollInbox();
  const s = (await h.get(`/api/since?ts=${encodeURIComponent(at(2026, 9, 22, 8).toISOString())}`)).json();
  assert.deepEqual(s.reviews.map(r => r.slug), ['rev']);
  assert.deepEqual(s.done.map(r => r.slug), ['fin']);
  assert.deepEqual(s.replies.map(r => [r.slug, r.count]), [['ask', 2]]);
  assert.equal(s.total, 3);
  assert.equal((await h.get('/api/since?ts=garbage')).status, 400);
});

test('a headless run that ends sends one banner, unless the user paused it', async () => {
  const sent = [];
  const list = [T('bg', 'in-progress', { auto_run_status: 'running' }), T('stop', 'in-progress', { auto_run_status: 'running' })];
  h = await makeApp({ live: [], tasks: list, notifier: { notify: a => sent.push(a), status: () => ({}) } });
  list[0].status = 'review'; list[0].auto_run_status = 'done';
  list[1].status = 'paused'; list[1].auto_run_status = 'done';
  h.app.refresh();
  assert.equal(sent.length, 1);
  assert.deepEqual(sent[0].map(a => [a.slug, a.body]), [['bg', 'Finished. Ready for your review.']]);
  h.app.refresh();
  assert.equal(sent.length, 1, 'announced once');
});

test('quick-add with stage next queues the new task, and rejects an unknown stage', async () => {
  h = await makeApp({ live: [], tasks: [T('x', 'backlog')] });
  const ok = await h.post('/api/capture', { name: 'Draft the launch note', stage: 'next' });
  assert.equal(ok.status, 200);
  assert.equal(ok.json().stage, 'next');
  const verbs = h.calls.map(c => c.args[0]);
  assert.deepEqual(verbs.filter(v => v === 'add' || v === 'next'), ['add', 'next']);
  assert.equal((await h.post('/api/capture', { name: 'Another one', stage: 'sideways' })).status, 400);
});
