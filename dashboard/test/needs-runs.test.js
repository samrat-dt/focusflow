import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { buildNeeds } from '../lib/needs.js';
import { describeBlock, lastActivity, transcriptPath } from '../lib/activity.js';
import { tailLog, stripAnsi, pidAlive } from '../lib/runs.js';
import { state, tmpdir } from './helpers.js';

const T = (slug, extra = {}) => ({ ...state({ slug, name: slug.toUpperCase() }), signals: {}, ...extra });
const M = (slug, direction, body, at, extra = {}) => ({ slug, direction, body, at, urgent: false, seen_read: false, ...extra });

test('needs-you gathers review, questions, overdue, waiting and failed runs, urgent first', () => {
  const tasks = [
    T('rev', { status: 'review', status_changed_at: '2026-09-24T10:00:00Z' }),
    T('late', { status: 'in-progress', due_date: '2026-09-01', signals: { due: 'overdue' } }),
    T('wait', { status: 'in-progress', waiting_on: 'legal' }),
    T('dead', { status: 'in-progress', auto_run_status: 'dead' }),
    T('ask', { status: 'in-progress' }), T('urgent', { status: 'in-progress' }), T('answered', { status: 'in-progress' }),
    T('fin', { status: 'done' }), T('arch', { status: 'review', archived: true }),
    T('p-agent', { status: 'review', kind: 'project_agent' }),
  ];
  const messages = [
    M('ask', 'in', 'Square or 4:5?', '2026-09-24T09:00:00Z'),
    M('urgent', 'in', 'Blocked!', '2026-09-24T08:00:00Z', { urgent: true }),
    M('answered', 'in', 'q', '2026-09-24T07:00:00Z'), M('answered', 'out', 'a', '2026-09-24T07:05:00Z'),
    M('fin', 'in', 'done thing', '2026-09-24T06:00:00Z'),
  ];
  const n = buildNeeds({ tasks, messages });
  assert.deepEqual(n.items.map(i => `${i.kind}:${i.slug}`), [
    'question:urgent', 'failed:dead', 'question:ask', 'review:rev', 'overdue:late', 'waiting:wait',
  ]);
  assert.deepEqual(n.counts, { failed: 1, question: 2, review: 1, overdue: 1, waiting: 1 });
  assert.equal(n.total, 6);
  assert.equal(n.badge, 4, 'the Dock badge counts what needs an answer or an approval');
  assert.equal(n.items[0].urgent, true);
  assert.equal(n.items.find(i => i.slug === 'ask').detail, 'Square or 4:5?');
  assert.deepEqual(buildNeeds({ tasks: [], messages: [] }), { items: [], counts: { failed: 0, question: 0, review: 0, overdue: 0, waiting: 0 }, total: 0, badge: 0 });
});

test('a coordinator question is a question but never a review item', () => {
  const n = buildNeeds({ tasks: [T('p-agent', { status: 'in-progress', kind: 'project_agent' })], messages: [M('p-agent', 'in', 'Which subtask?', '2026-09-24T09:00:00Z')] });
  assert.deepEqual(n.items.map(i => [i.kind, i.agent]), [['question', true]]);
});

test('describeBlock turns tool calls into plain phrases', () => {
  assert.equal(describeBlock({ type: 'tool_use', name: 'Bash', input: { command: 'npm test' } }).summary, 'Running: npm test');
  assert.equal(describeBlock({ type: 'tool_use', name: 'Edit', input: { file_path: '/a/b/README.md' } }).summary, 'Editing README.md');
  assert.equal(describeBlock({ type: 'tool_use', name: 'Read', input: { file_path: '/a/x.js' } }).summary, 'Reading x.js');
  assert.equal(describeBlock({ type: 'tool_use', name: 'Grep', input: { pattern: 'foo' } }).summary, 'Searching: foo');
  assert.equal(describeBlock({ type: 'tool_use', name: 'Mystery' }).summary, 'Using Mystery');
  assert.deepEqual(describeBlock({ type: 'text', text: '  Done — all tests pass.  ' }), { kind: 'text', summary: 'Done — all tests pass.' });
  assert.equal(describeBlock({ type: 'text', text: '   ' }), null);
  assert.equal(describeBlock({ type: 'thinking' }), null);
});

test('lastActivity reads the newest assistant block from the transcript tail; unknown is null', () => {
  const home = tmpdir('cc-act-home-');
  const workDir = '/work/.my_proj/x';
  const file = transcriptPath({ workDir, sessionId: 's1', home });
  assert.match(file, /-work--my-proj-x\/s1\.jsonl$/, 'dots and underscores are encoded like claude does');
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const rec = (type, content, ts) => JSON.stringify({ type, timestamp: ts, message: { content } });
  fs.writeFileSync(file, [
    rec('user', [{ type: 'text', text: 'go' }], '2026-09-24T10:00:00Z'),
    rec('assistant', [{ type: 'text', text: 'Starting.' }], '2026-09-24T10:00:05Z'),
    rec('assistant', [{ type: 'tool_use', name: 'Bash', input: { command: 'make test' } }], '2026-09-24T10:00:09Z'),
    'not json',
  ].join('\n') + '\n');
  assert.deepEqual(lastActivity({ workDir, sessionId: 's1', home }), { kind: 'tool', summary: 'Running: make test', at: '2026-09-24T10:00:09Z' });
  assert.equal(lastActivity({ workDir, sessionId: 'missing', home }), null);
});

test('tailLog strips colours, keeps the last lines, and refuses paths outside its directory', () => {
  const dir = tmpdir('cc-log-');
  const file = path.join(dir, 'run.log');
  fs.writeFileSync(file, Array.from({ length: 100 }, (_, i) => `\u001b[32mline ${i}\u001b[0m`).join('\n'));
  const t = tailLog(file, { allowedDir: dir, maxLines: 3 });
  assert.equal(t, 'line 97\nline 98\nline 99');
  const outside = tmpdir('cc-out-');
  fs.writeFileSync(path.join(outside, 's.txt'), 'secret');
  assert.equal(tailLog(path.join(outside, 's.txt'), { allowedDir: dir }), null);
  assert.equal(tailLog(path.join(dir, '..', path.basename(outside), 's.txt'), { allowedDir: dir }), null);
  assert.equal(tailLog(null, { allowedDir: dir }), null);
  assert.equal(stripAnsi('\u001b[1mA\u001b[0m'), 'A');
  assert.equal(pidAlive(process.pid), true);
  assert.equal(pidAlive(0), false);
});
