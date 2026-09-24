import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { makeApp, focusRow } from './harness.js';
import { readEvents } from '../lib/event-log.js';
import { rebuild } from '../lib/store.js';
import { at, tmpdir } from './helpers.js';

let h;
beforeEach(() => { h = null; });
afterEach(() => { h?.close(); });

const readCalls = () => h.calls.filter(c => c.args[0] === 'inbox' && c.args[1] === 'read').map(c => c.args);
const readLog = () => readEvents(h.config.dataDir).events.filter(e => e.kind === 'message.read');

async function withNews(rows, extra = {}) {
  h = await makeApp(extra);
  await h.app.pollInbox();
  h.inboxRows.rows = rows;
  await h.app.pollInbox();
}

test('opening a thread marks it read and calls `focusflow inbox read` once per covered message', async () => {
  const a = focusRow('going', 'one', [10, 0]);
  const b = focusRow('going', 'two', [10, 30]);
  await withNews([a, b, focusRow('todo', 'other thread', [11, 0])]);
  const r = await h.post('/api/tasks/going/read', {});
  assert.equal(r.status, 200);
  assert.deepEqual(r.json(), { ok: true, marked: 2 });
  assert.deepEqual(readCalls(), [['inbox', 'read', a.id], ['inbox', 'read', b.id]]);
  const ev = readLog();
  assert.equal(ev.length, 1);
  assert.deepEqual(ev[0].payload, { slug: 'going', through: at(2026, 9, 22, 10, 30).toISOString() });
  const inbox = (await h.get('/api/inbox')).json();
  assert.equal(inbox.threads.find(t => t.slug === 'going').unread, 0);
  assert.equal(inbox.threads.find(t => t.slug === 'todo').unread, 1);
  assert.equal(inbox.unread_total, 1);
});

test('reading again with nothing new adds no event and calls nothing', async () => {
  await withNews([focusRow('going', 'one', [10, 0])]);
  await h.post('/api/tasks/going/read', {});
  const before = h.calls.length;
  const r = await h.post('/api/tasks/going/read', {});
  assert.deepEqual(r.json(), { ok: true, marked: 0 });
  assert.equal(readLog().length, 1);
  assert.equal(h.calls.length, before);
});

test('a message that arrives later is covered by the next read only', async () => {
  const a = focusRow('going', 'one', [10, 0]);
  await withNews([a]);
  await h.post('/api/tasks/going/read', {});
  const b = focusRow('going', 'two', [10, 45]);
  h.inboxRows.rows = [a, b];
  await h.app.pollInbox();
  assert.equal((await h.get('/api/tasks/going/thread')).json().messages[1].unread, true);
  await h.post('/api/tasks/going/read', {});
  assert.deepEqual(readCalls(), [['inbox', 'read', a.id], ['inbox', 'read', b.id]]);
  assert.equal(readLog().length, 2);
});

test('messages imported as already read are not covered again', async () => {
  h = await makeApp();
  await h.app.pollInbox();
  h.inboxRows.rows = [focusRow('going', 'seen elsewhere', [10, 0], { mail: 'read' })];
  await h.app.pollInbox();
  assert.deepEqual((await h.post('/api/tasks/going/read', {})).json(), { ok: true, marked: 0 });
  assert.equal(readCalls().length, 0);
});

test('failures of `focusflow inbox read` are ignored: the thread is still read locally', async () => {
  const a = focusRow('going', 'one', [10, 0]);
  await withNews([a], { focusflow: async args => { if (args[1] === 'read') throw new Error('focusflow: no such message'); return { stdout: '', stderr: '' }; } });
  const r = await h.post('/api/tasks/going/read', {});
  assert.equal(r.status, 200);
  assert.equal(r.json().marked, 1);
  assert.equal((await h.get('/api/inbox')).json().unread_total, 0);
});

test('the read state survives a rebuild from events', async () => {
  await withNews([focusRow('going', 'one', [10, 0])]);
  await h.post('/api/tasks/going/read', {});
  const { store } = rebuild({ dbPath: path.join(tmpdir(), 's.db'), dataDir: h.config.dataDir });
  assert.equal(store.unreadCounts('d1').size, 0);
  assert.deepEqual(store.dump().thread_read, h.store.dump().thread_read);
  store.close();
});

test('validation: token, method, slug, JSON body', async () => {
  h = await makeApp();
  assert.equal((await h.post('/api/tasks/going/read', {}, { token: 'wrong' })).status, 401);
  assert.equal((await h.get('/api/tasks/going/read')).status, 405);
  assert.equal((await h.post('/api/tasks/nope/read', {})).status, 404);
  assert.equal((await h.post('/api/tasks/Bad/read', {})).status, 400);
  assert.equal((await h.post('/api/tasks/going/read', undefined, { raw: 'x', type: 'text/plain' })).status, 415);
});
