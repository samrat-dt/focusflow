import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { openStore } from '../lib/store.js';
import { createApp } from '../lib/server.js';
import { state, tmpdir, at } from './helpers.js';

const TOKEN = 'c'.repeat(64);
let app, port;

async function setup({ tasks = [state()], mirror, sync } = {}) {
  const cfg = { token: TOKEN, device: 'd1', dataDir: tmpdir('ff-ss-data-'), focusRoot: tmpdir('ff-ss-focusflow-'), terminalApp: 'Terminal.app', focusBin: 'focusflow', tunables: { stale_days: 5, due_soon_days: 3 } };
  const reads = { n: 0 };
  let list = tasks;
  app = createApp({
    config: cfg, store: openStore(), mirror, sync,
    readTasks: () => { reads.n += 1; if (!list) throw new Error('focusflow down'); return { tasks: list, live: new Set() }; },
    focusRun: async () => ({ stdout: 'ok', stderr: '' }),
    readProjects: () => [], readWorkDir: () => '/w',
    now: () => at(2026, 9, 22, 12),
  });
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  port = app.server.address().port;
  return { reads, setTasks: t => { list = t; } };
}
afterEach(() => app?.server.close());

const health = () => new Promise((resolve, reject) => {
  http.get({ host: '127.0.0.1', port, path: '/api/health', headers: { host: `127.0.0.1:${port}`, 'x-focusflow-token': TOKEN } }, res => {
    let out = ''; res.on('data', c => (out += c)); res.on('end', () => resolve(JSON.parse(out)));
  }).on('error', reject);
});
const post = (p, body) => new Promise((resolve, reject) => {
  const data = JSON.stringify(body ?? {});
  const r = http.request({ host: '127.0.0.1', port, path: p, method: 'POST', headers: { host: `127.0.0.1:${port}`, 'x-focusflow-token': TOKEN, 'content-type': 'application/json', 'content-length': Buffer.byteLength(data) } }, res => {
    let out = ''; res.on('data', c => (out += c)); res.on('end', () => resolve({ status: res.statusCode, json: JSON.parse(out) }));
  });
  r.on('error', reject); r.end(data);
});

function fakeSync(extra = {}) {
  const s = { notes: 0, retries: 0, treeLocked: false, noteChange() { s.notes += 1; }, maybeRetry() { s.retries += 1; return Promise.resolve(); }, status: () => ({ status: 'ok', remote: 'origin', last_push_at: null, last_error: null, pending: false }), ...extra };
  return s;
}

test('health reports the sync status from the sync object, and mirror_error', async () => {
  await setup({ sync: fakeSync({ status: () => ({ status: 'error', remote: 'origin', last_push_at: null, last_error: 'offline', pending: false }) }) });
  app.refresh();
  const h = await health();
  assert.equal(h.sync.status, 'error');
  assert.equal(h.sync.last_error, 'offline');
  assert.equal(h.mirror_error, null);
});

test('a changed board or a mirrored file tells sync; an unchanged tick does not', async () => {
  const sync = fakeSync();
  let copied = [];
  await setup({ sync, mirror: () => ({ copied, unchanged: 0, errors: [] }) });
  app.refresh();
  assert.equal(sync.notes, 1, 'first reading writes events');
  app.refresh();
  assert.equal(sync.notes, 1, 'nothing changed');
  copied = ['tasks/a/brief.md'];
  app.refresh();
  assert.equal(sync.notes, 2, 'a mirrored file counts as a change');
});

test('sync is retried on every tick, changed or not', async () => {
  const sync = fakeSync();
  await setup({ sync });
  app.refresh(); app.refresh(); app.refresh();
  assert.equal(sync.retries, 3);
});

test('captures and opens tell sync too', async () => {
  const sync = fakeSync();
  await setup({ sync, tasks: [state({ slug: 'todo', name: 'Todo' })] });
  app.refresh();
  const before = sync.notes;
  await post('/api/open', { slug: 'todo' });
  assert.ok(sync.notes > before);
});

test('while a pull is rewriting files the tick is skipped without reading focusflow or recording an error', async () => {
  const sync = fakeSync({ treeLocked: true });
  const { reads } = await setup({ sync });
  const r = app.refresh();
  assert.equal(r.skipped, true);
  assert.equal(reads.n, 0);
  assert.equal((await health()).last_error, null);
  sync.treeLocked = false;
  app.refresh();
  assert.equal(reads.n, 1);
});

test('mirror runs each tick, even when focusflow could not be read; its errors surface in health', async () => {
  let calls = 0;
  const { setTasks } = await setup({ mirror: () => { calls += 1; return { copied: [], unchanged: 3, errors: ['tasks/x/brief.md: EACCES'] }; } });
  setTasks(null);
  app.refresh();
  assert.equal(calls, 1);
  const h = await health();
  assert.match(h.mirror_error, /EACCES/);
  assert.match(h.last_error, /focusflow down/);
});

test('a mirror that throws is reported, not fatal', async () => {
  await setup({ mirror: () => { throw new Error('disk full'); } });
  const r = app.refresh();
  assert.equal(r.skipped, undefined);
  assert.match((await health()).mirror_error, /disk full/);
});

test('the server exposes its serial queue so sync can share it', async () => {
  await setup();
  assert.equal(typeof app.serial, 'function');
  assert.equal(await app.serial(async () => 7), 7);
});
