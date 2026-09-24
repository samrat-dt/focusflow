import { test } from 'node:test';
import assert from 'node:assert/strict';
import { scoreItem, isSuppressed, learnedBias, parseQuietHours, inQuietHours, createGate, waitedPhrase } from '../lib/attention.js';
import { buildNeeds } from '../lib/needs.js';
import { state } from './helpers.js';

const now = new Date('2026-09-25T12:00:00Z');
const T = (slug, extra = {}) => ({ ...state({ slug, name: slug.toUpperCase() }), signals: {}, ...extra });

test('scoreItem adds urgency, priority, age, blocking and bias, and says why', () => {
  const base = scoreItem({ kind: 'review', at: now.toISOString() }, { priority: 'low', now });
  assert.equal(base.score, 50);
  assert.deepEqual(base.reasons, []);
  const s = scoreItem({ kind: 'question', urgent: true, at: '2026-09-25T09:00:00Z' }, { priority: 'high', now, blocking: 1, bias: 5 });
  assert.equal(s.score, 60 + 30 + 10 + 6 + 10 + 5);
  assert.deepEqual(s.reasons, ['Marked urgent', 'High priority', 'Waiting 3h', 'Blocks 1 other task', 'You usually handle these quickly']);
  assert.equal(scoreItem({ kind: 'overdue', at: '2026-09-01' }, { priority: 'medium', now }).score, 40 + 4 + 20, 'age is capped');
  assert.equal(waitedPhrase(0.3), 'Waiting 18m');
  assert.equal(waitedPhrase(72), 'Waiting 3d');
});

test('snoozes and dismissals hold until something new happens', () => {
  const item = { fp: 'a' };
  assert.equal(isSuppressed(item, undefined, now), false);
  assert.equal(isSuppressed(item, { fp: 'a', until: '2026-09-25T13:00:00Z' }, now), true);
  assert.equal(isSuppressed(item, { fp: 'a', until: '2026-09-25T11:00:00Z' }, now), false, 'snooze expired');
  assert.equal(isSuppressed(item, { fp: 'a', until: null }, now), true, 'until something changes');
  assert.equal(isSuppressed(item, { fp: 'b', until: null }, now), false, 'something changed');
  assert.equal(isSuppressed(item, { fp: 'a', dismissed: true }, now), true);
});

test('learnedBias rewards what the person handles fast and needs enough history', () => {
  assert.equal(learnedBias([{ kind: 'review', waited_seconds: 10 }])('review'), 0);
  const acts = [
    ...Array.from({ length: 4 }, () => ({ kind: 'question', project: 'web', waited_seconds: 60 })),
    ...Array.from({ length: 4 }, () => ({ kind: 'review', project: 'web', waited_seconds: 7200 })),
    ...Array.from({ length: 3 }, () => ({ kind: 'overdue', project: 'ops', waited_seconds: 3600 })),
  ];
  const bias = learnedBias(acts);
  assert.ok(bias('question', 'web') > 0);
  assert.ok(bias('review', 'web') < 0);
  assert.ok(bias('review', 'web') >= -10 && bias('question', 'web') <= 10);
  assert.equal(bias('failed', 'web'), 0, 'no history for failed runs');
});

test('quiet hours parse, wrap midnight, and hold non-urgent banners for one summary', () => {
  assert.deepEqual(parseQuietHours('22:00-08:00'), { start: 1320, end: 480 });
  assert.equal(parseQuietHours('nonsense'), null);
  assert.equal(parseQuietHours('10:00-10:00'), null);
  const at = (h, m = 0) => new Date(2026, 8, 25, h, m);
  assert.equal(inQuietHours(at(23), '22:00-08:00'), true);
  assert.equal(inQuietHours(at(7, 59), '22:00-08:00'), true);
  assert.equal(inQuietHours(at(8), '22:00-08:00'), false);
  assert.equal(inQuietHours(at(13, 30), '13:00-14:00'), true);
  assert.equal(inQuietHours(at(3), null), false);

  const sent = [];
  let clock = at(23);
  const gate = createGate({ notify: a => sent.push(a), quietHours: '22:00-08:00', now: () => clock });
  gate.notify([{ slug: 'a', name: 'A', body: 'q1' }, { slug: 'b', name: 'B', body: 'boom', urgent: true }]);
  gate.notify([{ slug: 'c', name: 'C', body: 'q2' }]);
  assert.deepEqual(sent.map(x => x.map(a => a.slug)), [['b']], 'only urgent gets through at night');
  assert.equal(gate.flush(), 0, 'still quiet');
  clock = at(8, 5);
  assert.equal(gate.flush(), 2);
  assert.match(sent[1][0].body, /2 updates in 2 tasks/);
  assert.equal(gate.held(), 0);
});

test('stalled runs and blocking tasks are ranked, and suppressed items move to hidden', () => {
  const tasks = [
    T('run', { status: 'in-progress', auto_run_status: 'running', priority: 'low' }),
    T('fresh', { status: 'in-progress', auto_run_status: 'running' }),
    T('rev', { status: 'review', status_changed_at: '2026-09-25T11:00:00Z', priority: 'low' }),
    T('other', { status: 'in-progress', waiting_on: 'rev to be approved' }),
  ];
  const runActivity = new Map([['run', '2026-09-25T11:30:00Z'], ['fresh', '2026-09-25T11:58:00Z']]);
  const n = buildNeeds({ tasks, now, runActivity });
  const stalled = n.items.find(i => i.kind === 'stalled');
  assert.equal(stalled.slug, 'run');
  assert.match(stalled.detail, /no progress for 30m/);
  assert.ok(!n.items.some(i => i.slug === 'fresh'), 'a run that moved recently is fine');
  const rev = n.items.find(i => i.key === 'review:rev');
  assert.ok(rev.reasons.includes('Blocks 1 other task'));
  assert.equal(n.badge, 2, 'stalled runs and reviews count toward the badge');

  const rules = new Map([['review:rev', { fp: rev.fp, until: null }]]);
  const m = buildNeeds({ tasks, now, runActivity, rules });
  assert.ok(!m.items.some(i => i.key === 'review:rev'));
  assert.deepEqual(m.hidden.map(i => [i.key, i.snoozed_until, i.dismissed]), [['review:rev', null, false]]);
});
