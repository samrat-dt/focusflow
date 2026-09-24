import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
// logic.js is a plain browser script: run it the way a page would and read the global it defines.
const ctx = vm.createContext({});
vm.runInContext(`${fs.readFileSync(new URL('../public/logic.js', import.meta.url), 'utf8')}\n;globalThis.__L = Logic;`, ctx);
// Values built inside the vm carry that realm's prototypes; clone them so deepEqual compares plainly.
const plain = v => (v === undefined ? v : JSON.parse(JSON.stringify(v)));
const L = Object.fromEntries(Object.entries(ctx.__L).map(([k, f]) => [k, typeof f === 'function' ? (...a) => plain(f(...a)) : f]));

test('friendlyError turns CLI output into a sentence and never leaks the prefix', () => {
  assert.equal(L.friendlyError('error: do requires a task ref'), 'Focusflow could not tell which task to use. Please try again.');
  assert.match(L.friendlyError('TypeError: Failed to fetch'), /not answering/);
  assert.equal(L.friendlyError('error: something odd happened\nusage: focusflow ...'), 'Something odd happened.');
  assert.equal(L.friendlyError(''), 'Something went wrong. Please try again.');
  assert.equal(L.friendlyError(undefined), 'Something went wrong. Please try again.');
});

test('humanTime reads like a person', () => {
  const now = new Date(2026, 8, 24, 15, 0);
  const at = (d, h, m = 0) => new Date(2026, 8, d, h, m).toISOString();
  assert.equal(L.humanTime(new Date(2026, 8, 24, 14, 59, 40).toISOString(), now), 'just now');
  assert.equal(L.humanTime(at(24, 14, 30), now), '30m ago');
  assert.equal(L.humanTime(at(24, 9), now), '6h ago');
  assert.match(L.humanTime(at(23, 10), now), /^Yesterday /);
  assert.equal(L.humanTime(at(21, 10), now), '3d ago');
  assert.equal(L.humanTime(at(1, 10), now), 'Sep 1');
  assert.equal(L.humanTime('nonsense', now), '');
});

test('duration formats seconds, minutes and hours', () => {
  assert.deepEqual([45, 125, 3725].map(L.duration), ['45s', '2m 05s', '1h 02m']);
  assert.equal(L.duration(NaN), '');
});

test('parseQuickAdd pulls out a known #project and a !priority, leaving the rest as the name', () => {
  const projects = ['website', 'ops'];
  assert.deepEqual(L.parseQuickAdd('Write launch note #website !high', projects), { name: 'Write launch note', project: 'website', priority: 'high' });
  assert.deepEqual(L.parseQuickAdd('#ops   Rotate keys', projects), { name: 'Rotate keys', project: 'ops', priority: null });
  assert.deepEqual(L.parseQuickAdd('Fix issue #123 today', projects), { name: 'Fix issue #123 today', project: null, priority: null });
  assert.deepEqual(L.parseQuickAdd('Ship it!high', projects), { name: 'Ship it!high', project: null, priority: null });
  assert.equal(L.parseQuickAdd('   ', projects).name, '');
});

test('filterTasks by project, priority and text', () => {
  const list = [
    { name: 'Alpha', slug: 'alpha', project: 'p1', priority: 'high', tags: ['ui'] },
    { name: 'Beta', slug: 'beta', project: null, priority: 'low', tags: [] },
    { name: 'Gamma', slug: 'gamma', project: 'p1', priority: 'low', tags: [] },
  ];
  assert.deepEqual(L.filterTasks(list, { project: 'p1' }).map(t => t.slug), ['alpha', 'gamma']);
  assert.deepEqual(L.filterTasks(list, { project: '__none__' }).map(t => t.slug), ['beta']);
  assert.deepEqual(L.filterTasks(list, { priority: 'low', text: 'ga' }).map(t => t.slug), ['gamma']);
  assert.deepEqual(L.filterTasks(list, { text: '#UI'.slice(1) }).map(t => t.slug), ['alpha']);
  assert.equal(L.filterTasks(list).length, 3);
});

test('dropAction maps lane moves to actions and explains the ones it refuses', () => {
  const t = status => ({ status, kind: 'regular' });
  assert.equal(L.dropAction(t('in-progress'), 'paused').action, 'pause');
  assert.equal(L.dropAction(t('in-progress'), 'review').action, 'review');
  assert.equal(L.dropAction(t('review'), 'done').action, 'approve');
  assert.equal(L.dropAction(t('paused'), 'in-progress').action, 'unpause');
  assert.equal(L.dropAction(t('backlog'), 'next').action, 'next');
  assert.match(L.dropAction(t('next'), 'in-progress').hint, /Open terminal/);
  assert.match(L.dropAction(t('in-progress'), 'done').hint, /Review/);
  assert.deepEqual(L.dropAction(t('next'), 'next'), { action: null, hint: null });
  assert.deepEqual(L.dropAction({ ...t('review'), archived: true }, 'done'), { action: null, hint: null });
  assert.match(L.dropAction({ status: 'next', kind: 'project_agent' }, 'paused').hint, /coordinator/);
});

test('isEditing and fingerprint', () => {
  assert.equal(L.isEditing({ tagName: 'INPUT' }), true);
  assert.equal(L.isEditing({ tagName: 'BUTTON' }), false);
  assert.equal(L.isEditing(null), false);
  assert.equal(L.fingerprint({ a: [1] }), L.fingerprint({ a: [1] }));
});
