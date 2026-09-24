import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { openStore, rebuild } from '../lib/store.js';
import { readEvents } from '../lib/event-log.js';
import { tick, ensureInstance, localDate } from '../lib/snapshotter.js';
import { state, tmpdir, at } from './helpers.js';

function setup(initial = []) {
  const dataDir = tmpdir();
  const store = openStore();
  let tasks = initial;
  const ctx = () => ({ store, dataDir, device: 'd1', readTasks: () => ({ tasks, live: new Set() }) });
  return { dataDir, store, set: t => { tasks = t; }, ctx };
}
const kinds = evs => evs.map(e => e.kind);

test('localDate uses the local calendar day', () => {
  assert.equal(localDate(at(2026, 9, 22, 0, 5)), '2026-09-22');
  assert.equal(localDate(at(2026, 9, 22, 23, 55)), '2026-09-22');
});

test('ensureInstance writes instance.created once', () => {
  const s = setup();
  assert.equal(ensureInstance({ ...s.ctx(), now: at(2026, 9, 22) }).length, 1);
  assert.equal(ensureInstance({ ...s.ctx(), now: at(2026, 9, 22, 11) }).length, 0);
  assert.deepEqual(kinds(readEvents(s.dataDir).events), ['instance.created']);
});

test('first tick: task.appeared for each task, then one snapshot.daily, no gap', () => {
  const s = setup([state(), state({ slug: 'b', name: 'B' })]);
  const r = tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  assert.deepEqual(kinds(r.events), ['task.appeared', 'task.appeared', 'snapshot.daily']);
  const snap = r.events.at(-1).payload;
  assert.equal(snap.date, '2026-09-22');
  assert.equal(snap.tasks.length, 2);
  assert.equal(s.store.tasks().length, 2);
});

test('tick with nothing changed writes nothing', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  const r = tick({ ...s.ctx(), now: at(2026, 9, 22, 10, 5) });
  assert.deepEqual(r.events, []);
  assert.equal(readEvents(s.dataDir).events.length, 2);
});

test('a change produces one task.changed with old and new values, dated by updated_at', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  const updated = at(2026, 9, 22, 10, 12).toISOString();
  s.set([state({ status: 'in-progress', has_session: true, updated_at: updated })]);
  const r = tick({ ...s.ctx(), now: at(2026, 9, 22, 10, 15) });
  assert.deepEqual(kinds(r.events), ['task.changed']);
  const e = r.events[0];
  assert.deepEqual(e.payload.changes.status, ['backlog', 'in-progress']);
  assert.deepEqual(e.payload.changes.has_session, [false, true]);
  assert.equal(e.event_time, updated);
  assert.equal(e.recorded_at, at(2026, 9, 22, 10, 15).toISOString());
  assert.equal(s.store.getTask('task-a').status, 'in-progress');
});

test('a future updated_at is not trusted as event_time', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  s.set([state({ priority: 'high', updated_at: at(2027, 1, 1).toISOString() })]);
  const r = tick({ ...s.ctx(), now: at(2026, 9, 22, 10, 5) });
  assert.equal(r.events[0].event_time, r.events[0].recorded_at);
});

test('a new task appearing later gets task.appeared', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  s.set([state(), state({ slug: 'new', name: 'New' })]);
  const r = tick({ ...s.ctx(), now: at(2026, 9, 22, 10, 5) });
  assert.deepEqual(kinds(r.events), ['task.appeared']);
  assert.equal(r.events[0].subject, 'task:new');
});

test('first reading of a new local day writes snapshot.daily once, with the current state', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 22) });
  s.set([state({ priority: 'high' })]);
  const r1 = tick({ ...s.ctx(), now: at(2026, 9, 23, 0, 5) });
  assert.deepEqual(kinds(r1.events), ['task.changed', 'snapshot.daily']);
  assert.equal(r1.events[1].payload.date, '2026-09-23');
  assert.equal(r1.events[1].payload.tasks[0].priority, 'high');
  const r2 = tick({ ...s.ctx(), now: at(2026, 9, 23, 9) });
  assert.deepEqual(r2.events, []);
  assert.deepEqual(s.store.snapshotDates('d1'), ['2026-09-22', '2026-09-23']);
});

test('skipped days produce exactly one snapshot.gap listing them, before the new snapshot', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  const r = tick({ ...s.ctx(), now: at(2026, 9, 26, 9) });
  assert.deepEqual(kinds(r.events), ['snapshot.gap', 'snapshot.daily']);
  assert.deepEqual(r.events[0].payload, { device: 'd1', dates: ['2026-09-23', '2026-09-24', '2026-09-25'] });
  assert.deepEqual(s.store.snapshotDates('d1'), ['2026-09-22', '2026-09-26']);
});

test('gap detection across a month boundary', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 29, 10) });
  const r = tick({ ...s.ctx(), now: at(2026, 10, 2, 10) });
  assert.deepEqual(r.events[0].payload.dates, ['2026-09-30', '2026-10-01']);
});

test('consecutive days record no gap', () => {
  const s = setup([state()]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  const r = tick({ ...s.ctx(), now: at(2026, 9, 23, 10) });
  assert.deepEqual(kinds(r.events), ['snapshot.daily']);
});

test('a failing reader skips the tick, writes nothing and reports the error', () => {
  const s = setup([state()]);
  const r = tick({ ...s.ctx(), readTasks: () => { throw new Error('database is locked'); }, now: at(2026, 9, 22, 10) });
  assert.equal(r.skipped, true);
  assert.match(r.error, /database is locked/);
  assert.deepEqual(r.events, []);
  assert.equal(readEvents(s.dataDir).events.length, 0);
});

test('rebuilding from the events gives the same cache the live ticks built', () => {
  const s = setup([state(), state({ slug: 'b', name: 'B' })]);
  ensureInstance({ ...s.ctx(), now: at(2026, 9, 22, 9) });
  tick({ ...s.ctx(), now: at(2026, 9, 22, 10) });
  s.set([state({ status: 'in-progress', has_session: true, harness: 'claude', updated_at: at(2026, 9, 22, 11).toISOString() }), state({ slug: 'b', name: 'B', tags: ['x'] }), state({ slug: 'c', name: 'C' })]);
  tick({ ...s.ctx(), now: at(2026, 9, 22, 12) });
  s.set([state({ status: 'done', has_session: true, harness: 'claude', archived: true }), state({ slug: 'b', name: 'B', tags: ['x'], waiting_on: 'them' }), state({ slug: 'c', name: 'C' })]);
  tick({ ...s.ctx(), now: at(2026, 9, 25, 8) });
  const { store: rebuilt, eventCount } = rebuild({ dbPath: path.join(s.dataDir, '.cache', 'store.db'), dataDir: s.dataDir });
  assert.ok(eventCount >= 8);
  assert.equal(rebuilt.hash(), s.store.hash());
  assert.deepEqual(rebuilt.dump(), s.store.dump());
  assert.deepEqual(rebuilt.tasks().map(t => t.slug), ['b', 'c', 'task-a']);
  rebuilt.close();
});
