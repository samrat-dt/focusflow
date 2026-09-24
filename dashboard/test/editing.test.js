import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { taskEditArgs, projectEditArgs, writeBrief, writeNote, validateSettings, EditError } from '../lib/editing.js';
import { makeApp } from './harness.js';
import { state, tmpdir } from './helpers.js';

let h;
afterEach(() => { h?.close(); h = null; });

test('taskEditArgs only sends what changed, and diffs tags', () => {
  const cur = { name: 'Old', priority: 'low', due_date: '2026-10-01', waiting_on: 'legal', tags: ['ui', 'web'] };
  assert.deepEqual(taskEditArgs('t', { name: 'Old', priority: 'low' }, cur), []);
  assert.deepEqual(taskEditArgs('t', { name: 'New name', priority: 'high', due: null, waiting: '', tags: ['web', '#API'] }, cur),
    ['update', 'task', 't', '--name', 'New name', '--priority', 'high', '--clear-due', '--clear-waiting', '--tag', 'api', '--remove-tag', 'ui']);
  assert.deepEqual(taskEditArgs('t', { due: 'tomorrow', waiting: 'Sam' }, {}), ['update', 'task', 't', '--due-date', 'tomorrow', '--waiting', 'Sam']);
  for (const bad of [{ name: '-rf' }, { priority: 'urgent' }, { due: 'next blue moon' }, { waiting: '--x' }, { tags: ['bad tag'] }, { tags: 'x' }, { name: 'a\nb' }]) {
    assert.throws(() => taskEditArgs('t', bad, cur), EditError, JSON.stringify(bad));
  }
  assert.deepEqual(projectEditArgs('p', { name: 'Site', priority: 'high' }, { name: 'Web', priority: 'low' }), ['update', 'project', 'p', '--name', 'Site', '--priority', 'high']);
});

test('briefs are replaced atomically; notes never overwrite each other', () => {
  const root = tmpdir('ff-edit-');
  fs.mkdirSync(path.join(root, 'tasks', 'a'), { recursive: true });
  writeBrief(root, 'tasks', 'a', '# A\n\nDo it');
  assert.equal(fs.readFileSync(path.join(root, 'tasks', 'a', 'brief.md'), 'utf8'), '# A\n\nDo it\n');
  assert.throws(() => writeBrief(root, 'tasks', 'missing', 'x'), EditError);
  assert.throws(() => writeBrief(root, 'tasks', 'a', 'x'.repeat(60000)), EditError);
  const when = new Date(2026, 8, 25, 9, 5, 7);
  assert.equal(writeNote(root, 'a', 'first', when), '2026-09-25-note-090507.md');
  assert.equal(writeNote(root, 'a', 'second', when), '2026-09-25-note-090507-2.md');
  assert.throws(() => writeNote(root, 'a', '   ', when), EditError);
});

test('validateSettings accepts only known settings with sane values', () => {
  assert.deepEqual(validateSettings({ quiet_hours: '23:00-07:00', stall_minutes: 20, notifications: false, terminal_app: 'iTerm2' }),
    { quiet_hours: '23:00-07:00', stall_minutes: 20, notifications: false, terminal_app: 'iTerm2' });
  assert.deepEqual(validateSettings({ quiet_hours: '' }), { quiet_hours: null });
  for (const bad of [{ port: 1 }, { stall_minutes: 1 }, { notifications: 'yes' }, { terminal_app: 'Warp' }, { quiet_hours: 'late' }]) {
    assert.throws(() => validateSettings(bad), EditError, JSON.stringify(bad));
  }
});

test('edit, brief, note and settings routes', async () => {
  h = await makeApp({ live: [], tasks: [state({ slug: 'a', name: 'A', status: 'next', priority: 'low', tags: [] })], projectsFull: [{ slug: 'web', name: 'Web', priority: 'low' }] });
  fs.mkdirSync(path.join(h.config.focusRoot, 'tasks', 'a'), { recursive: true });
  fs.mkdirSync(path.join(h.config.focusRoot, 'projects', 'web'), { recursive: true });
  fs.writeFileSync(path.join(h.config.home, 'instance.json'), JSON.stringify({ name: 'x', tunables: { port: 1 } }));

  assert.equal((await h.post('/api/tasks/a/edit', { priority: 'high', tags: ['ui'] })).status, 200);
  assert.deepEqual(h.calls.at(-1).args, ['update', 'task', 'a', '--priority', 'high', '--tag', 'ui']);
  assert.deepEqual((await h.post('/api/tasks/a/edit', { priority: 'low' })).json(), { ok: true, changed: false });
  assert.equal((await h.post('/api/tasks/a/edit', { name: '-x' })).status, 400);
  assert.equal((await h.post('/api/tasks/zzz/edit', { name: 'x' })).status, 404);
  assert.equal((await h.post('/api/projects/web/edit', { name: 'Website' })).status, 200);
  assert.deepEqual(h.calls.at(-1).args, ['update', 'project', 'web', '--name', 'Website']);

  assert.equal((await h.post('/api/tasks/a/brief', { text: '# A' })).status, 200);
  assert.equal((await h.get('/api/tasks/a/brief')).json().brief, '# A\n');
  assert.equal((await h.post('/api/projects/web/brief', { text: 'Why' })).status, 200);
  assert.equal((await h.post('/api/tasks/a/note', { text: 'Half done' })).status, 200);
  assert.equal((await h.post('/api/projects/web/note', { text: 'x' })).status, 404);

  const s = (await h.get('/api/settings')).json();
  assert.equal(s.settings.quiet_hours, '22:00-08:00');
  assert.equal((await h.post('/api/settings', { quiet_hours: '23:00-06:00', stall_minutes: 30 })).status, 200);
  assert.equal((await h.get('/api/settings')).json().settings.stall_minutes, 30);
  const saved = JSON.parse(fs.readFileSync(path.join(h.config.home, 'instance.json'), 'utf8'));
  assert.deepEqual(saved.tunables, { port: 1, quiet_hours: '23:00-06:00', stall_minutes: 30 });
  assert.equal((await h.post('/api/settings', { port: 80 })).status, 400);
});
