import { test } from 'node:test';
import assert from 'node:assert/strict';
import { signalsFor } from '../lib/signals.js';
import { state, at } from './helpers.js';

const T = { stale_days: 5, due_soon_days: 3 };
const now = at(2026, 9, 22, 12);
const sig = (o, live = false) => signalsFor(state(o), { live, now, tunables: T });

test('due: overdue, today and within the window are flagged; later or none are not', () => {
  assert.equal(sig({ due_date: '2026-09-20' }).due, 'overdue');
  assert.equal(sig({ due_date: '2026-09-22' }).due, 'soon');
  assert.equal(sig({ due_date: '2026-09-25' }).due, 'soon');
  assert.equal(sig({ due_date: '2026-09-26' }).due, null);
  assert.equal(sig({ due_date: null }).due, null);
  assert.equal(sig({ due_date: 'not-a-date' }).due, null);
});

test('stale: only in-progress, non-archived tasks idle for stale_days or more', () => {
  const old = at(2026, 9, 16, 12).toISOString();
  const recent = at(2026, 9, 18, 12).toISOString();
  assert.equal(sig({ status: 'in-progress', updated_at: old }).stale, true);
  assert.equal(sig({ status: 'in-progress', updated_at: recent }).stale, false);
  assert.equal(sig({ status: 'in-progress', updated_at: old, session_last_resumed: recent }).stale, false);
  assert.equal(sig({ status: 'backlog', updated_at: old }).stale, false);
  assert.equal(sig({ status: 'done', updated_at: old }).stale, false);
  assert.equal(sig({ status: 'in-progress', updated_at: old, archived: true }).stale, false);
});

test('waiting and live reflect the task and the current reading', () => {
  assert.equal(sig({ waiting_on: 'legal' }).waiting, true);
  assert.equal(sig({}).waiting, false);
  assert.equal(sig({}, true).live, true);
  assert.equal(sig({}, false).live, false);
});

test('working is true only when both live and the working flag are true', () => {
  const s = (live, working) => signalsFor(state({}), { live, working, now, tunables: T });
  assert.equal(s(true, true).working, true);
  assert.equal(s(true, false).working, false);
  assert.equal(s(false, true).working, false, 'working cannot be true without live');
  assert.equal(s(false, false).working, false);
});
