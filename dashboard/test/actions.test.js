import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { openStore } from '../lib/store.js';
import { createApp } from '../lib/server.js';
import { thinBrief } from '../lib/capture.js';
import { readEvents } from '../lib/event-log.js';
import { state, tmpdir, at } from './helpers.js';

const TOKEN = 'b'.repeat(64);
const STUB = name => `# ${name}\n\nEdit this brief freely via \`focusflow edit\` or by composing a focusflow skill\nsession. Sections to cover: What / Why / Where / Done when / Out of scope /\nOpen questions.\n`;

let ctx;

async function setup({ tasks: initial, live = [], runFocus, projects = [{ slug: 'proj', name: 'Proj' }], briefFor } = {}) {
  const focusRoot = tmpdir('ff-act-focusflow-');
  const calls = [];
  const tasks = initial ?? [
    state({ slug: 'todo', name: 'Todo', status: 'backlog' }),
    state({ slug: 'going', name: 'Going', status: 'in-progress', has_session: true }),
    state({ slug: 'running', name: 'Running', status: 'in-progress', has_session: true }),
    state({ slug: 'finished', name: 'Finished', status: 'done', has_session: true }),
    state({ slug: 'shelved', name: 'Shelved', status: 'backlog', archived: true }),
  ];
  const fakeFocus = runFocus ?? (async args => {
    if (args[0] === 'add' && args[1] === 'project') {
      projects.push({ slug: args[args.indexOf('--slug') + 1], name: args[2] });
      return { stdout: `Created project "${args[2]}"\n`, stderr: '' };
    }
    if (args[0] === 'add') {
      const name = args[2];
      const slug = args[args.indexOf('--slug') + 1];
      const dir = path.join(focusRoot, 'tasks', slug);
      fs.mkdirSync(path.join(dir, 'updates'), { recursive: true });
      fs.writeFileSync(path.join(dir, 'brief.md'), briefFor ? briefFor(name) : STUB(name));
      tasks.push(state({ slug, name, priority: args[args.indexOf('--priority') + 1], project: args.includes('--project') ? args[args.indexOf('--project') + 1] : null }));
      return { stdout: `Created floating task "${slug}"\n`, stderr: '' };
    }
    return { stdout: `opened ${args[1]}\n`, stderr: '' };
  });
  const config = { token: TOKEN, device: 'd1', dataDir: tmpdir('ff-act-data-'), focusRoot, terminalApp: 'Terminal.app', focusBin: 'focusflow', tunables: { stale_days: 5, due_soon_days: 3 } };
  const store = openStore();
  const app = createApp({
    config, store,
    readTasks: () => ({ tasks, live: new Set(live) }),
    focusRun: async (args, opts) => { calls.push({ args, opts }); return fakeFocus(args, opts); },
    readProjects: () => projects,
    readWorkDir: ({ slug }) => path.join(focusRoot, 'tasks', slug, 'workspace'),
    now: () => at(2026, 9, 22, 12),
  });
  app.refresh();
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  ctx = { app, store, calls, config, tasks, focusRoot, port: app.server.address().port };
  return ctx;
}

beforeEach(() => { ctx = null; });
afterEach(() => { ctx?.app.server.close(); });

function post(p, body, { type = 'application/json', raw, token = TOKEN, method = 'POST' } = {}) {
  return new Promise((resolve, reject) => {
    const data = raw ?? (body === undefined ? '' : JSON.stringify(body));
    const h = { host: `127.0.0.1:${ctx.port}`, 'x-focusflow-token': token, 'content-length': Buffer.byteLength(data) };
    if (type) h['content-type'] = type;
    const r = http.request({ host: '127.0.0.1', port: ctx.port, path: p, method, headers: h }, res => {
      let out = '';
      res.on('data', c => (out += c));
      res.on('end', () => resolve({ status: res.statusCode, body: out, json: () => JSON.parse(out) }));
    });
    r.on('error', reject);
    r.end(data);
  });
}
const get = p => new Promise((resolve, reject) => {
  http.get({ host: '127.0.0.1', port: ctx.port, path: p, headers: { host: `127.0.0.1:${ctx.port}`, 'x-focusflow-token': TOKEN } }, res => {
    let out = ''; res.on('data', c => (out += c)); res.on('end', () => resolve({ status: res.statusCode, json: () => JSON.parse(out) }));
  }).on('error', reject);
});

// ---------------- open ----------------

test('open runs `focusflow do <slug>` with FOCUSFLOW_TERM from config and returns focusflow\'s output', async () => {
  await setup();
  const r = await post('/api/open', { slug: 'todo' });
  assert.equal(r.status, 200);
  assert.deepEqual(r.json(), { ok: true, action: 'start', output: 'opened todo' });
  assert.deepEqual(ctx.calls.map(c => c.args), [['do', 'todo']]);
  assert.equal(ctx.calls[0].opts.env.FOCUSFLOW_TERM, 'Terminal.app');
  assert.equal(ctx.calls[0].opts.env.CLAUDE_CODE_SESSION_ID, undefined);
});

test('the action is Start for backlog, Resume for in-progress, Focus for live', async () => {
  await setup({ live: ['running'] });
  assert.equal((await post('/api/open', { slug: 'todo' })).json().action, 'start');
  assert.equal((await post('/api/open', { slug: 'going' })).json().action, 'resume');
  assert.equal((await post('/api/open', { slug: 'running' })).json().action, 'focus');
});

test('open records open.requested with the outcome', async () => {
  await setup();
  await post('/api/open', { slug: 'todo' });
  const opens = ctx.store.dump().opens;
  assert.equal(opens.length, 1);
  assert.deepEqual(opens[0].payload, { slug: 'todo', action: 'start', ok: true });
});

test('a focusflow failure is relayed unchanged (502) and still recorded', async () => {
  const msg = 'Terminal needs Accessibility.\nOpen System Settings > Privacy';
  await setup({ runFocus: async () => { throw new Error(msg); } });
  const r = await post('/api/open', { slug: 'todo' });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, msg);
  assert.deepEqual(ctx.store.dump().opens[0].payload, { slug: 'todo', action: 'start', ok: false, error: msg });
});

test('a done task is reopened: status back to in-progress and NO terminal is opened', async () => {
  await setup();
  const r = await post('/api/open', { slug: 'finished' });
  assert.equal(r.status, 200);
  assert.equal(r.json().ok, true);
  assert.equal(r.json().action, 'reopen');
  assert.match(r.json().output, /No terminal was opened/);
  assert.deepEqual(ctx.calls.map(c => c.args), [['update', 'task', 'finished', '--status', 'in-progress']]);
  assert.deepEqual(ctx.store.dump().opens[0].payload, { slug: 'finished', action: 'reopen', ok: true });
});

test('reopen: if the status change fails, focusflow do is not run and the error is relayed', async () => {
  const msg = 'error: something went wrong';
  await setup({ runFocus: async args => { if (args[0] === 'update') throw new Error(msg); return { stdout: '', stderr: '' }; } });
  const r = await post('/api/open', { slug: 'finished' });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, msg);
  assert.deepEqual(ctx.calls.map(c => c.args[0]), ['update']);
  assert.deepEqual(ctx.store.dump().opens[0].payload, { slug: 'finished', action: 'reopen', ok: false, error: msg });
});

test('open validates: slug shape, existence, and state', async () => {
  await setup();
  assert.equal((await post('/api/open', { slug: '../x' })).status, 400);
  assert.equal((await post('/api/open', { slug: 'Todo' })).status, 400);
  assert.equal((await post('/api/open', { slug: 5 })).status, 400);
  assert.equal((await post('/api/open', {})).status, 400);
  assert.equal((await post('/api/open', { slug: 'nope' })).status, 404);
  assert.equal((await post('/api/open', { slug: 'shelved' })).status, 409);
  assert.equal(ctx.calls.length, 0, 'focusflow is never called for a rejected request');
});

test('POST bodies must be JSON', async () => {
  await setup();
  assert.equal((await post('/api/open', undefined, { raw: 'slug=todo', type: 'application/x-www-form-urlencoded' })).status, 415);
  assert.equal((await post('/api/open', undefined, { raw: '{"slug":"todo"}', type: 'text/plain' })).status, 415);
  assert.equal((await post('/api/open', undefined, { raw: '{"slug":"todo"}', type: null })).status, 415);
  assert.equal((await post('/api/open', undefined, { raw: '{not json' })).status, 400);
  assert.equal((await post('/api/open', undefined, { raw: '[1,2]' })).status, 400);
  assert.equal((await post('/api/open', undefined, { raw: 'x'.repeat(70 * 1024) })).status, 413);
  assert.equal(ctx.calls.length, 0);
});

test('open and capture need the token and the right method', async () => {
  await setup();
  assert.equal((await post('/api/open', { slug: 'todo' }, { token: 'wrong' })).status, 401);
  assert.equal((await post('/api/capture', { name: 'x' }, { token: 'wrong' })).status, 401);
  assert.equal((await post('/api/open', { slug: 'todo' }, { method: 'PUT' })).status, 405);
  assert.equal((await get('/api/open')).status, 405);
  assert.equal((await get('/api/capture')).status, 405);
  assert.equal(ctx.calls.length, 0);
});

// ---------------- projects ----------------

test('GET /api/projects lists projects for the capture dialog', async () => {
  await setup();
  const j = (await get('/api/projects')).json();
  assert.deepEqual(j.projects, [{ slug: 'proj', name: 'Proj' }]);
  // a new project defaults to the focusflow repo, which is where the server runs
  assert.equal(j.default_work_dir, path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..'));
});

// ---------------- capture ----------------

test('capture creates the task through focusflow with a computed slug and writes the thin brief', async () => {
  await setup();
  const r = await post('/api/capture', { name: '  Write the Q3 report  ', project: 'proj', priority: 'high', note: 'Numbers for finance' });
  assert.equal(r.status, 200);
  const j = r.json();
  assert.equal(j.slug, 'write-the-q3-report');
  assert.equal(j.brief_written, true);
  assert.equal(j.opened, false);
  assert.deepEqual(ctx.calls.map(c => c.args), [['add', 'task', 'Write the Q3 report', '--slug', 'write-the-q3-report', '--project', 'proj', '--priority', 'high']]);
  const brief = fs.readFileSync(path.join(ctx.focusRoot, 'tasks', 'write-the-q3-report', 'brief.md'), 'utf8');
  assert.equal(brief, thinBrief({ name: 'Write the Q3 report', note: 'Numbers for finance', workDir: path.join(ctx.focusRoot, 'tasks', 'write-the-q3-report', 'workspace') }));
  assert.match(brief, /\*Deferred — fill in at task start\.\*/);
});

test('capture defaults priority to medium, omits --project, and shows the task on the board', async () => {
  await setup();
  await post('/api/capture', { name: 'Plain task' });
  assert.deepEqual(ctx.calls[0].args, ['add', 'task', 'Plain task', '--slug', 'plain-task', '--priority', 'medium']);
  const board = (await get('/api/tasks')).json().tasks;
  assert.ok(board.some(t => t.slug === 'plain-task'));
});

test('capture records capture.created and the new task\'s first appearance', async () => {
  await setup();
  await post('/api/capture', { name: 'Plain task', note: 'secret note text' });
  const d = ctx.store.dump();
  assert.equal(d.captures.length, 1);
  assert.deepEqual(d.captures[0].payload, { slug: 'plain-task', name: 'Plain task', project: null, priority: 'medium', has_note: true, brief_written: true });
  assert.ok(!JSON.stringify(d.captures).includes('secret note text'));
  assert.ok(ctx.store.getTask('plain-task'));
});

test('names that focusflow would take for flags are rejected before focusflow is ever called', async () => {
  await setup();
  for (const name of ['--help', '-h', '  --slug=x', '-']) {
    const r = await post('/api/capture', { name });
    assert.equal(r.status, 400, name);
    assert.match(r.json().error, /start with/);
  }
  assert.equal(ctx.calls.length, 0);
});

test('capture validates name, priority, project and note', async () => {
  await setup();
  assert.equal((await post('/api/capture', {})).status, 400);
  assert.equal((await post('/api/capture', { name: '' })).status, 400);
  assert.equal((await post('/api/capture', { name: 'x'.repeat(201) })).status, 400);
  assert.equal((await post('/api/capture', { name: 'a\nb' })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', priority: 'urgent' })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', project: 'nope' })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', project: '--x' })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', project: 7 })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', note: 'n'.repeat(2001) })).status, 400);
  assert.equal((await post('/api/capture', { name: 'ok', note: 5 })).status, 400);
  assert.equal(ctx.calls.length, 0);
});

test('slug collisions (archived tasks included) get -2, -3', async () => {
  await setup({ tasks: [state({ slug: 'plain-task', archived: true }), state({ slug: 'plain-task-2' })] });
  const j = (await post('/api/capture', { name: 'Plain task' })).json();
  assert.equal(j.slug, 'plain-task-3');
});

test('two simultaneous captures of the same name never share a slug', async () => {
  await setup();
  const [a, b] = await Promise.all([post('/api/capture', { name: 'Same name' }), post('/api/capture', { name: 'Same name' })]);
  assert.deepEqual([a.json().slug, b.json().slug].sort(), ['same-name', 'same-name-2']);
});

test('a focusflow failure during capture is relayed, and nothing is recorded or written', async () => {
  await setup({ runFocus: async () => { throw new Error('error: project "proj" not found'); } });
  const r = await post('/api/capture', { name: 'Plain task', project: 'proj' });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, 'error: project "proj" not found');
  assert.equal(ctx.store.dump().captures.length, 0);
  assert.equal(fs.existsSync(path.join(ctx.focusRoot, 'tasks', 'plain-task')), false);
});

test('a brief that already has real content is not overwritten', async () => {
  await setup({ briefFor: name => `# ${name}\n\n## What\nSomeone already wrote this.\n` });
  const j = (await post('/api/capture', { name: 'Plain task', note: 'mine' })).json();
  assert.equal(j.brief_written, false);
  assert.match(j.warning, /left as is/);
  assert.match(fs.readFileSync(path.join(ctx.focusRoot, 'tasks', 'plain-task', 'brief.md'), 'utf8'), /Someone already wrote this/);
  assert.equal(ctx.store.dump().captures[0].payload.brief_written, false);
});

test('if the brief cannot be written the task still exists and the response says so', async () => {
  await setup({ runFocus: async args => { ctx.tasks.push(state({ slug: 'plain-task', name: 'Plain task' })); return { stdout: 'ok', stderr: '' }; } });
  const r = await post('/api/capture', { name: 'Plain task' });
  assert.equal(r.status, 200);
  assert.equal(r.json().brief_written, false);
  assert.match(r.json().warning, /brief/);
  assert.equal(ctx.store.dump().captures.length, 1);
});

test('Save & open captures, then opens the new task', async () => {
  await setup();
  const j = (await post('/api/capture', { name: 'Plain task', open: true })).json();
  assert.equal(j.opened, true);
  assert.deepEqual(ctx.calls.map(c => c.args[0]), ['add', 'do']);
  assert.deepEqual(ctx.calls[1].args, ['do', 'plain-task']);
  assert.equal(ctx.store.dump().opens[0].payload.action, 'start');
});

test('if opening fails after a successful capture, the capture stands and the open error is returned', async () => {
  await setup({
    runFocus: async args => {
      if (args[0] === 'do') throw new Error('focusflow: Accessibility needed');
      const slug = args[args.indexOf('--slug') + 1];
      fs.mkdirSync(path.join(ctx.focusRoot, 'tasks', slug), { recursive: true });
      fs.writeFileSync(path.join(ctx.focusRoot, 'tasks', slug, 'brief.md'), STUB(args[2]));
      ctx.tasks.push(state({ slug, name: args[2] }));
      return { stdout: 'ok', stderr: '' };
    },
  });
  const r = await post('/api/capture', { name: 'Plain task', open: true });
  assert.equal(r.status, 200);
  const j = r.json();
  assert.equal(j.slug, 'plain-task');
  assert.equal(j.opened, false);
  assert.equal(j.open_error, 'focusflow: Accessibility needed');
});

// ---------------- capture with a new project ----------------

const newProject = (over = {}) => ({ name: 'Billing rewrite', work_dir: ctx.focusRoot, priority: 'high', ...over });

test('a new project is created first, then the task goes under it', async () => {
  await setup();
  const r = await post('/api/capture', { name: 'Plan the rewrite', new_project: newProject() });
  assert.equal(r.status, 200);
  assert.equal(r.json().slug, 'plan-the-rewrite');
  assert.equal(r.json().project, 'billing-rewrite');
  assert.deepEqual(ctx.calls.map(c => c.args), [
    ['add', 'project', 'Billing rewrite', '--slug', 'billing-rewrite', '--work-dir', ctx.focusRoot, '--priority', 'high'],
    ['add', 'task', 'Plan the rewrite', '--slug', 'plan-the-rewrite', '--project', 'billing-rewrite', '--priority', 'medium'],
  ]);
});

test('a new project defaults to medium priority and its slug never collides with a listed project', async () => {
  await setup();
  await post('/api/capture', { name: 'Anything', new_project: { name: 'Proj', work_dir: ctx.focusRoot } });
  assert.deepEqual(ctx.calls[0].args, ['add', 'project', 'Proj', '--slug', 'proj-2', '--work-dir', ctx.focusRoot, '--priority', 'medium']);
  assert.equal(ctx.calls[1].args[ctx.calls[1].args.indexOf('--project') + 1], 'proj-2');
});

test('the new project is recorded as project.created before the capture', async () => {
  await setup();
  await post('/api/capture', { name: 'Plan the rewrite', new_project: newProject() });
  const kinds = readEvents(ctx.config.dataDir).events.filter(e => e.kind === 'project.created' || e.kind === 'capture.created');
  assert.deepEqual(kinds.map(e => e.kind), ['project.created', 'capture.created']);
  assert.equal(kinds[0].subject, 'project:billing-rewrite');
  assert.deepEqual(kinds[0].payload, { slug: 'billing-rewrite', name: 'Billing rewrite', priority: 'high', work_dir: ctx.focusRoot });
  assert.equal(kinds[1].payload.project, 'billing-rewrite');
});

test('the new project shows up in the project list afterwards', async () => {
  await setup();
  await post('/api/capture', { name: 'Plan the rewrite', new_project: newProject() });
  assert.ok((await get('/api/projects')).json().projects.some(p => p.slug === 'billing-rewrite'));
});

test('new_project is validated before focusflow is ever called', async () => {
  await setup();
  const file = path.join(ctx.focusRoot, 'a-file');
  fs.writeFileSync(file, 'x');
  const bad = [
    ['a string', 'Billing'],
    ['an array', []],
    ['no name', newProject({ name: undefined })],
    ['a flag-like name', newProject({ name: '--help' })],
    ['a multi-line name', newProject({ name: 'a\nb' })],
    ['a bad priority', newProject({ priority: 'urgent' })],
    ['no work_dir', newProject({ work_dir: undefined })],
    ['a relative work_dir', newProject({ work_dir: 'relative/path' })],
    ['a work_dir with a line break', newProject({ work_dir: `${ctx.focusRoot}\n/x` })],
    ['a work_dir that does not exist', newProject({ work_dir: path.join(ctx.focusRoot, 'missing') })],
    ['a work_dir that is a file', newProject({ work_dir: file })],
  ];
  for (const [what, np] of bad) {
    const r = await post('/api/capture', { name: 'ok', new_project: np });
    assert.equal(r.status, 400, what);
  }
  assert.equal(ctx.calls.length, 0);
});

test('project and new_project together are refused', async () => {
  await setup();
  const r = await post('/api/capture', { name: 'ok', project: 'proj', new_project: newProject() });
  assert.equal(r.status, 400);
  assert.match(r.json().error, /either/);
  assert.equal(ctx.calls.length, 0);
});

test('if focusflow cannot create the project, no task is created and nothing is recorded', async () => {
  await setup({ runFocus: async () => { throw new Error('error: work_dir does not exist'); } });
  const r = await post('/api/capture', { name: 'Plan the rewrite', new_project: newProject() });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, 'error: work_dir does not exist');
  assert.equal(ctx.calls.length, 1);
  assert.equal(readEvents(ctx.config.dataDir).events.filter(e => e.kind === 'project.created' || e.kind === 'capture.created').length, 0);
});

test('if the task fails after the project was created, the error says so and names the project', async () => {
  await setup({
    runFocus: async args => {
      if (args[1] === 'task') throw new Error('error: boom');
      return { stdout: 'ok', stderr: '' };
    },
  });
  const r = await post('/api/capture', { name: 'Plan the rewrite', new_project: newProject() });
  assert.equal(r.status, 502);
  assert.match(r.json().error, /project "billing-rewrite" was created/);
  assert.match(r.json().error, /error: boom/);
  assert.equal(r.json().project, 'billing-rewrite');
  const kinds = readEvents(ctx.config.dataDir).events.map(e => e.kind);
  assert.ok(kinds.includes('project.created'));
  assert.ok(!kinds.includes('capture.created'));
});
