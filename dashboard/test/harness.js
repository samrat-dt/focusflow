import http from 'node:http';
import { openStore } from '../lib/store.js';
import { createApp } from '../lib/server.js';
import { state, tmpdir, at } from './helpers.js';

export const TOKEN = 'c'.repeat(64);

let n = 0;
// A row as `focusflow inbox --all --as user --json` returns it.
export const focusRow = (slug, body, when = [10, 0], extra = {}) => ({
  id: (0xa0000000 + ++n).toString(16),
  created_at: at(2026, 9, 22, ...when).toISOString(),
  kind: 'message',
  from: slug ? { assignee: 'user', task_slug: slug } : { assignee: 'user' },
  to: { assignee: 'user' },
  body, urgent: false, status: 'delivered', mail: 'unread', reply_to: null,
  ...extra,
});

const DEFAULT_TASKS = () => [
  state({ slug: 'todo', name: 'Todo', status: 'backlog' }),
  state({ slug: 'going', name: 'Going', status: 'in-progress', has_session: true, harness: 'claude' }),
  state({ slug: 'running', name: 'Running', status: 'in-progress', has_session: true, harness: 'claude' }),
  state({ slug: 'finished', name: 'Finished', status: 'done', has_session: true }),
  state({ slug: 'shelved', name: 'Shelved', status: 'backlog', archived: true }),
  state({ slug: 'coding', name: 'Coding', status: 'in-progress', has_session: true, harness: 'codex' }),
  state({ slug: 'unknown-harness', name: 'Unknown', status: 'in-progress', has_session: true, harness: null }),
];

// The real app on an ephemeral port with a fake focusflow. `focusflow(args)` overrides the
// answer to any call that is not an inbox listing; the default answers
// `focusflow message` with a new id and everything else with empty output.
// `sessions`: map slug -> { sessionId, harness } | undefined, backs the `readSession` DI seam.
export async function makeApp({ tasks, live = ['running'], focusflow, workDir = '/work/x', notifier, tunables, sessions = {}, activityHome, projectsFull = [], owners = [], detachedCalls, pickFolder, runs = {}, bus = { messages: [], watches: [], nudges: [] } } = {}) {
  const calls = [];
  const inboxRows = { rows: [], fail: null };
  const list = tasks ?? DEFAULT_TASKS();
  const liveSet = new Set(live);
  const config = {
    token: TOKEN, device: 'd1', dataDir: tmpdir('ff-h-data-'), focusRoot: tmpdir('ff-h-focusflow-'), home: tmpdir('ff-h-home-'),
    cacheDb: tmpdir('ff-h-cache-') + '/store.db',
    terminalApp: 'Terminal.app', focusBin: 'focusflow', claudeBin: 'claude',
    // notifications stay off so no test can ever run the real osascript
    tunables: { stale_days: 5, due_soon_days: 3, inbox_poll_seconds: 5, notifications: false, approve_settle_ms: 0, working_window_seconds: 15, nudge_stall_seconds: 30, ...tunables },
  };
  const store = openStore();
  const app = createApp({
    config, store,
    readTasks: () => ({ tasks: list, live: liveSet }),
    focusRun: async (args, opts) => {
      calls.push({ args, opts });
      if (args[0] === 'inbox' && args[1] === '--all') {
        if (inboxRows.fail) throw new Error(inboxRows.fail);
        return { stdout: JSON.stringify(inboxRows.rows), stderr: '' };
      }
      if (focusflow) return focusflow(args, opts);
      if (args[0] === 'message') return { stdout: '[ab12cd34]\n', stderr: '' };
      return { stdout: '', stderr: '' };
    },
    readWorkDir: () => workDir,
    readSession: ({ slug }) => sessions[slug] ?? null,
    readRun: ({ slug }) => runs[slug] ?? null,
    readBus: () => bus,
    readProjects: () => [],
    readProjectsFull: () => projectsFull,
    readOwners: () => owners,
    ...(pickFolder ? { pickFolder } : { pickFolder: async () => { throw new Error('no dialog in tests'); } }),
    focusRunDetached: async (args, opts) => { (detachedCalls ?? []).push({ args, opts }); },
    ...(notifier ? { notifier } : {}),
    ...(activityHome !== undefined ? { activityHome } : {}),
    now: () => at(2026, 9, 22, 12),
  });
  app.refresh();
  await new Promise(r => app.server.listen(0, '127.0.0.1', r));
  const port = app.server.address().port;

  const request = (method, p, body, { token = TOKEN, type = 'application/json', raw, host } = {}) => new Promise((resolve, reject) => {
    const data = raw ?? (body === undefined ? '' : JSON.stringify(body));
    const h = { host: host ?? `127.0.0.1:${port}`, 'content-length': Buffer.byteLength(data) };
    if (token) h['x-focusflow-token'] = token;
    if (type && method !== 'GET') h['content-type'] = type;
    const r = http.request({ host: '127.0.0.1', port, path: p, method, headers: h }, res => {
      let out = '';
      res.on('data', c => (out += c));
      res.on('end', () => resolve({ status: res.statusCode, body: out, json: () => JSON.parse(out) }));
    });
    r.on('error', reject);
    r.end(data);
  });

  return {
    app, store, calls, config, tasks: list, live: liveSet, inboxRows, port,
    get: (p, o) => request('GET', p, undefined, o),
    post: (p, b, o) => request('POST', p, b, o),
    request,
    close: () => app.server.close(),
  };
}
