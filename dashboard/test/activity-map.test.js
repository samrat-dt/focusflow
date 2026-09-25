import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { buildActivity } from '../lib/activity-map.js';
import { makeApp } from './harness.js';
import { state, tmpdir } from './helpers.js';
import { DatabaseSync } from 'node:sqlite';
import fs from 'node:fs';
import path from 'node:path';
import { readBus } from '../lib/focus-reader.js';

let h;
afterEach(() => { h?.close(); h = null; });
const T = (slug, extra = {}) => ({ ...state({ slug, name: slug.toUpperCase(), status: 'in-progress' }), ...extra });
let n = 0;
const M = (from, to, body, at, extra = {}) => ({
  id: `m${++n}`, from_assignee: 'user', from_task_slug: from, to_assignee: 'user', to_task_slug: to, body, urgent: 0, broadcast: 0,
  mail: 'unread', created_at: at, read_at: null, delivered_at: null, ...extra,
});

test('you, agents, agent-to-agent, broadcasts and watches become nodes, conversations and events', () => {
  const tasks = [T('a', { project: 'web' }), T('b', { project: 'web' }), T('web-agent', { project: 'web', kind: 'project_agent' }), T('c', { project: 'ops', headless_running: true })];
  const now = new Date('2026-09-25T12:00:00Z');
  const a = buildActivity({
    tasks, projects: [{ slug: 'web', name: 'Website' }, { slug: 'ops', name: 'Ops' }], now,
    messages: [
      M('a', null, 'Question? (via focusflow: reply with focusflow message user "<answer>")', '2026-09-25T10:00:00Z', { urgent: 1 }),
      M(null, 'a', 'Answer', '2026-09-25T10:05:00Z', { delivered_at: '2026-09-25T10:06:00Z' }),
      M('a', 'web-agent', 'a is done', '2026-09-25T11:55:00Z'),
      M('web-agent', 'b', 'please pick up the copy', '2026-09-25T11:56:00Z'),
      M('c', 'a', 'shipped', '2026-09-25T09:00:00Z', { broadcast: 1 }),
      M('c', 'b', 'shipped', '2026-09-25T09:00:00Z', { broadcast: 1 }),
    ],
    watches: [{ watcher: 'b', watched: 'c', created_at: 'x' }, { watcher: 'user/a', watched: 'web', created_at: 'x' }, { watcher: 'user', watched: 'a', created_at: 'x' }],
    nudges: [{ task_slug: 'a', attempts: 2 }],
  });
  const byId = new Map(a.nodes.map(x => [x.id, x]));
  assert.equal(byId.get('you').kind, 'you');
  assert.equal(byId.get('web-agent').name, 'Website assistant');
  assert.equal(byId.get('c').running, true);
  assert.equal(byId.get('a').nudges, 2);
  const kinds = Object.fromEntries(a.edges.map(e => [`${e.from}>${e.to}`, e.kind]));
  assert.equal(kinds['a>you'], 'you');
  assert.equal(kinds['a>web-agent'], 'agents');
  assert.equal(kinds['web-agent>b'], 'agents');
  assert.equal(kinds['c>a'], 'broadcast');
  assert.equal(kinds['b>c'], 'watch', 'a task (stored as its bare slug) watching another task');
  assert.equal(kinds['you>a'], 'watch', 'you watching a task');
  assert.ok(!byId.has('@b'), 'a watching task is never mistaken for a person');
  assert.ok(!a.edges.some(e => e.to === 'project:web'), 'project-wide watches are not drawn as edges');
  const toYou = a.edges.find(e => e.from === 'a' && e.to === 'you');
  assert.deepEqual([toYou.count, toYou.urgent, toYou.unread, toYou.both], [1, 1, 1, true]);
  assert.equal(a.edges.find(e => e.from === 'web-agent').recent, true, 'talked in the last 10 minutes');
  assert.equal(a.events[0].text, 'please pick up the copy', 'newest first');
  const bc = a.events.find(e => e.kind === 'broadcast');
  assert.deepEqual(bc.to.sort(), ['a', 'b'], 'one broadcast event with every recipient');
  assert.equal(a.events.find(e => e.text === 'Question?').text, 'Question?', 'the delivery cue is stripped');
  assert.equal(a.events.find(e => e.text === 'Answer').state, 'delivered');
  assert.deepEqual(a.stats, { agents: 4, messages: 4, between_agents: 2, broadcasts: 1, watches: 2, unread_for_you: 1, live: 1 });
});

test('an empty bus is just you', () => {
  const a = buildActivity({});
  assert.deepEqual(a.nodes.map(x => x.id), ['you']);
  assert.deepEqual([a.edges, a.events], [[], []]);
});

test('readBus compares times as instants, whatever offset the writer used', () => {
  const root = tmpdir('ff-bus-');
  const db = new DatabaseSync(path.join(root, 'focusflow.db'));
  db.exec(`CREATE TABLE bus_messages (id TEXT, from_assignee TEXT, from_task_slug TEXT, to_assignee TEXT, to_task_slug TEXT, body TEXT, urgent INT, broadcast INT, mail TEXT, created_at TEXT, read_at TEXT, delivered_at TEXT)`);
  const ins = db.prepare('INSERT INTO bus_messages VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, ?, NULL, NULL)');
  ins.run('old', 'user', 'a', 'user', null, 'old', 'read', '2026-09-24T10:00:00+05:30'); // 04:30Z: before the window
  ins.run('new', 'user', 'a', 'user', null, 'new', 'unread', '2026-09-24T12:00:00+05:30'); // 06:30Z: inside
  db.close();
  const r = readBus({ focusRoot: root, sinceIso: '2026-09-24T05:00:00Z' });
  assert.deepEqual(r.messages.map(m => m.id), ['new']);
  assert.deepEqual([r.watches, r.nudges], [[], []], 'an older database without watches or nudges still works');
});

test('/api/activity validates the window and returns the map', async () => {
  const bus = { messages: [M('going', null, 'hi', new Date().toISOString())], watches: [], nudges: [] };
  h = await makeApp({ live: [], bus });
  const r = await h.get('/api/activity?since=1d');
  assert.equal(r.status, 200);
  const j = r.json();
  assert.equal(j.since, '1d');
  assert.ok(j.nodes.some(x => x.id === 'going'));
  assert.equal((await h.get('/api/activity?since=forever')).status, 400);
  assert.equal((await h.get('/api/activity', { token: null })).status, 401);
});
