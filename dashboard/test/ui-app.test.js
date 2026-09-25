import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import * as L from '../public/ui/js/logic.js';
import { ABOUT, HELP, UPDATED } from '../public/ui/js/content.js';
import { makeApp } from './harness.js';

let h;
afterEach(() => { h?.close(); h = null; });

test('wording helpers', () => {
  assert.equal(L.friendlyError('error: do requires a task ref'), 'Focusflow could not tell which task to use. Please try again.');
  assert.equal(L.friendlyError({ message: 'error: bad thing' }), 'Bad thing.');
  const now = new Date(2026, 8, 25, 15, 0);
  assert.equal(L.humanTime(new Date(2026, 8, 25, 14, 30).toISOString(), now), '30m ago');
  assert.equal(L.humanTime(new Date(2026, 8, 25, 15, 20).toISOString(), now), 'in 20m');
  assert.equal(L.humanTime(new Date(2026, 8, 24, 9).toISOString(), now), 'Yesterday');
  assert.equal(L.humanTime(new Date(2025, 0, 2).toISOString(), now), 'Jan 2, 2025');
  assert.deepEqual([45, 125, 3725].map(L.duration), ['45s', '2m', '1h 02m']);
  assert.equal(L.greeting(new Date(2026, 8, 25, 9)), 'Good morning');
  assert.equal(L.STAGE_NAME.next, 'Up next');
});

test('parseQuickAdd takes a known #project and a !priority out of the name; unknown #words stay', () => {
  const projects = ['website', 'ops'];
  assert.deepEqual({ ...L.parseQuickAdd('Write launch note #website !high', projects) }, { name: 'Write launch note', project: 'website', priority: 'high' });
  assert.deepEqual({ ...L.parseQuickAdd('#ops   Rotate keys', projects) }, { name: 'Rotate keys', project: 'ops', priority: null });
  assert.deepEqual({ ...L.parseQuickAdd('Fix issue #123 today', projects) }, { name: 'Fix issue #123 today', project: null, priority: null });
  assert.equal(L.parseQuickAdd('   ', projects).name, '');
});

test('filterTasks by project, priority, stage and text', () => {
  const list = [
    { name: 'Alpha', slug: 'alpha', project: 'p1', priority: 'high', status: 'next', tags: ['ui'] },
    { name: 'Beta', slug: 'beta', project: null, priority: 'low', status: 'review', tags: [] },
    { name: 'Gamma', slug: 'gamma', project: 'p1', priority: 'low', status: 'next', tags: [] },
  ];
  assert.deepEqual(L.filterTasks(list, { project: 'p1' }).map(t => t.slug), ['alpha', 'gamma']);
  assert.deepEqual(L.filterTasks(list, { project: '__none__' }).map(t => t.slug), ['beta']);
  assert.deepEqual(L.filterTasks(list, { priority: 'low', text: 'ga' }).map(t => t.slug), ['gamma']);
  assert.deepEqual(L.filterTasks(list, { stage: 'review' }).map(t => t.slug), ['beta']);
  assert.deepEqual(L.filterTasks(list, { text: 'ui' }).map(t => t.slug), ['alpha'], 'tags match too');
});

test('primaryAction gives each stage one obvious next step', () => {
  const p = status => L.primaryAction({ status }).label;
  assert.deepEqual(['backlog', 'next', 'in-progress', 'paused', 'review', 'done'].map(p), ['Move to Up next', 'Start', 'Message', 'Resume', 'Approve', 'Reopen']);
  assert.equal(L.primaryAction({ status: 'review', archived: true }).label, 'Restore');
});

test('dropAction maps every board move, and refuses to move a project assistant', () => {
  const t = status => ({ status, kind: 'regular' });
  assert.equal(L.dropAction(t('in-progress'), 'review').action, 'review');
  assert.equal(L.dropAction(t('review'), 'done').action, 'approve');
  assert.equal(L.dropAction(t('next'), 'backlog').action, 'backlog');
  assert.equal(L.dropAction(t('next'), 'in-progress').action, 'unpause');
  assert.equal(L.dropAction(t('backlog'), 'done').action, null);
  assert.match(L.dropAction({ status: 'next', kind: 'project_agent' }, 'paused').hint, /assistant/);
});

test('layoutActivity puts you in the middle, clusters projects, and the assistant at its cluster centre', () => {
  const nodes = [{ id: 'you', kind: 'you' }, { id: 'wa', kind: 'assistant', project: 'web' }, { id: 'a', kind: 'task', project: 'web' }, { id: 'b', kind: 'task', project: 'web' }, { id: 'o', kind: 'task', project: 'ops' }];
  const { pos, labels } = L.layoutActivity(nodes, { width: 1000, height: 600 });
  assert.deepEqual(pos.get('you'), { x: 500, y: 300 });
  const wa = pos.get('wa');
  for (const id of ['a', 'b']) { const p = pos.get(id); assert.ok(Math.hypot(p.x - wa.x, p.y - wa.y) > 40, `${id} sits around its assistant`); }
  assert.equal(labels.length, 2);
  assert.deepEqual(L.layoutActivity(nodes, { width: 1000, height: 600 }).pos.get('a'), pos.get('a'), 'deterministic');
  for (const [, p] of pos) assert.ok(p.x > 0 && p.x < 1000 && p.y > 0 && p.y < 600, 'inside the box');
  assert.match(L.curvePath({ x: 0, y: 0 }, { x: 100, y: 0 }), /^M0\.0,0\.0 Q50\.0,16\.0 100\.0,0\.0$/);
});

test('groupTasks orders stages and puts no-project last', () => {
  const list = [{ slug: 'a', status: 'done', project: 'x', priority: 'low' }, { slug: 'b', status: 'next', project: null, priority: 'high' }, { slug: 'c', status: 'next', project: 'a', priority: 'low' }];
  assert.deepEqual(L.groupTasks(list, 'stage').map(g => g.key), ['next', 'done']);
  assert.deepEqual(L.groupTasks(list, 'project').map(g => g.key), ['a', 'x', '']);
  assert.deepEqual(L.groupTasks(list, 'stage')[0].tasks.map(t => t.slug), ['b', 'c'], 'high priority first');
});

test('markdown blocks and inline tokens are parsed without HTML', () => {
  const b = L.markdownBlocks('<p class="stamp">Updated today</p>\n# Title\n\nSome **bold** and `code`.\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```\nx < y\n```');
  assert.deepEqual(b.map(x => x.tag), ['stamp', 'h1', 'p', 'ul', 'table', 'pre']);
  assert.deepEqual(L.inlineTokens('a **b** [c](https://x.y) <script>').map(t => t.t), ['text', 'b', 'text', 'a', 'text']);
  assert.equal(L.inlineTokens('[bad](javascript:alert(1))').length, 1, 'only http(s) and in-app links become links');
});

test('About and Help are dated, complete, and every in-app link resolves', () => {
  assert.match(UPDATED, /2026/);
  const ids = new Set([...ABOUT.map(a => a.id), ...HELP.map(a => a.id)]);
  assert.equal(ids.size, ABOUT.length + HELP.length, 'unique ids');
  for (const want of ['about', 'why', 'market', 'approach', 'story', 'vision', 'getting-started', 'troubleshooting', 'faq', 'shortcuts', 'background-runs']) assert.ok(ids.has(want), want);
  for (const page of [...ABOUT, ...HELP]) {
    assert.match(page.body, /<p class="stamp">(Updated|Researched|Written) /, `${page.id} carries a date`);
    for (const [, route] of page.body.matchAll(/\]\(#\/(?:help|about)\/([a-z-]+)\)/g)) assert.ok(ids.has(route), `${page.id} links to ${route}`);
  }
  // Views link to help articles by id; make sure those exist.
  const views = fs.readdirSync(new URL('../public/ui/js/views/', import.meta.url)).map(f => fs.readFileSync(new URL(`../public/ui/js/views/${f}`, import.meta.url), 'utf8')).join('\n')
    + fs.readFileSync(new URL('../public/ui/js/main.js', import.meta.url), 'utf8');
  for (const [, id] of views.matchAll(/#\/help\/([a-z-]+)|'\/help\/([a-z-]+)'/g)) if (id) assert.ok(ids.has(id), `link to help/${id}`);
});

test('the new app is served at / with recovery help embedded; /ui serves only its own files', async () => {
  h = await makeApp({ live: [] });
  const root = await h.get('/', { token: null });
  assert.equal(root.status, 200);
  assert.match(root.body, /\/ui\/js\/main\.js/);
  assert.match(root.body, /id="help-data">\{/);
  const css = await h.get('/ui/css/app.css', { token: null });
  assert.equal(css.status, 200);
  assert.equal((await h.get('/ui/fonts/inter.woff2', { token: null })).status, 200);
  for (const bad of ['/ui/../../package.json', '/ui/%2e%2e/%2e%2e/package.json', '/ui/js/nope.js', '/ui/fonts/inter-license.exe']) {
    assert.equal((await h.get(bad, { token: null })).status, 404, bad);
  }
  assert.equal((await h.request('POST', '/ui/css/app.css', {}, { token: null })).status, 405);
});
