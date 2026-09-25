import { api, beacon } from './api.js';
import { toast, oops } from './ui.js';

// One place that holds what the app knows, refreshes it, and changes it. Views subscribe and redraw.
export const S = {
  tasks: [], needs: { items: [], counts: {}, badge: 0, hidden: [] }, inbox: { unread_total: 0, threads: [] },
  overview: { projects: [], floating: [] }, projectList: [], defaultDir: '', runs: new Map(), health: null, loaded: false, error: null,
};
const subs = new Set();
export const subscribe = fn => { subs.add(fn); return () => subs.delete(fn); };
export const notify = () => emit();
const emit = () => { for (const fn of subs) { try { fn(); } catch (err) { console.error(err); } } };

// ---- Undo: approve, archive and pause-project wait a few seconds before anything is sent ----
const UNDO_MS = 6000;
const pending = new Map(); // key -> { path, body, timer, apply? }
export const pendingFor = key => pending.get(key);
export function overlay(t) {
  const p = pending.get(`task:${t.slug}`);
  return p?.apply ? p.apply(t) : t;
}
export const tasksNow = () => S.tasks.map(overlay).filter(Boolean);
export const taskBy = slug => { const t = S.tasks.find(x => x.slug === slug); return t ? overlay(t) : null; };

async function flush(key) {
  const p = pending.get(key);
  if (!p || p.sending) return;
  p.sending = true;
  clearTimeout(p.timer);
  try { await api(p.path, 'POST', p.body); } catch (err) { oops(err, `Could not ${p.verb}`); }
  pending.delete(key);
  await load();
}
function defer({ key, path, body, verb, label, apply }) {
  if (pending.has(key)) return;
  const entry = { path, body, verb, apply, timer: setTimeout(() => flush(key), UNDO_MS) };
  pending.set(key, entry);
  emit();
  toast(label, { undo: () => { clearTimeout(entry.timer); pending.delete(key); emit(); }, ms: UNDO_MS });
}
addEventListener('pagehide', () => { for (const [, p] of pending) beacon(p.path, p.body); });

// ---- loading ----
let inflight = null;
export function load() {
  inflight ??= (async () => {
    try {
      const [t, n, i, o, p] = await Promise.all([
        api('/api/tasks'), api('/api/needs-you'), api('/api/inbox'), api('/api/projects/overview'), api('/api/projects'),
      ]);
      S.tasks = t.tasks; S.needs = n; S.inbox = i; S.overview = o; S.projectList = p.projects; S.defaultDir = p.default_work_dir || '';
      const running = S.tasks.filter(x => x.headless_running || x.auto_run_status === 'dead' || x.auto_run_status === 'timed_out');
      const runs = await Promise.all(running.map(x => api(`/api/tasks/${encodeURIComponent(x.slug)}/run`).then(r => [x.slug, r]).catch(() => null)));
      S.runs = new Map(runs.filter(Boolean));
      S.loaded = true; S.error = null;
    } catch (err) {
      if (!err.helped) S.error = err;
    } finally {
      inflight = null;
    }
    emit();
  })();
  return inflight;
}
export async function loadLight() {
  try {
    const [n, i] = await Promise.all([api('/api/needs-you'), api('/api/inbox')]);
    const changed = JSON.stringify([n, i]) !== JSON.stringify([S.needs, S.inbox]);
    S.needs = n; S.inbox = i;
    if (changed) emit();
  } catch { /* the full load reports problems */ }
}

// ---- actions ----
const VERB = { approve: 'approve that', pause: 'pause that', next: 'move that to Up next', review: 'send that for review', unpause: 'resume that', backlog: 'move that to the backlog', archive: 'archive that', unarchive: 'restore that' };
const DONE_TEXT = { pause: 'Paused.', next: 'Moved to Up next.', review: 'Sent for review.', unpause: 'Back in progress.', backlog: 'Moved to the backlog.', unarchive: 'Restored.' };

export async function act(slug, action) {
  const t = taskBy(slug);
  const name = t?.name ?? slug;
  if (action === 'approve' || action === 'archive') {
    defer({
      key: `task:${slug}`, path: `/api/tasks/${encodeURIComponent(slug)}/action`, body: { action }, verb: VERB[action],
      label: action === 'approve' ? `Approved “${name}”.` : `Archived “${name}”.`,
      apply: action === 'approve' ? x => ({ ...x, status: 'done', status_changed_at: new Date().toISOString() }) : () => null,
    });
    return;
  }
  try {
    const r = await api(`/api/tasks/${encodeURIComponent(slug)}/action`, 'POST', { action });
    toast(DONE_TEXT[action] ?? (r.output || 'Done.'));
  } catch (err) { oops(err, `Could not ${VERB[action] ?? action}`); }
  await load();
}

export async function projectAct(slug, action, extra = {}) {
  const p = S.overview.projects.find(x => x.slug === slug);
  const name = p?.name ?? slug;
  if (action === 'archive' || action === 'pause') {
    defer({
      key: `project:${slug}:${action}`, path: `/api/projects/${encodeURIComponent(slug)}/action`, body: { action, ...extra },
      verb: action === 'archive' ? 'archive that project' : 'pause that project',
      label: action === 'archive' ? `Archived “${name}”.` : `Pausing everything in “${name}”.`,
    });
    return;
  }
  try {
    const r = await api(`/api/projects/${encodeURIComponent(slug)}/action`, 'POST', { action, ...extra });
    toast(r.output || 'Done.');
    await load();
    return r;
  } catch (err) { oops(err, 'That did not work'); }
}
export const projectHidden = slug => pending.has(`project:${slug}:archive`);

export async function openTerminal(slug) {
  try {
    const r = await api('/api/open', 'POST', { slug });
    toast(r.output ? 'Opened in your terminal.' : 'Opened.');
  } catch (err) { oops(err, 'Could not open the session'); }
  await load();
}

// Sending: while a terminal is open the message goes to that session; otherwise the task gets to work in
// the background. Other harnesses without a continuation path get it on their next run.
export async function send(slug, text) {
  const thread = await api(`/api/tasks/${encodeURIComponent(slug)}/thread`);
  const background = !thread.live && (!thread.has_session || thread.harness === 'claude');
  const r = await api(`/api/tasks/${encodeURIComponent(slug)}/message`, 'POST', background ? { text, mode: 'headless', confirm: true } : { text, mode: 'queue' });
  await load();
  return { ...r, background };
}

export async function edit(kind, slug, body) {
  const r = await api(`/api/${kind}/${encodeURIComponent(slug)}/edit`, 'POST', body);
  await load();
  return r;
}

export async function capture({ name, project, priority = 'medium', note, stage = 'next' }) {
  const r = await api('/api/capture', 'POST', { name, project: project || undefined, priority, note: note || undefined, stage });
  await load();
  return r;
}
