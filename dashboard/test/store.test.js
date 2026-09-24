import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs';
import { openStore, rebuild } from '../lib/store.js';
import { makeEvent, appendEvent } from '../lib/event-log.js';
import { state, tmpdir } from './helpers.js';

const ev = (kind, payload, o = {}) =>
  makeEvent({ device: 'd1', kind, subject: o.subject ?? 'x', payload, recordedAt: o.at ?? '2026-09-22T10:00:00.000Z', eventTime: o.eventTime });

test('task.appeared then task.changed builds current state', () => {
  const s = openStore();
  s.apply(ev('task.appeared', { state: state() }, { subject: 'task:task-a' }));
  s.apply(ev('task.changed', { slug: 'task-a', changes: { status: ['backlog', 'in-progress'] } }, { at: '2026-09-22T11:00:00.000Z' }));
  assert.equal(s.getTask('task-a').status, 'in-progress');
  assert.equal(s.tasks().length, 1);
});

test('task.changed for an unknown task is counted, not invented', () => {
  const s = openStore();
  s.apply(ev('task.changed', { slug: 'ghost', changes: { status: ['a', 'b'] } }));
  assert.equal(s.getTask('ghost'), null);
  assert.equal(s.dump().anomalies, 1);
});

test('snapshot.daily is stored once per (date, device)', () => {
  const s = openStore();
  const e1 = ev('snapshot.daily', { date: '2026-09-22', tasks: [state()] });
  const e2 = makeEvent({ device: 'd1', kind: 'snapshot.daily', subject: 'x', payload: { date: '2026-09-22', tasks: [state(), state({ slug: 'b' })] }, recordedAt: '2026-09-22T12:00:00.000Z' });
  s.apply(e1); s.apply(e2);
  assert.deepEqual(s.snapshotDates('d1'), ['2026-09-22']);
  assert.equal(s.dump().snapshots.length, 1);
});

test('snapshot.gap dates are stored and deduplicated', () => {
  const s = openStore();
  s.apply(ev('snapshot.gap', { device: 'd1', dates: ['2026-09-20', '2026-09-21'] }));
  s.apply(ev('snapshot.gap', { device: 'd1', dates: ['2026-09-21'] }));
  assert.deepEqual(s.dump().gaps.map(g => g.date), ['2026-09-20', '2026-09-21']);
});

test('from another device an older change does not overwrite a newer state', () => {
  const s = openStore();
  s.apply(makeEvent({ device: 'd1', kind: 'task.appeared', subject: 't', payload: { state: state({ status: 'backlog' }) }, recordedAt: '2026-09-22T10:00:00.000Z' }));
  s.apply(makeEvent({ device: 'd1', kind: 'task.changed', subject: 't', payload: { slug: 'task-a', changes: { status: ['backlog', 'done'] } }, recordedAt: '2026-09-22T12:00:00.000Z' }));
  s.apply(makeEvent({ device: 'd2', kind: 'task.changed', subject: 't', payload: { slug: 'task-a', changes: { status: ['backlog', 'in-progress'] } }, recordedAt: '2026-09-22T13:00:00.000Z', eventTime: '2026-09-22T11:00:00.000Z' }));
  assert.equal(s.getTask('task-a').status, 'done');
});

test('on the same device, later-recorded events win even if the clock went backwards', () => {
  const s = openStore();
  s.apply(ev('task.appeared', { state: state() }, { subject: 't' }));
  s.apply(makeEvent({ device: 'd1', kind: 'task.changed', subject: 't', payload: { slug: 'task-a', changes: { status: ['backlog', 'in-progress'] } }, recordedAt: '2026-09-22T12:00:00.000Z', eventTime: '2026-09-22T12:00:00.000Z' }));
  s.apply(makeEvent({ device: 'd1', kind: 'task.changed', subject: 't', payload: { slug: 'task-a', changes: { status: ['in-progress', 'done'] } }, recordedAt: '2026-09-22T12:05:00.000Z', eventTime: '2026-09-22T09:00:00.000Z' }));
  assert.equal(s.getTask('task-a').status, 'done');
});

test('capture.created and open.requested are recorded', () => {
  const s = openStore();
  s.apply(ev('capture.created', { slug: 'q', name: 'Q' }));
  s.apply(ev('open.requested', { slug: 'q' }));
  const d = s.dump();
  assert.equal(d.captures.length, 1);
  assert.equal(d.opens.length, 1);
});

test('rebuild replays the log into a fresh cache and equals the live cache (same hash)', () => {
  const dir = tmpdir();
  const dbPath = path.join(dir, '.cache', 'store.db');
  const live = openStore(dbPath);
  const events = [
    ev('instance.created', { name: 'cc' }, { at: '2026-09-20T08:00:00.000Z' }),
    ev('task.appeared', { state: state() }, { at: '2026-09-20T08:01:00.000Z' }),
    ev('task.appeared', { state: state({ slug: 'b', name: 'B' }) }, { at: '2026-09-20T08:01:00.000Z' }),
    ev('snapshot.daily', { date: '2026-09-20', tasks: [state(), state({ slug: 'b', name: 'B' })] }, { at: '2026-09-20T08:02:00.000Z' }),
    ev('task.changed', { slug: 'task-a', changes: { status: ['backlog', 'in-progress'], has_session: [false, true] } }, { at: '2026-09-21T09:00:00.000Z' }),
    ev('snapshot.gap', { device: 'd1', dates: ['2026-09-21'] }, { at: '2026-09-22T09:00:00.000Z' }),
    ev('capture.created', { slug: 'c', name: 'C' }, { at: '2026-09-22T09:01:00.000Z' }),
  ];
  for (const e of events) { appendEvent(dir, e); live.apply(e); }
  const liveHash = live.hash();
  const liveDump = live.dump();
  live.close();

  fs.writeFileSync(dbPath, 'garbage, not a database');
  const { store, eventCount, malformed } = rebuild({ dbPath, dataDir: dir });
  assert.equal(eventCount, events.length);
  assert.deepEqual(malformed, []);
  assert.equal(store.hash(), liveHash);
  assert.deepEqual(store.dump(), liveDump);
  store.close();
});

test('rebuild works when no cache exists yet and when there are no events', () => {
  const dir = tmpdir();
  const { store, eventCount } = rebuild({ dbPath: path.join(dir, '.cache', 'store.db'), dataDir: dir });
  assert.equal(eventCount, 0);
  assert.equal(store.tasks().length, 0);
  store.close();
});
