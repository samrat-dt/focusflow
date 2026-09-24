import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { tick } from './snapshotter.js';
import { signalsFor } from './signals.js';
import { makeEvent, appendEvent } from './event-log.js';
import { runFocus } from './focus-run.js';
import { focusEnv } from './focus-env.js';
import { readProjects as readFocusProjects, readProjectsFull as readFocusProjectsFull, readOwners as readFocusOwners, readWorkDir as readFocusWorkDir, readSession as readFocusSession } from './focus-reader.js';
import { spawn } from 'node:child_process';
import { buildOverview, buildArchive } from './hierarchy.js';
import { runPickFolder } from './pick-folder.js';
import { runResume } from './resume-run.js';
import { isWorking } from './activity.js';
import { validateName, slugify, uniqueSlug, thinBrief, isStubBrief, CaptureError, MAX_NOTE } from './capture.js';
import { SLUG } from './slug.js';
import { createPoller, buildInbox, buildThread } from './inbox.js';
import { checkText, queueBody, headlessBody, parseFocusId } from './send.js';
import { createNotifier } from './notifier.js';
import { helpContent, appFragments } from './help.js';

const PUBLIC_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'public');
const STATIC = {
  '/': ['index.html', 'text/html; charset=utf-8'],
  '/app.js': ['app.js', 'text/javascript; charset=utf-8'],
  '/style.css': ['style.css', 'text/css; charset=utf-8'],
  '/help.js': ['help.js', 'text/javascript; charset=utf-8'],
  '/help.css': ['help.css', 'text/css; charset=utf-8'],
};
const PRIORITIES = ['high', 'medium', 'low'];
const MAX_BODY = 64 * 1024;
const FOCUSFLOW_DO_TIMEOUT_MS = 30000;
// A project made from the capture dialog defaults to this repo (the server runs from it).
const DEFAULT_PROJECT_DIR = path.resolve(PUBLIC_DIR, '..');

class HttpError extends Error {
  // `data` adds fields to the JSON error body (e.g. the project a failed capture had already created).
  constructor(status, message, data) { super(message); this.status = status; this.data = data; }
}
const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

function send(res, status, body, type, extra = {}) {
  res.writeHead(status, {
    'content-type': type,
    'cache-control': 'no-store',
    'x-content-type-options': 'nosniff',
    'referrer-policy': 'no-referrer',
    'content-security-policy': CSP,
    ...extra,
  });
  res.end(body);
}
const json = (res, status, obj, extra) => send(res, status, JSON.stringify(obj), 'application/json; charset=utf-8', extra);
const fail = (res, status, message, extra) => json(res, status, { error: message }, extra);

function tokenOk(given, expected) {
  const a = Buffer.from(String(given ?? ''));
  const b = Buffer.from(expected);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

function readText(file) {
  try { return fs.readFileSync(file, 'utf8'); } catch { return null; }
}

// JSON only (a cross-site form or text/plain POST cannot be JSON), size-limited.
function readJson(req) {
  return new Promise((resolve, reject) => {
    if (!/^application\/json\s*(;|$)/i.test(req.headers['content-type'] ?? '')) {
      req.resume();
      return reject(new HttpError(415, 'content-type must be application/json'));
    }
    const chunks = [];
    let size = 0;
    req.on('data', c => {
      size += c.length;
      if (size <= MAX_BODY) chunks.push(c);
    });
    req.on('error', reject);
    req.on('end', () => {
      if (size > MAX_BODY) return reject(new HttpError(413, 'body too large'));
      let body;
      try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { return reject(new HttpError(400, 'body is not valid JSON')); }
      if (body === null || typeof body !== 'object' || Array.isArray(body)) return reject(new HttpError(400, 'body must be a JSON object'));
      resolve(body);
    });
  });
}

export function createApp({
  config, store, readTasks, malformed = 0, now = () => new Date(), notifier: injectedNotifier = null,
  focusRun = (args, { env, timeoutMs }) => runFocus(args, { focusBin: config.focusBin, env, timeoutMs }),
  pickFolder = opts => runPickFolder(opts),
  readProjects = () => readFocusProjects({ focusRoot: config.focusRoot, dbName: config.focusDbName }),
  readProjectsFull = () => readFocusProjectsFull({ focusRoot: config.focusRoot, dbName: config.focusDbName }),
  readOwners = () => readFocusOwners({ focusRoot: config.focusRoot, dbName: config.focusDbName }),
  // Fire-and-forget (used for `done`, whose KB sweep can take minutes; the status flips first).
  focusRunDetached = (args, { env }) => { const c = spawn(config.focusBin, args, { env, detached: true, stdio: 'ignore' }); c.on('error', () => {}); c.unref(); return Promise.resolve(); },
  readWorkDir = ({ slug }) => readFocusWorkDir({ focusRoot: config.focusRoot, dbName: config.focusDbName, slug }),
  readSession = ({ slug }) => readFocusSession({ focusRoot: config.focusRoot, dbName: config.focusDbName, slug }),
  resumeRun = opts => runResume(opts),
  activityHome = undefined,
  mirror = null,
  sync = null,
}) {
  const notifier = injectedNotifier ?? createNotifier({ enabled: config.tunables.notifications !== false });
  const status = { lastTickAt: null, lastError: null, mirrorError: null, live: new Set(), working: new Set(), malformed };

  // Idle vs working, for the (small) currently-live set only, claude harness only (the transcript
  // layout for other harnesses is unverified — see the design spec). Any read failure (missing
  // focusflow.db, locked, no session row) just leaves that slug out of the set: never wrong, sometimes
  // just coarser.
  function computeWorking(liveSet) {
    const working = new Set();
    for (const slug of liveSet) {
      try {
        const t = store.getTask(slug);
        if (t?.harness !== 'claude') continue;
        const session = readSession({ slug });
        if (!session) continue;
        const workDir = readWorkDir({ slug });
        if (!workDir) continue;
        if (isWorking({ workDir, sessionId: session.sessionId, now: now(), windowMs: config.tunables.working_window_seconds * 1000, home: activityHome })) {
          working.add(slug);
        }
      } catch { /* leave this slug idle */ }
    }
    return working;
  }

  // Read focusflow, record what changed, mirror focusflow's text files, and tell sync.
  function refresh() {
    // A pull may be rewriting files under data/: wait for the next tick.
    if (sync?.treeLocked) return { events: [], skipped: true, error: null };
    let pendingLive = null;
    let result;
    try {
      result = tick({
        store, dataDir: config.dataDir, device: config.device, now: now(),
        readTasks: () => { const r = readTasks(); pendingLive = r.live; return r; },
      });
    } catch (err) {
      result = { events: [], skipped: true, error: err.message };
    }
    if (result.skipped) {
      status.lastError = result.error;
    } else {
      status.lastError = null;
      status.live = pendingLive ?? new Set();
      status.working = computeWorking(status.live);
      status.lastTickAt = now();
    }
    let mirrored = 0;
    if (mirror) {
      try {
        const m = mirror();
        mirrored = m.copied.length;
        status.mirrorError = m.errors.length ? m.errors.join('; ') : null;
      } catch (err) {
        status.mirrorError = err.message;
      }
    }
    if (result.events.length || mirrored) sync?.noteChange();
    sync?.maybeRetry();
    return result;
  }

  // "Open in a terminal" and "status" are two independent facts: a live session that is not a
  // headless run is a terminal someone can look at, whatever stage the task is in (paused, review...).
  const view = t => ({
    ...t,
    live: status.live.has(t.slug),
    working: status.working.has(t.slug),
    terminal_open: status.live.has(t.slug) && t.auto_run_status !== 'running',
    headless_running: t.auto_run_status === 'running',
    signals: signalsFor(t, { live: status.live.has(t.slug), working: status.working.has(t.slug), now: now(), tunables: config.tunables }),
  });

  function detail(slug) {
    const task = store.getTask(slug);
    if (!task) return null;
    const dir = path.join(config.focusRoot, 'tasks', slug);
    let files = [];
    try { files = fs.readdirSync(path.join(dir, 'updates')).filter(f => f.endsWith('.md')).sort().reverse(); } catch { /* no notes */ }
    const v = view(task);
    return {
      task: v,
      brief: readText(path.join(dir, 'brief.md')),
      notes: files.map(f => ({
        file: f,
        date: (/^\d{4}-\d{2}-\d{2}/.exec(f) ?? [null])[0],
        text: readText(path.join(dir, 'updates', f)) ?? '',
      })),
      session: {
        harness: task.harness, has_session: task.has_session, started: task.session_started,
        last_resumed: task.session_last_resumed, live: v.live, auto_run_status: task.auto_run_status,
      },
    };
  }

  function health() {
    const dates = store.snapshotDates(config.device);
    return {
      device: config.device,
      last_snapshot_date: dates.at(-1) ?? null,
      gap_count: store.gapCount(config.device),
      task_count: store.tasks().length,
      last_tick_at: status.lastTickAt ? status.lastTickAt.toISOString() : null,
      cache_age_seconds: status.lastTickAt ? Math.round((now().getTime() - status.lastTickAt.getTime()) / 1000) : null,
      last_error: status.lastError,
      malformed_count: status.malformed,
      mirror_error: status.mirrorError,
      stalled_count: [...status.live].filter(slug =>
        buildThread({ store, device: config.device, slug, live: true, now: now(), stallMs: config.tunables.nudge_stall_seconds * 1000 }).stalled,
      ).length,
      ...poller.status(),
      notify_error: notifier.status().last_error,
      sync: sync ? sync.status() : { status: 'not configured' },
    };
  }

  const record = (kind, subject, payload, eventTime) => {
    const e = makeEvent({ device: config.device, kind, subject, payload, recordedAt: now().toISOString(), eventTime });
    appendEvent(config.dataDir, e);
    store.apply(e);
    sync?.noteChange();
  };

  // Actions that change focusflow run one at a time, so two captures can never pick
  // the same slug.
  let queue = Promise.resolve();
  const serial = fn => {
    const p = queue.then(fn);
    queue = p.then(() => {}, () => {});
    return p;
  };

  const runFocusCmd = (args, timeoutMs) =>
    focusRun(args, { env: focusEnv({ focusBin: config.focusBin, terminalApp: config.terminalApp }), timeoutMs });

  const poller = createPoller({
    config, store, runFocusCmd, record, now,
    notify: arrivals => notifier.notify(arrivals),
    isLocked: () => !!sync?.treeLocked,
  });

  const threadResponse = slug => {
    const t = store.getTask(slug);
    const live = status.live.has(slug);
    let workDir = null;
    try { workDir = readWorkDir({ slug }) ?? null; } catch { /* the confirmation just omits it */ }
    return {
      ...buildThread({ store, device: config.device, slug, live, now: now(), stallMs: config.tunables.nudge_stall_seconds * 1000 }),
      live, working: status.working.has(slug), status: t.status, archived: !!t.archived, work_dir: workDir,
      has_session: !!t.has_session, harness: t.harness ?? null, ever_headless: store.everHeadless(slug),
    };
  };

  // Start (backlog), Resume (in progress), Focus (live) or Reopen (done). Start/Resume/Focus
  // open a terminal because the user clicked them; what happens there is up to focusflow, and its
  // output or error is relayed as is. Reopen only moves the task back to in-progress (its session
  // id is kept) and NEVER opens a terminal: messages are answered headlessly, and a terminal is
  // only opened when the user asks for one (Resume).
  async function openTask(slug) {
    if (typeof slug !== 'string' || !SLUG.test(slug)) throw new HttpError(400, 'bad slug');
    refresh();
    const t = store.getTask(slug);
    if (!t) throw new HttpError(404, 'no such task');
    if (t.archived) throw new HttpError(409, 'archived tasks cannot be opened');
    const action = t.status === 'done' ? 'reopen' : status.live.has(slug) ? 'focus' : t.status === 'backlog' ? 'start' : 'resume';
    let result;
    try {
      if (action === 'reopen') {
        await runFocusCmd(['update', 'task', slug, '--status', 'in-progress'], FOCUSFLOW_DO_TIMEOUT_MS);
        result = { ok: true, action, output: 'Reopened. No terminal was opened: send a message to continue it headlessly, or use Resume to open a terminal.' };
      } else {
        const r = await runFocusCmd(['do', slug], FOCUSFLOW_DO_TIMEOUT_MS);
        result = { ok: true, action, output: r.stdout.trim() };
      }
    } catch (err) {
      result = { ok: false, action, error: err.message };
    }
    record('open.requested', `task:${slug}`, { slug, action, ok: result.ok, ...(result.ok ? {} : { error: result.error }) });
    refresh();
    return result;
  }

  // Queue: the message goes onto focusflow's bus for the task; the reply arrives through the poller.
  async function sendQueue(slug, text, extra = {}) {
    let r;
    try {
      r = await runFocusCmd(['message', `user/${slug}`, queueBody(text)], 15000);
    } catch (err) {
      throw new HttpError(502, err.message);
    }
    const focusId = parseFocusId(r.stdout);
    record('message.sent', `task:${slug}`, { slug, text, mode: 'queue', flow_id: focusId, ok: true });
    return { ok: true, mode: 'queue', flow_id: focusId, ...extra };
  }

  async function sendMessageCore(slug, body) {
    const c = checkText(body.text);
    if (!c.ok) throw new HttpError(400, c.error);
    if (body.mode !== 'queue' && body.mode !== 'headless') throw new HttpError(400, 'mode must be queue or headless');
    refresh();
    const t = store.getTask(slug);
    if (!t) throw new HttpError(404, 'no such task');
    if (t.archived) throw new HttpError(409, 'archived tasks cannot receive messages');
    if (t.status === 'done') throw new HttpError(409, 'done tasks cannot receive messages');
    if (body.mode === 'queue') return sendQueue(slug, c.text);

    if (body.confirm !== true) throw new HttpError(400, 'headless runs need confirm: true');
    // A live session would be answering already; do not start a second, unattended one.
    if (status.live.has(slug)) {
      return sendQueue(slug, c.text, { downgraded: true, note: 'The task is live, so the message was queued instead of starting a headless run.' });
    }
    if (!t.has_session) {
      // First-ever run: only focusflow can start this cleanly.
      let error = null;
      try {
        await runFocusCmd(['do', slug, '--auto', '--with', headlessBody(c.text)], FOCUSFLOW_DO_TIMEOUT_MS);
      } catch (err) {
        error = err.message;
      }
      record('message.sent', `task:${slug}`, { slug, text: c.text, mode: 'headless', via: 'auto', ok: error === null, ...(error === null ? {} : { error }) });
      refresh();
      if (error !== null) throw new HttpError(502, error);
      return { ok: true, mode: 'headless' };
    }

    // A session exists but is not live: continue it directly (focusflow's own `do --auto` would reuse
    // `--session-id`, which Claude rejects once that id already exists). Only supported for the
    // claude harness — focusflow's CLI has no equivalent for any harness, so this app talks to the
    // binary itself.
    if (t.harness !== 'claude') {
      throw new HttpError(409, `focusflow cannot continue a "${t.harness ?? 'unknown'}" session headlessly from focusflow yet. Use Queue instead, then Resume the task to get the answer.`);
    }
    const session = readSession({ slug });
    const workDir = session ? readWorkDir({ slug }) : null;
    if (!session || !workDir) {
      throw new HttpError(409, 'cannot find this task\'s session or work directory to continue it headlessly. Use Queue instead, then Resume the task to get the answer.');
    }
    let error = null;
    try {
      await resumeRun({
        claudeBin: config.claudeBin, sessionId: session.sessionId, text: headlessBody(c.text), cwd: workDir,
        env: focusEnv({ focusBin: config.claudeBin, terminalApp: config.terminalApp }),
        logFile: path.join(path.dirname(config.cacheDb), 'resume-logs', `${slug}-${Date.now()}.log`),
      });
    } catch (err) {
      error = err.message;
    }
    record('message.sent', `task:${slug}`, { slug, text: c.text, mode: 'headless', via: 'resume', ok: error === null, ...(error === null ? {} : { error }) });
    refresh();
    if (error !== null) throw new HttpError(502, error);
    return { ok: true, mode: 'headless' };
  }

  // From a project coordinator's chat, `fanout` also tells every open subtask of that project.
  async function sendMessage(slug, body) {
    const r = await sendMessageCore(slug, body);
    if (body.fanout === true) {
      const t = store.getTask(slug);
      if (t?.kind === 'project_agent' && t.project) {
        try { await runFocusCmd(['project', 'message', t.project, String(body.text).trim(), '--subtasks-only'], 15000); r.fanned_out = true; } catch (err) {
          throw new HttpError(502, `sent to the coordinator, but the fan-out failed: ${err.message}`);
        }
      }
    }
    return r;
  }

  // Local read state comes first (it is the truth); telling focusflow is best effort.
  async function markRead(slug) {
    const covered = store.unreadIncoming(config.device, slug);
    if (!covered.length) return { ok: true, marked: 0 };
    record('message.read', `task:${slug}`, { slug, through: covered.at(-1).at });
    for (const m of covered) {
      try { await runFocusCmd(['inbox', 'read', m.flow_id], 10000); } catch { /* focusflow may have dropped the row */ }
    }
    return { ok: true, marked: covered.length };
  }

  // The "+ New project" form: name, priority and an existing directory. Focusflow needs a work_dir,
  // and creating one on disk is out of scope, so a missing path is refused here.
  function validateNewProject(input) {
    if (typeof input !== 'object' || input === null || Array.isArray(input)) throw new HttpError(400, 'new_project must be an object');
    let name;
    try { name = validateName(input.name); } catch (err) {
      throw new HttpError(400, `project ${err instanceof CaptureError ? err.message : 'name is invalid'}`);
    }
    const priority = input.priority ?? 'medium';
    if (!PRIORITIES.includes(priority)) throw new HttpError(400, 'project priority must be high, medium or low');
    const dir = input.work_dir;
    // eslint-disable-next-line no-control-regex
    if (typeof dir !== 'string' || !path.isAbsolute(dir) || /[\u0000-\u001f\u007f]/.test(dir)) {
      throw new HttpError(400, 'project work_dir must be an absolute path');
    }
    let isDir = false;
    try { isDir = fs.statSync(dir).isDirectory(); } catch { /* falls through to the error below */ }
    if (!isDir) throw new HttpError(400, `project work_dir "${dir}" is not an existing directory`);
    return { name, priority, workDir: dir };
  }

  async function captureTask(body) {
    let name;
    try { name = validateName(body.name); } catch (err) {
      throw new HttpError(400, err instanceof CaptureError ? err.message : 'bad name');
    }
    const priority = body.priority ?? 'medium';
    if (!PRIORITIES.includes(priority)) throw new HttpError(400, 'priority must be high, medium or low');
    const note = body.note ?? '';
    if (typeof note !== 'string') throw new HttpError(400, 'note must be text');
    if (note.length > MAX_NOTE) throw new HttpError(400, `note must be at most ${MAX_NOTE} characters`);
    let project = null;
    const hasProject = body.project !== undefined && body.project !== null && body.project !== '';
    const hasNewProject = body.new_project !== undefined && body.new_project !== null;
    if (hasProject && hasNewProject) throw new HttpError(400, 'pick either an existing project or a new one, not both');
    const newProject = hasNewProject ? validateNewProject(body.new_project) : null;
    if (hasProject) {
      if (typeof body.project !== 'string' || !SLUG.test(body.project)) throw new HttpError(400, 'bad project');
      let known;
      try { known = readProjects(); } catch (err) { throw new HttpError(502, `cannot read focusflow projects: ${err.message}`); }
      if (!known.some(p => p.slug === body.project)) throw new HttpError(400, `unknown project "${body.project}"`);
      project = body.project;
    }

    // The project comes first; if it fails no task is attempted. If the task then fails, the
    // project stays (nothing is rolled back) and the error names it so a retry can reuse it.
    if (newProject) {
      let known;
      try { known = readProjects(); } catch (err) { throw new HttpError(502, `cannot read focusflow projects: ${err.message}`); }
      const projectSlug = uniqueSlug(slugify(newProject.name, 'project'), new Set(known.map(p => p.slug)));
      try {
        await runFocusCmd(['add', 'project', newProject.name, '--slug', projectSlug, '--work-dir', newProject.workDir, '--priority', newProject.priority], 15000);
      } catch (err) {
        throw new HttpError(502, err.message);
      }
      project = projectSlug;
      record('project.created', `project:${project}`, { slug: project, name: newProject.name, priority: newProject.priority, work_dir: newProject.workDir });
    }

    refresh();
    const slug = uniqueSlug(slugify(name), new Set(store.tasks().map(t => t.slug)));
    const args = ['add', 'task', name, '--slug', slug, ...(project ? ['--project', project] : []), '--priority', priority];
    try {
      await runFocusCmd(args, 15000);
    } catch (err) {
      if (newProject) throw new HttpError(502, `project "${project}" was created, but the task was not: ${err.message}`, { project });
      throw new HttpError(502, err.message);
    }

    // Replace focusflow's placeholder brief with the thin brief. Never overwrite real content.
    let briefWritten = false;
    let warning = null;
    const briefPath = path.join(config.focusRoot, 'tasks', slug, 'brief.md');
    try {
      const existing = readText(briefPath);
      if (existing === null || isStubBrief(existing, name)) {
        const workDir = readWorkDir({ slug }) ?? '(set by focusflow)';
        const tmp = `${briefPath}.tmp-${process.pid}`;
        fs.writeFileSync(tmp, thinBrief({ name, note, workDir }));
        fs.renameSync(tmp, briefPath);
        briefWritten = true;
      } else {
        warning = 'the new task\'s brief already had content, so it was left as is';
      }
    } catch (err) {
      warning = `task created but the brief was not written: ${err.message}`;
    }

    record('capture.created', `task:${slug}`, { slug, name, project, priority, has_note: note.trim() !== '', brief_written: briefWritten });
    refresh();

    const result = { slug, project, brief_written: briefWritten, warning, opened: false };
    if (body.open === true) {
      const o = await openTask(slug);
      result.opened = o.ok;
      if (!o.ok) result.open_error = o.error;
    }
    return result;
  }

  // The recovery steps travel inside the page, so the app can still show them after the server stops
  // answering. They hold this machine's project folder and port, never the token (the page needs none).
  function withHelpData(html, port) {
    const data = { html: appFragments(helpContent({ projectDir: DEFAULT_PROJECT_DIR, port })) };
    const json = JSON.stringify(data).replace(/</g, '\\u003c');
    return html.replace('<script type="application/json" id="help-data"></script>', () => `<script type="application/json" id="help-data">${json}</script>`);
  }


  // ---- stages, pause, approve ----
  const TASK_ACTIONS = {
    pause: { from: ['backlog', 'next', 'in-progress', 'review'], args: slug => ['pause', slug] },
    next: { from: ['backlog', 'paused', 'in-progress', 'review'], args: slug => ['next', slug] },
    review: { from: ['next', 'in-progress', 'paused'], args: slug => ['review', slug] },
    unpause: { from: ['paused', 'done', 'review', 'next', 'backlog'], args: slug => ['update', 'task', slug, '--status', 'in-progress'] },
    backlog: { from: ['next', 'paused', 'in-progress', 'review'], args: slug => ['update', 'task', slug, '--status', 'backlog'] },
    archive: { from: STAGE_LIST(), args: slug => ['archive', `task/${slug}`] },
    unarchive: { from: STAGE_LIST(), args: slug => ['unarchive', `task/${slug}`], archivedOnly: true },
  };
  function STAGE_LIST() { return ['backlog', 'next', 'in-progress', 'paused', 'review', 'done']; }

  async function taskAction(slug, body) {
    const t = store.getTask(slug);
    if (!t) throw new HttpError(404, 'no such task');
    const action = body.action;
    if (action !== 'approve' && !Object.hasOwn(TASK_ACTIONS, action)) throw new HttpError(400, 'unknown action');
    if (t.archived && action !== 'unarchive') throw new HttpError(409, 'archived tasks can only be unarchived');
    if (t.kind === 'project_agent' && ['approve', 'review', 'archive'].includes(action)) throw new HttpError(409, 'a project coordinator is permanent');
    let args;
    let detached = false;
    if (action === 'approve') {
      if (!['review', 'in-progress', 'paused'].includes(t.status)) throw new HttpError(409, `only work in review (or in progress) can be approved, this one is ${t.status}`);
      args = ['done', slug];
      detached = true; // `done` runs a KB sweep that can take minutes; the status flips first
    } else {
      const spec = TASK_ACTIONS[action];
      if (action === 'unarchive' ? !t.archived : !spec.from.includes(t.status)) {
        throw new HttpError(409, action === 'unarchive' ? 'this task is not archived' : `cannot ${action} a task that is ${t.status}`);
      }
      args = spec.args(slug);
    }
    let output = '';
    try {
      if (detached) {
        await focusRunDetached(args, { env: focusEnv({ focusBin: config.focusBin, terminalApp: config.terminalApp }) });
        await new Promise(r => setTimeout(r, config.tunables.approve_settle_ms ?? 1500));
        output = 'Approved. Closing out (the knowledge-base sweep finishes in the background).';
      } else {
        output = (await runFocusCmd(args, FOCUSFLOW_DO_TIMEOUT_MS)).stdout.trim();
      }
    } catch (err) {
      record('task.action', `task:${slug}`, { slug, action, ok: false, error: err.message });
      throw new HttpError(502, err.message);
    }
    record('task.action', `task:${slug}`, { slug, action, ok: true });
    refresh();
    return { ok: true, action, output };
  }

  // ---- projects ----
  const projectOr404 = slug => {
    if (typeof slug !== 'string' || !SLUG.test(slug)) throw new HttpError(400, 'bad project slug');
    let all;
    try { all = readProjectsFull(); } catch (err) { throw new HttpError(502, `cannot read focusflow projects: ${err.message}`); }
    const p = all.find(x => x.slug === slug);
    if (!p) throw new HttpError(404, 'no such project');
    return p;
  };

  const everyOk = v => typeof v === 'string' && /^[1-9]\d{0,3}[mhdw]$/.test(v);

  async function projectAction(slug, body) {
    const p = projectOr404(slug);
    const a = body.action;
    let args;
    switch (a) {
      case 'pause': args = ['pause', `project/${slug}`]; break;
      case 'archive': if (p.archived) throw new HttpError(409, 'already archived'); args = ['archive', `project/${slug}`]; break;
      case 'unarchive': if (!p.archived) throw new HttpError(409, 'not archived'); args = ['unarchive', `project/${slug}`]; break;
      case 'unschedule': args = ['project', 'unschedule', slug]; break;
      case 'ensure-agent': {
        if (p.archived) throw new HttpError(409, 'archived projects cannot be opened');
        return { ok: true, action: a, agent: await ensureAgent(slug) };
      }
      case 'schedule':
        if (!everyOk(body.every ?? '1d')) throw new HttpError(400, 'every must look like 12h, 1d or 1w');
        args = ['project', 'schedule', slug, '--every', body.every ?? '1d'];
        break;
      case 'open-agent': {
        // The user asked for a terminal on the project's coordinator session: that is the one place a
        // project action opens one.
        if (p.archived) throw new HttpError(409, 'archived projects cannot be opened');
        const agent = await ensureAgent(slug);
        const o = await openTask(agent);
        if (!o.ok) throw new HttpError(502, o.error);
        return { ok: true, action: a, agent, output: o.output };
      }
      default: throw new HttpError(400, 'unknown action');
    }
    if (p.archived && a !== 'unarchive') throw new HttpError(409, 'archived projects can only be unarchived');
    let output;
    try { output = (await runFocusCmd(args, FOCUSFLOW_DO_TIMEOUT_MS)).stdout.trim(); } catch (err) {
      record('project.action', `project:${slug}`, { slug, action: a, ok: false, error: err.message });
      throw new HttpError(502, err.message);
    }
    record('project.action', `project:${slug}`, { slug, action: a, ok: true });
    refresh();
    return { ok: true, action: a, output };
  }

  async function ensureAgent(slug) {
    let out;
    try { out = (await runFocusCmd(['project', 'agent', slug], 15000)).stdout; } catch (err) { throw new HttpError(502, err.message); }
    const agent = out.trim().split('\n').at(-1).trim();
    if (!SLUG.test(agent)) throw new HttpError(502, 'focusflow did not return the coordinator task');
    refresh();
    return agent;
  }

  // A project-level message goes to the project's coordinator session (created on first use), which
  // decides whether it belongs to an existing subtask or needs a new one. `fanout` also tells every
  // open subtask directly.
  async function projectMessage(slug, body) {
    const p = projectOr404(slug);
    if (p.archived) throw new HttpError(409, 'archived projects cannot receive messages');
    const c = checkText(body.text);
    if (!c.ok) throw new HttpError(400, c.error);
    const agent = await ensureAgent(slug);
    const r = await sendMessage(agent, { text: c.text, mode: body.mode, confirm: body.confirm });
    let fanned = false;
    if (body.fanout === true) {
      try {
        await runFocusCmd(['project', 'message', slug, c.text, '--subtasks-only'], 15000);
        fanned = true;
      } catch (err) {
        throw new HttpError(502, `sent to the coordinator, but the fan-out failed: ${err.message}`);
      }
    }
    return { ...r, agent, fanned_out: fanned };
  }

  // One native folder dialog at a time; a second click while one is open just says so.
  let picking = false;
  async function pickFolderNow(body) {
    if (picking) throw new HttpError(409, 'a folder dialog is already open');
    const start = typeof body.start === 'string' ? body.start : undefined;
    const prompt = typeof body.prompt === 'string' ? body.prompt.slice(0, 120) : undefined;
    picking = true;
    try { return await pickFolder({ start, prompt }); } catch (err) { throw new HttpError(502, err.message); } finally { picking = false; }
  }

  async function newProject(body) {
    const np = validateNewProject(body);
    let known;
    try { known = readProjectsFull(); } catch (err) { throw new HttpError(502, `cannot read focusflow projects: ${err.message}`); }
    const slug = uniqueSlug(slugify(np.name, 'project'), new Set(known.map(x => x.slug)));
    try {
      await runFocusCmd(['add', 'project', np.name, '--slug', slug, '--work-dir', np.workDir, '--priority', np.priority], 15000);
    } catch (err) { throw new HttpError(502, err.message); }
    record('project.created', `project:${slug}`, { slug, name: np.name, priority: np.priority, work_dir: np.workDir });
    return { ok: true, slug };
  }

  // A draft subtask: created in the backlog and left there — nothing starts.
  async function draftSubtask(slug, body) {
    projectOr404(slug);
    return captureTask({ name: body.name, priority: body.priority, project: slug, open: false });
  }

  function buildOverviewNow() {
    const tasks = store.tasks().map(view);
    return buildOverview({ tasks, projects: readProjectsFull(), owners: safeOwners() });
  }
  const safeOwners = () => { try { return readOwners(); } catch { return []; } };

  // ---- stats, search, health (all through the focusflow CLI) ----
  const cache = new Map();
  async function cached(key, ttlMs, fn) {
    const hit = cache.get(key);
    if (hit && now().getTime() - hit.at < ttlMs) return hit.value;
    const value = await fn();
    cache.set(key, { at: now().getTime(), value });
    return value;
  }
  const parseJsonOut = (text, what) => {
    try { return JSON.parse(text); } catch { throw new HttpError(502, `focusflow ${what} returned output that is not JSON`); }
  };

  async function statsNow(since) {
    if (!/^(all|\d{1,3}d)$/.test(since)) throw new HttpError(400, 'since must be all or like 30d');
    return cached(`stats:${since}`, 30000, async () => {
      try { return parseJsonOut((await runFocusCmd(['stats', '--json', '--since', since], 60000)).stdout, 'stats'); } catch (err) {
        if (err instanceof HttpError) throw err;
        throw new HttpError(502, err.message);
      }
    });
  }

  async function searchNow(q) {
    if (typeof q !== 'string' || q.trim() === '' || q.length > 100) throw new HttpError(400, 'q must be 1-100 characters');
    if (q.trim().startsWith('-')) throw new HttpError(400, 'q must not start with "-"');
    try { return { hits: parseJsonOut((await runFocusCmd(['search', q.trim(), '--json', '--limit', '30'], 20000)).stdout, 'search') }; } catch (err) {
      if (err instanceof HttpError) throw err;
      throw new HttpError(502, err.message);
    }
  }

  async function doctorNow() {
    return cached('doctor', 60000, async () => {
      let out;
      try { out = (await runFocusCmd(['doctor', '--json'], 60000)).stdout; } catch (err) {
        if (!err.stdout) throw new HttpError(502, err.message); // doctor exits 1 on a FAIL but still prints its findings
        out = err.stdout;
      }
      const findings = parseJsonOut(out, 'doctor');
      const count = lvl => findings.filter(f => f.level === lvl).length;
      return { ok: count('fail') === 0, warn: count('warn'), fail: count('fail'), problems: findings.filter(f => f.level !== 'ok') };
    });
  }

  async function handle(req, res) {
    const port = req.socket.localPort;
    const hosts = [`127.0.0.1:${port}`, `localhost:${port}`];
    if (!hosts.includes(req.headers.host)) return fail(res, 403, 'bad host');
    if (req.headers.origin !== undefined && !hosts.some(h => req.headers.origin === `http://${h}`)) return fail(res, 403, 'bad origin');

    const { pathname } = new URL(req.url, 'http://localhost');

    if (!pathname.startsWith('/api/')) {
      const entry = STATIC[pathname];
      if (!entry) return fail(res, 404, 'not found');
      if (req.method !== 'GET') return fail(res, 405, 'method not allowed', { allow: 'GET' });
      let body = readText(path.join(PUBLIC_DIR, entry[0]));
      if (body !== null && pathname === '/') body = withHelpData(body, port);
      return body === null ? fail(res, 404, 'not found') : send(res, 200, body, entry[1]);
    }

    if (!tokenOk(req.headers['x-focusflow-token'], config.token)) return fail(res, 401, 'missing or wrong token');

    const need = (method) => {
      if (req.method === method) return true;
      fail(res, 405, 'method not allowed', { allow: method });
      return false;
    };

    if (pathname === '/api/inbox') {
      if (!need('GET')) return;
      return json(res, 200, buildInbox({ store, device: config.device, live: status.live }));
    }
    const tm = /^\/api\/tasks\/([^/]+)\/(thread|message|read|action)$/.exec(pathname);
    if (tm) {
      let slug;
      try { slug = decodeURIComponent(tm[1]); } catch { return fail(res, 400, 'bad slug'); }
      if (!SLUG.test(slug)) return fail(res, 400, 'bad slug');
      if (!store.getTask(slug)) return fail(res, 404, 'no such task');
      if (tm[2] === 'thread') {
        if (!need('GET')) return;
        return json(res, 200, threadResponse(slug));
      }
      if (!need('POST')) return;
      const body = await readJson(req);
      if (tm[2] === 'message') return json(res, 200, await serial(() => sendMessage(slug, body)));
      if (tm[2] === 'action') return json(res, 200, await serial(() => taskAction(slug, body)));
      return json(res, 200, await markRead(slug));
    }
    if (pathname === '/api/tasks') {
      if (!need('GET')) return;
      return json(res, 200, { tasks: store.tasks().map(view), generated_at: now().toISOString() });
    }
    if (pathname.startsWith('/api/tasks/')) {
      if (!need('GET')) return;
      let slug;
      try { slug = decodeURIComponent(pathname.slice('/api/tasks/'.length)); } catch { return fail(res, 400, 'bad slug'); }
      if (!SLUG.test(slug)) return fail(res, 400, 'bad slug');
      const d = detail(slug);
      return d ? json(res, 200, d) : fail(res, 404, 'no such task');
    }
    if (pathname === '/api/health') {
      if (!need('GET')) return;
      return json(res, 200, health());
    }
    if (pathname === '/api/refresh') {
      if (!need('POST')) return;
      const r = refresh();
      return json(res, 200, { skipped: !!r.skipped, error: r.error ?? null, events: r.events.length });
    }
    if (pathname === '/api/projects') {
      if (!need('GET')) return;
      try { return json(res, 200, { projects: readProjects(), default_work_dir: DEFAULT_PROJECT_DIR }); } catch (err) { return fail(res, 502, `cannot read focusflow projects: ${err.message}`); }
    }
    if (pathname === '/api/open') {
      if (!need('POST')) return;
      const body = await readJson(req);
      const r = await serial(() => openTask(body.slug));
      return r.ok ? json(res, 200, { ok: true, action: r.action, output: r.output }) : fail(res, 502, r.error);
    }

    if (pathname === '/api/projects/overview') {
      if (!need('GET')) return;
      try { return json(res, 200, buildOverviewNow()); } catch (err) { return fail(res, 502, `cannot read focusflow projects: ${err.message}`); }
    }
    if (pathname === '/api/archive') {
      if (!need('GET')) return;
      try { return json(res, 200, buildArchive({ tasks: store.tasks().map(view), projects: readProjectsFull() })); } catch (err) { return fail(res, 502, `cannot read focusflow projects: ${err.message}`); }
    }
    const pm = /^\/api\/projects\/([^/]+)\/(action|message|task)$/.exec(pathname);
    if (pm) {
      if (!need('POST')) return;
      let pslug;
      try { pslug = decodeURIComponent(pm[1]); } catch { return fail(res, 400, 'bad slug'); }
      const body = await readJson(req);
      if (pm[2] === 'action') return json(res, 200, await serial(() => projectAction(pslug, body)));
      if (pm[2] === 'message') return json(res, 200, await serial(() => projectMessage(pslug, body)));
      return json(res, 200, await serial(() => draftSubtask(pslug, body)));
    }
    if (pathname === '/api/pick-folder') {
      if (!need('POST')) return;
      return json(res, 200, await pickFolderNow(await readJson(req)));
    }
    if (pathname === '/api/projects/new') {
      if (!need('POST')) return;
      return json(res, 200, await serial(async () => newProject(await readJson(req))));
    }
    if (pathname === '/api/stats') {
      if (!need('GET')) return;
      return json(res, 200, await statsNow(new URL(req.url, 'http://localhost').searchParams.get('since') ?? 'all'));
    }
    if (pathname === '/api/search') {
      if (!need('GET')) return;
      return json(res, 200, await searchNow(new URL(req.url, 'http://localhost').searchParams.get('q')));
    }
    if (pathname === '/api/doctor') {
      if (!need('GET')) return;
      return json(res, 200, await doctorNow());
    }
    if (pathname === '/api/capture') {
      if (!need('POST')) return;
      const body = await readJson(req);
      return json(res, 200, await serial(() => captureTask(body)));
    }
    return fail(res, 404, 'not found');
  }

  const server = http.createServer((req, res) => {
    handle(req, res).catch(err => {
      if (err instanceof HttpError && !res.headersSent) return json(res, err.status, { error: err.message, ...err.data });
      console.error(err);
      if (!res.headersSent) fail(res, 500, 'internal error');
      else res.end();
    });
  });
  return { server, refresh, status, serial, pollInbox: poller.pollOnce };
}
