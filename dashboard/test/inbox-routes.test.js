import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { makeEvent } from '../lib/event-log.js';
import { openStore } from '../lib/store.js';
import { buildThread } from '../lib/inbox.js';
import { makeApp, focusRow } from './harness.js';
import { at, tmpdir } from './helpers.js';

let h;
beforeEach(() => { h = null; });
afterEach(() => { h?.close(); });

const apply = (kind, payload, when) => h.store.apply(makeEvent({ device: 'd1', kind, subject: `task:${payload.slug}`, payload, recordedAt: at(2026, 9, 22, ...when).toISOString() }));
const out = (slug, text, when) => apply('message.sent', { slug, text, mode: 'queue', flow_id: `0${when.join('')}`.padEnd(8, '0'), ok: true }, when);

async function primed(rows) {
  h = await makeApp();
  await h.app.pollInbox();           // first poll: an empty queue
  h.inboxRows.rows = rows;
  await h.app.pollInbox();           // now the rows arrive as news
}

test('GET /api/inbox lists threads newest first with unread counts, live marker and status', async () => {
  await primed([
    focusRow('going', 'older', [9, 0]),
    focusRow('running', 'newest one', [11, 0]),
    focusRow('running', 'newest two', [11, 30], { urgent: true }),
    focusRow('todo', 'mid', [10, 0], { mail: 'read' }),
  ]);
  const j = (await h.get('/api/inbox')).json();
  assert.deepEqual(j.threads.map(t => t.slug), ['running', 'todo', 'going']);
  assert.equal(j.unread_total, 3);
  const running = j.threads[0];
  assert.deepEqual(running, {
    slug: 'running', name: 'Running', unread: 2,
    last: { direction: 'in', preview: 'newest two', at: at(2026, 9, 22, 11, 30).toISOString() },
    live: true, auto_run_status: null, status: 'in-progress',
  });
  assert.equal(j.threads[1].unread, 0);
  assert.equal(j.threads[2].live, false);
});

test('the inbox preview is one line and short; my own message can be the last one', async () => {
  await primed([focusRow('going', `line one\n   line two ${'x'.repeat(200)}`, [9, 0])]);
  out('going', 'my reply', [10, 0]);
  const t = (await h.get('/api/inbox')).json().threads[0];
  assert.equal(t.last.direction, 'out');
  assert.equal(t.last.preview, 'my reply');
  out('going', `${'y '.repeat(200)}`, [10, 5]);
  const long = (await h.get('/api/inbox')).json().threads[0].last.preview;
  assert.ok(long.length <= 100);
  assert.ok(!long.includes('\n'));
});

test('threads for tasks focusflow no longer lists are left out of the inbox and its count', async () => {
  await primed([focusRow('going', 'kept', [9, 0]), focusRow('ghost', 'orphan', [9, 30])]);
  const j = (await h.get('/api/inbox')).json();
  assert.deepEqual(j.threads.map(t => t.slug), ['going']);
  assert.equal(j.unread_total, 1);
});

test('GET /api/tasks/:slug/thread: oldest first, unread flags, no reply cue, composer state', async () => {
  await primed([focusRow('running', 'first', [10, 0]), focusRow('running', 'second', [10, 30])]);
  const j = (await h.get('/api/tasks/running/thread')).json();
  assert.deepEqual(j.messages.map(m => [m.direction, m.body, m.unread]), [['in', 'first', true], ['in', 'second', true]]);
  assert.equal(j.awaiting_reply, false);
  assert.equal(j.live, true);
  assert.equal(j.status, 'in-progress');
  assert.equal(j.archived, false);
  assert.equal(j.work_dir, '/work/x');
  assert.equal(j.has_session, true);
  assert.deepEqual(Object.keys(j.messages[0]).sort(), ['at', 'body', 'direction', 'id', 'reply_to', 'unread']);
});

test('the thread says whether the task already has a session (governs whether headless is a fresh run or a continuation)', async () => {
  h = await makeApp();
  assert.equal((await h.get('/api/tasks/todo/thread')).json().has_session, false);
  assert.equal((await h.get('/api/tasks/going/thread')).json().has_session, true);
});

test('a live claude task with a recently-touched transcript is reported as working; a stale transcript is not', async () => {
  // makeApp's fake clock is fixed at at(2026, 9, 22, 12) — mtimes are set relative to that, not to
  // real wall-clock time, so the test is deterministic regardless of when it actually runs.
  const fakeNow = at(2026, 9, 22, 12);
  const home = tmpdir('ff-activity-');
  const dir = path.join(home, '.claude', 'projects', '-work-x');
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, 'sess-run.jsonl');
  fs.writeFileSync(file, '{}');
  fs.utimesSync(file, fakeNow, fakeNow); // touched exactly "now": working

  h = await makeApp({
    live: ['running'],
    sessions: { running: { sessionId: 'sess-run', harness: 'claude' } },
    workDir: '/work/x',
    activityHome: home,
  });
  const tasks = (await h.get('/api/tasks')).json().tasks;
  const running = tasks.find(t => t.slug === 'running');
  assert.equal(running.working, true);
  assert.equal(running.signals.working, true);

  const old = new Date(fakeNow.getTime() - 60000); // 60s before "now": outside the default 15s window
  fs.utimesSync(file, old, old);
  await h.app.refresh();
  const after = (await h.get('/api/tasks')).json().tasks.find(t => t.slug === 'running');
  assert.equal(after.working, false, 'live but idle: the transcript went quiet');
  assert.equal(after.signals.live, true);
});

test('a live task with no session row, or a non-claude harness, is live but never working', async () => {
  const home = tmpdir('ff-activity-');
  h = await makeApp({ live: ['running'], activityHome: home }); // no `sessions` entry for `running`
  const running = (await h.get('/api/tasks')).json().tasks.find(t => t.slug === 'running');
  assert.equal(running.live, true);
  assert.equal(running.working, false);
});

test('awaiting_reply is true while my latest message has no reply after it', async () => {
  await primed([focusRow('going', 'hello', [9, 0])]);
  out('going', 'question?', [10, 0]);
  assert.equal((await h.get('/api/tasks/going/thread')).json().awaiting_reply, true);
  h.inboxRows.rows = [...h.inboxRows.rows, focusRow('going', 'answer', [10, 30])];
  await h.app.pollInbox();
  assert.equal((await h.get('/api/tasks/going/thread')).json().awaiting_reply, false);
});

test('a thread with no messages is an empty list and not awaiting anything', async () => {
  h = await makeApp();
  const j = (await h.get('/api/tasks/going/thread')).json();
  assert.deepEqual(j.messages, []);
  assert.equal(j.awaiting_reply, false);
});

test('an unreadable work_dir does not break the thread', async () => {
  h = await makeApp({ workDir: null });
  assert.equal((await h.get('/api/tasks/going/thread')).json().work_dir, null);
});

test('buildThread: stalled is true only when live, awaiting a reply, and the last outgoing message is past stallMs', () => {
  const store = openStore();
  const sent = makeEvent({ device: 'd1', kind: 'message.sent', subject: 'task:x', payload: { slug: 'x', text: 'hi', mode: 'queue', ok: true, flow_id: 'ab12cd34' }, recordedAt: '2026-09-22T10:00:00.000Z' });
  store.apply(sent);
  const past = new Date('2026-09-22T10:01:00.000Z'); // 60s later
  assert.equal(buildThread({ store, device: 'd1', slug: 'x', live: true, now: past, stallMs: 30000 }).stalled, true);
  assert.equal(buildThread({ store, device: 'd1', slug: 'x', live: false, now: past, stallMs: 30000 }).stalled, false, 'not live: expected, not a failure');
  assert.equal(buildThread({ store, device: 'd1', slug: 'x', live: true, now: past, stallMs: 120000 }).stalled, false, 'not past the timeout yet');
  assert.equal(buildThread({ store, device: 'd1', slug: 'x' }).stalled, false, 'defaults never mark anything stalled');
});

test('the thread route reports ever_headless from the store, and stalled only past the watchdog timeout on a live task', async () => {
  h = await makeApp({ live: ['running'], tunables: { nudge_stall_seconds: 30 } });
  await h.post('/api/tasks/running/message', { text: 'ping', mode: 'queue' });
  let t = (await h.get('/api/tasks/running/thread')).json();
  assert.equal(t.stalled, false, 'not past the timeout yet (fake clock does not advance)');
  assert.equal(t.ever_headless, false);
});

test('ever_headless flips true after a successful headless send and stays true after', async () => {
  h = await makeApp();
  await h.post('/api/tasks/todo/message', { text: 'go', mode: 'headless', confirm: true });
  const t = (await h.get('/api/tasks/todo/thread')).json();
  assert.equal(t.ever_headless, true);
});

test('routes need the token, right method and a valid existing slug', async () => {
  h = await makeApp();
  assert.equal((await h.get('/api/inbox', { token: 'wrong' })).status, 401);
  assert.equal((await h.get('/api/tasks/going/thread', { token: null })).status, 401);
  assert.equal((await h.request('GET', '/api/inbox', undefined, { host: 'evil.example.com' })).status, 403);
  assert.equal((await h.post('/api/inbox', {})).status, 405);
  assert.equal((await h.post('/api/tasks/going/thread', {})).status, 405);
  for (const bad of ['..%2Fx', 'Going', '-x', 'a%20b', 'a'.repeat(200)]) {
    assert.equal((await h.get(`/api/tasks/${bad}/thread`)).status, 400, bad);
  }
  assert.equal((await h.get('/api/tasks/nope/thread')).status, 404);
});

test('health reports inbox polling state', async () => {
  h = await makeApp();
  h.inboxRows.fail = 'focusflow exploded';
  await h.app.pollInbox();
  let j = (await h.get('/api/health')).json();
  assert.equal(j.last_inbox_error, 'focusflow exploded');
  assert.equal(j.last_inbox_poll_at, null);
  assert.equal(j.notify_error, null);
  h.inboxRows.fail = null;
  h.inboxRows.rows = [null];
  await h.app.pollInbox();
  j = (await h.get('/api/health')).json();
  assert.equal(j.last_inbox_error, null);
  assert.equal(j.inbox_malformed_count, 1);
  assert.equal(j.last_inbox_poll_at, at(2026, 9, 22, 12).toISOString());
});

test('health reports stalled_count over the live set only', async () => {
  h = await makeApp({ live: ['running'], tunables: { nudge_stall_seconds: 0 } }); // 0s: anything awaiting is immediately stalled
  await h.post('/api/tasks/running/message', { text: 'ping', mode: 'queue' });
  const health = (await h.get('/api/health')).json();
  assert.equal(health.stalled_count, 1);
});

test('the new tunables have defaults', async () => {
  const { DEFAULTS } = await import('../lib/config.js');
  assert.equal(DEFAULTS.tunables.inbox_poll_seconds, 5);
  assert.equal(DEFAULTS.tunables.notifications, true);
});
