import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs';
import { openStore, rebuild } from '../lib/store.js';
import { makeEvent, appendEvent } from '../lib/event-log.js';
import { tmpdir } from './helpers.js';

const ev = (kind, payload, o = {}) =>
  makeEvent({ device: o.device ?? 'd1', kind, subject: o.subject ?? 'task:t', payload, recordedAt: o.at ?? '2026-09-22T10:00:00.000Z', eventTime: o.eventTime });

const received = (flow_id, slug, sent_at, extra = {}) =>
  ev('message.received', { flow_id, slug, body: `body ${flow_id}`, reply_to: null, urgent: false, sent_at, seen_read: false, ...extra }, { eventTime: sent_at });

test('message.received is stored as an incoming row keyed by (device, focusflow id)', () => {
  const s = openStore();
  s.apply(received('aaaa0001', 'task-a', '2026-09-22T05:00:00.000Z', { urgent: true, reply_to: 'bbbb0002' }));
  assert.deepEqual(s.thread('d1', 'task-a'), [
    { flow_id: 'aaaa0001', slug: 'task-a', direction: 'in', body: 'body aaaa0001', reply_to: 'bbbb0002', urgent: true, at: '2026-09-22T05:00:00.000Z', seen_read: false, delivered_at: null },
  ]);
  assert.equal(s.hasMessage('d1', 'aaaa0001'), true);
  assert.equal(s.hasMessage('d2', 'aaaa0001'), false);
  assert.equal(s.hasMessages('d1'), true);
  assert.equal(s.hasMessages('d2'), false);
});

test('applying the same message twice keeps one row', () => {
  const s = openStore();
  const e = received('aaaa0001', 'task-a', '2026-09-22T05:00:00.000Z');
  s.apply(e); s.apply(e);
  assert.equal(s.dump().messages.length, 1);
});

test('a message from a sender with no task is kept but belongs to no thread', () => {
  const s = openStore();
  s.apply(received('aaaa0001', null, '2026-09-22T05:00:00.000Z'));
  assert.equal(s.hasMessage('d1', 'aaaa0001'), true);
  assert.deepEqual(s.messages('d1'), []);
  assert.equal(s.dump().messages.length, 1);
});

test('message.sent: queue rows use the focusflow id, headless rows a synthetic out:<event id> key', () => {
  const s = openStore();
  s.apply(ev('message.sent', { slug: 'task-a', text: 'hello', mode: 'queue', flow_id: 'cccc0003', ok: true }, { at: '2026-09-22T06:00:00.000Z' }));
  const headless = ev('message.sent', { slug: 'task-a', text: 'do it', mode: 'headless', ok: true }, { at: '2026-09-22T06:05:00.000Z' });
  s.apply(headless);
  const rows = s.thread('d1', 'task-a');
  assert.deepEqual(rows.map(r => [r.flow_id, r.direction, r.body, r.at]), [
    ['cccc0003', 'out', 'hello', '2026-09-22T06:00:00.000Z'],
    [`out:${headless.id}`, 'out', 'do it', '2026-09-22T06:05:00.000Z'],
  ]);
});

test('a queue send whose focusflow id could not be parsed still gets a synthetic key', () => {
  const s = openStore();
  const e = ev('message.sent', { slug: 'task-a', text: 'hi', mode: 'queue', flow_id: null, ok: true });
  s.apply(e);
  assert.equal(s.thread('d1', 'task-a')[0].flow_id, `out:${e.id}`);
});

test('a failed headless send stays in the log only', () => {
  const s = openStore();
  s.apply(ev('message.sent', { slug: 'task-a', text: 'do it', mode: 'headless', ok: false, error: 'boom' }));
  assert.deepEqual(s.thread('d1', 'task-a'), []);
  assert.equal(s.hasMessages('d1'), false);
});

test('headless_runs: a successful headless send marks the task, a failed one does not', () => {
  const s = openStore();
  s.apply(ev('message.sent', { slug: 'going', text: 'hi', mode: 'headless', ok: true }));
  assert.equal(s.everHeadless('going'), true);
  assert.equal(s.everHeadless('never-sent'), false);

  s.apply(ev('message.sent', { slug: 'other', text: 'hi', mode: 'headless', ok: false, error: 'boom' }));
  assert.equal(s.everHeadless('other'), false);

  s.apply(ev('message.sent', { slug: 'queued-only', text: 'hi', mode: 'queue', ok: true, flow_id: 'ab12cd34' }));
  assert.equal(s.everHeadless('queued-only'), false, 'queue sends never set it, only headless');
});

test('headless_runs is idempotent and keeps the first timestamp', () => {
  const s = openStore();
  const first = ev('message.sent', { slug: 'going', text: 'a', mode: 'headless', ok: true }, { at: '2026-09-22T10:00:00.000Z' });
  const second = ev('message.sent', { slug: 'going', text: 'b', mode: 'headless', ok: true }, { at: '2026-09-22T11:00:00.000Z' });
  s.apply(first);
  s.apply(second);
  const row = s.dump().headless_runs.find(r => r.slug === 'going');
  assert.equal(row.first_at, first.event_time);
});

test('thread_read only moves forward', () => {
  const s = openStore();
  s.apply(ev('message.read', { slug: 'task-a', through: '2026-09-22T07:00:00.000Z' }));
  s.apply(ev('message.read', { slug: 'task-a', through: '2026-09-22T06:00:00.000Z' }));
  assert.equal(s.threadRead('d1', 'task-a'), '2026-09-22T07:00:00.000Z');
  s.apply(ev('message.read', { slug: 'task-a', through: '2026-09-22T08:00:00.000Z' }));
  assert.equal(s.threadRead('d1', 'task-a'), '2026-09-22T08:00:00.000Z');
  assert.equal(s.threadRead('d1', 'other'), null);
});

test('unread = incoming, not seen_read, newer than thread_read', () => {
  const s = openStore();
  s.apply(received('aaaa0001', 'task-a', '2026-09-22T05:00:00.000Z'));
  s.apply(received('aaaa0002', 'task-a', '2026-09-22T05:10:00.000Z', { seen_read: true }));
  s.apply(received('aaaa0003', 'task-a', '2026-09-22T05:20:00.000Z'));
  s.apply(received('aaaa0004', 'task-b', '2026-09-22T05:30:00.000Z'));
  s.apply(ev('message.sent', { slug: 'task-a', text: 'mine', mode: 'queue', flow_id: 'cccc0003', ok: true }, { at: '2026-09-22T05:40:00.000Z' }));
  assert.deepEqual(s.unreadIncoming('d1', 'task-a').map(r => r.flow_id), ['aaaa0001', 'aaaa0003']);
  assert.deepEqual([...s.unreadCounts('d1')], [['task-a', 2], ['task-b', 1]]);
  s.apply(ev('message.read', { slug: 'task-a', through: '2026-09-22T05:10:00.000Z' }));
  assert.deepEqual(s.unreadIncoming('d1', 'task-a').map(r => r.flow_id), ['aaaa0003']);
  assert.equal(s.unreadCounts('d1').get('task-a'), 1);
});

test('messages are ordered by time then id, for one device only', () => {
  const s = openStore();
  s.apply(received('aaaa0002', 'task-a', '2026-09-22T05:00:00.000Z'));
  s.apply(received('aaaa0001', 'task-a', '2026-09-22T05:00:00.000Z'));
  s.apply(received('aaaa0000', 'task-a', '2026-09-22T04:00:00.000Z'));
  s.apply(makeEvent({ device: 'd2', kind: 'message.received', subject: 'task:task-a', payload: { flow_id: 'ffff0000', slug: 'task-a', body: 'other device', reply_to: null, urgent: false, sent_at: '2026-09-22T03:00:00.000Z', seen_read: false }, recordedAt: '2026-09-22T10:00:00.000Z' }));
  assert.deepEqual(s.thread('d1', 'task-a').map(r => r.flow_id), ['aaaa0000', 'aaaa0001', 'aaaa0002']);
  assert.equal(s.messages('d1').length, 3);
});

test('rebuild from events equals the live cache, including messages and thread_read', () => {
  const dir = tmpdir();
  const dbPath = path.join(dir, '.cache', 'store.db');
  const live = openStore(dbPath);
  const events = [
    received('aaaa0001', 'task-a', '2026-09-22T05:00:00.000Z'),
    received('aaaa0002', 'task-b', '2026-09-22T05:10:00.000Z', { seen_read: true, urgent: true }),
    received('aaaa0003', null, '2026-09-22T05:20:00.000Z'),
    ev('message.sent', { slug: 'task-a', text: 'hello', mode: 'queue', flow_id: 'cccc0003', ok: true }, { at: '2026-09-22T06:00:00.000Z' }),
    ev('message.sent', { slug: 'task-a', text: 'run', mode: 'headless', ok: true }, { at: '2026-09-22T06:05:00.000Z' }),
    ev('message.sent', { slug: 'task-a', text: 'run again', mode: 'headless', ok: false, error: 'boom' }, { at: '2026-09-22T06:06:00.000Z' }),
    ev('message.read', { slug: 'task-a', through: '2026-09-22T05:00:00.000Z' }, { at: '2026-09-22T07:00:00.000Z' }),
    ev('message.read', { slug: 'task-a', through: '2026-09-22T04:00:00.000Z' }, { at: '2026-09-22T07:01:00.000Z' }),
  ];
  for (const e of events) { appendEvent(dir, e); live.apply(e); }
  const liveHash = live.hash();
  const liveDump = live.dump();
  assert.equal(liveDump.messages.length, 5);
  assert.equal(liveDump.thread_read.length, 1);
  live.close();

  fs.writeFileSync(dbPath, 'garbage, not a database');
  const { store, eventCount } = rebuild({ dbPath, dataDir: dir });
  assert.equal(eventCount, events.length);
  assert.equal(store.hash(), liveHash);
  assert.deepEqual(store.dump(), liveDump);
  store.close();
});

test('an older reader ignores the new kinds (unknown kinds are kept in the log only)', () => {
  const s = openStore();
  s.apply(ev('message.future-kind', { anything: 1 }));
  assert.equal(s.dump().messages.length, 0);
});
