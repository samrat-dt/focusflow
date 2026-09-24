import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { openStore, rebuild } from '../lib/store.js';
import { makeEvent, appendEvent, readEvents } from '../lib/event-log.js';
import { createPoller, parseRow } from '../lib/inbox.js';
import { tmpdir, at } from './helpers.js';

let n = 0;
const row = (slug, body, when, extra = {}) => ({
  id: (++n).toString(16).padStart(8, '0'),
  created_at: at(2026, 9, 22, ...when).toISOString(),
  kind: 'message',
  from: slug ? { assignee: 'user', task_slug: slug } : { assignee: 'user' },
  to: { assignee: 'user' },
  body, urgent: false, status: 'delivered', mail: 'unread', reply_to: null,
  ...extra,
});

function setup({ rows = [], store, dataDir, seed = [] } = {}) {
  dataDir = dataDir ?? tmpdir('ff-poll-');
  store = store ?? openStore();
  for (const t of seed) store.apply(makeEvent({ device: 'd1', kind: 'task.appeared', subject: `task:${t.slug}`, payload: { state: t }, recordedAt: '2026-09-22T00:00:00.000Z' }));
  const box = { rows, fail: null, locked: false, calls: [], notified: [] };
  const record = (kind, subject, payload, eventTime) => {
    const e = makeEvent({ device: 'd1', kind, subject, payload, eventTime, recordedAt: at(2026, 9, 22, 12).toISOString() });
    appendEvent(dataDir, e);
    store.apply(e);
  };
  const poller = createPoller({
    config: { device: 'd1' }, store, record,
    notify: async a => { box.notified.push(a); },
    isLocked: () => box.locked,
    now: () => at(2026, 9, 22, 12),
    runFocusCmd: async (args, timeoutMs) => {
      box.calls.push({ args, timeoutMs });
      if (box.fail) throw new Error(box.fail);
      return { stdout: typeof box.rows === 'string' ? box.rows : JSON.stringify(box.rows), stderr: '' };
    },
  });
  return { ...box, box, store, dataDir, poller };
}

const eventsOf = dir => readEvents(dir).events.filter(e => e.kind.startsWith('message.'));

test('lists the user queue read-only: never pops, exact argv', async () => {
  const t = setup({ rows: [row('task-a', 'hi', [10])] });
  await t.poller.pollOnce();
  await t.poller.pollOnce();
  assert.deepEqual(t.box.calls.map(c => c.args), [['inbox', '--all', '--as', 'user', '--json'], ['inbox', '--all', '--as', 'user', '--json']]);
});

test('imports unseen rows oldest first as message.received with UTC times', async () => {
  const t = setup({ rows: [row('task-b', 'second', [11]), row('task-a', 'first', [10])] });
  await t.poller.pollOnce(); // backlog: also exercises ordering
  const evs = eventsOf(t.dataDir);
  assert.deepEqual(evs.map(e => e.payload.body), ['first', 'second']);
  assert.deepEqual(evs.map(e => e.subject), ['task:task-a', 'task:task-b']);
  assert.equal(evs[0].payload.sent_at, at(2026, 9, 22, 10).toISOString());
  assert.equal(evs[0].event_time, at(2026, 9, 22, 10).toISOString());
  assert.deepEqual(Object.keys(evs[0].payload).sort(), ['body', 'flow_id', 'reply_to', 'seen_read', 'sent_at', 'slug', 'urgent']);
});

test('dedupes by id: a second poll adds nothing', async () => {
  const t = setup({ rows: [row('task-a', 'hi', [10])] });
  await t.poller.pollOnce();
  const r = await t.poller.pollOnce();
  assert.equal(r.imported, 0);
  assert.equal(eventsOf(t.dataDir).length, 1);
});

test('a duplicate id inside one listing is imported once', async () => {
  const a = row('task-a', 'hi', [10]);
  const t = setup({ rows: [a, { ...a }] });
  await t.poller.pollOnce();
  assert.equal(eventsOf(t.dataDir).length, 1);
});

test('the first poll on an empty store imports the backlog as read, without notifying', async () => {
  const t = setup({ rows: [row('task-a', 'old news', [9]), row('task-a', 'older news', [8])], seed: [{ slug: 'task-a', name: 'Task A' }] });
  const r = await t.poller.pollOnce();
  assert.equal(r.imported, 2);
  assert.equal(r.notified, 0);
  assert.deepEqual(t.box.notified, []);
  assert.ok(eventsOf(t.dataDir).every(e => e.payload.seen_read === true));
  assert.equal(t.store.unreadCounts('d1').size, 0);
});

test('a message that arrives after the first poll notifies, even if the first listing was empty', async () => {
  const t = setup({ rows: [], seed: [{ slug: 'task-a', name: 'Task A' }] });
  await t.poller.pollOnce();
  t.box.rows = [row('task-a', 'ping', [10], { urgent: true })];
  const r = await t.poller.pollOnce();
  assert.equal(r.notified, 1);
  assert.deepEqual(t.box.notified, [[{ slug: 'task-a', name: 'Task A', body: 'ping', urgent: true }]]);
  assert.equal(t.store.unreadCounts('d1').get('task-a'), 1);
});

test('a message focusflow already labels read is stored as read and does not notify', async () => {
  const t = setup({ rows: [], seed: [{ slug: 'task-a', name: 'Task A' }] });
  await t.poller.pollOnce();
  t.box.rows = [row('task-a', 'seen elsewhere', [10], { mail: 'read' })];
  const r = await t.poller.pollOnce();
  assert.equal(r.imported, 1);
  assert.equal(r.notified, 0);
  assert.equal(eventsOf(t.dataDir)[0].payload.seen_read, true);
});

test('several arrivals in one poll go to the notifier together, in order', async () => {
  const t = setup({ rows: [], seed: [{ slug: 'task-a', name: 'Task A' }, { slug: 'task-b', name: 'Task B' }] });
  await t.poller.pollOnce();
  t.box.rows = [row('task-b', 'two', [11]), row('task-a', 'one', [10]), row('task-a', 'three', [12])];
  await t.poller.pollOnce();
  assert.equal(t.box.notified.length, 1);
  assert.deepEqual(t.box.notified[0].map(a => a.body), ['one', 'two', 'three']);
});

test('an arrival for a task the cache does not know is announced under its slug', async () => {
  const t = setup({ rows: [] });
  await t.poller.pollOnce();
  t.box.rows = [row('mystery', 'hello', [10])];
  await t.poller.pollOnce();
  assert.equal(t.box.notified[0][0].name, 'mystery');
});

test('a failing focusflow call leaves no events, is reported, and the next poll recovers', async () => {
  const t = setup({ rows: [row('task-a', 'hi', [10])] });
  t.box.fail = 'focusflow exited 1: database is locked';
  const r = await t.poller.pollOnce();
  assert.equal(r.skipped, true);
  assert.equal(r.error, 'focusflow exited 1: database is locked');
  assert.equal(eventsOf(t.dataDir).length, 0);
  assert.equal(t.poller.status().last_inbox_error, 'focusflow exited 1: database is locked');
  t.box.fail = null;
  await t.poller.pollOnce();
  assert.equal(eventsOf(t.dataDir).length, 1);
  assert.equal(t.poller.status().last_inbox_error, null);
  assert.equal(t.poller.status().last_inbox_poll_at, at(2026, 9, 22, 12).toISOString());
});

test('a failed first poll does not use up the backlog rule', async () => {
  const t = setup({ rows: [row('task-a', 'old', [9])], seed: [{ slug: 'task-a', name: 'Task A' }] });
  t.box.fail = 'down';
  await t.poller.pollOnce();
  t.box.fail = null;
  await t.poller.pollOnce();
  assert.equal(t.box.notified.length, 0);
  assert.equal(eventsOf(t.dataDir)[0].payload.seen_read, true);
});

test('output that is not a list is an error, not an import', async () => {
  const t = setup({ rows: '{"oops": true}' });
  const r = await t.poller.pollOnce();
  assert.equal(r.skipped, true);
  assert.match(r.error, /list/);
  t.box.rows = 'not json';
  assert.equal((await t.poller.pollOnce()).skipped, true);
});

test('empty output and null count as an empty queue', async () => {
  const t = setup({ rows: '' });
  assert.equal((await t.poller.pollOnce()).skipped, false);
  t.box.rows = 'null\n';
  assert.equal((await t.poller.pollOnce()).skipped, false);
  t.box.rows = '[]';
  assert.equal((await t.poller.pollOnce()).skipped, false);
});

test('malformed rows are skipped and counted; good rows still import', async () => {
  const good = row('task-a', 'ok', [10]);
  const t = setup({ rows: [null, 5, { id: '../x', body: 'b', created_at: good.created_at }, { ...good, id: 'bad00001', body: 7 }, { ...good, id: 'bad00002', created_at: 'yesterday-ish' }, good] });
  await t.poller.pollOnce();
  assert.equal(eventsOf(t.dataDir).length, 1);
  assert.equal(t.poller.status().inbox_malformed_count, 5);
  t.box.rows = [good];
  await t.poller.pollOnce();
  assert.equal(t.poller.status().inbox_malformed_count, 0);
});

test('a sender with no task is imported under subject "inbox", belongs to no thread, and does not notify', async () => {
  const t = setup({ rows: [] });
  await t.poller.pollOnce();
  t.box.rows = [row(null, 'anonymous', [10])];
  await t.poller.pollOnce();
  const e = eventsOf(t.dataDir)[0];
  assert.equal(e.subject, 'inbox');
  assert.equal(e.payload.slug, null);
  assert.deepEqual(t.box.notified, []);
  assert.deepEqual(t.store.messages('d1'), []);
});

test('unusable slugs and reply ids are dropped, not passed on', () => {
  const m = parseRow({ ...row('Not A Slug', 'x', [10]), reply_to: '--evil' });
  assert.equal(m.slug, null);
  assert.equal(m.reply_to, null);
});

test('nothing is polled while a git pull owns data/, and polls never overlap', async () => {
  const t = setup({ rows: [row('task-a', 'hi', [10])] });
  t.box.locked = true;
  assert.equal((await t.poller.pollOnce()).skipped, true);
  assert.equal(t.box.calls.length, 0);
  t.box.locked = false;
  const [a, b] = await Promise.all([t.poller.pollOnce(), t.poller.pollOnce()]);
  assert.deepEqual([a.skipped, b.skipped].sort(), [false, true]);
  assert.equal(t.box.calls.length, 1);
});

test('after a restart (rebuild from events) nothing is imported or notified twice', async () => {
  const dir = tmpdir('ff-poll-restart-');
  const rows = [];
  const t1 = setup({ rows, dataDir: dir, seed: [{ slug: 'task-a', name: 'Task A' }] });
  await t1.poller.pollOnce();
  rows.push(row('task-a', 'ping', [10]));
  t1.box.rows = rows;
  await t1.poller.pollOnce();
  assert.equal(t1.box.notified.length, 1);

  const { store } = rebuild({ dbPath: path.join(tmpdir(), 'store.db'), dataDir: dir });
  const t2 = setup({ rows, dataDir: dir, store });
  const r = await t2.poller.pollOnce();
  assert.equal(r.imported, 0);
  assert.deepEqual(t2.box.notified, []);
});
