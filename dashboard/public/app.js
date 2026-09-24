'use strict';

// The board only shows work that is being picked up; plain backlog lives under each project.
const COLUMNS = [
  { status: 'next', title: 'Next' },
  { status: 'in-progress', title: 'In progress' },
  { status: 'paused', title: 'Paused' },
  { status: 'review', title: 'Review' },
  { status: 'done', title: 'Done (recent)' },
];
const DONE_RECENT_DAYS = 7;
const VIEWS = ['needs', 'board', 'projects', 'archive', 'inbox'];
const STAGE_ICON = { backlog: '○', next: '◔', 'in-progress': '▶', paused: '⏸', review: '◎', done: '✓' };
const STAGE_HINT = { next: 'Add one with the box above.', 'in-progress': 'Nothing running. Open a task from Next.', paused: 'Nothing set aside.', review: 'Nothing waiting for approval.', done: 'Approved work shows up here.' };
const PRI = { high: 0, medium: 1, low: 2 };
const POLL_MS = 30000;

const $ = id => document.getElementById(id);
let token = null;
let tasks = [];
let showArchived = false;
let openSlug = null;
let lastFocus = null;
const INBOX_POLL_MS = 5000;
let view = 'needs';
try { const v = localStorage.getItem('focusflow-view'); if (VIEWS.includes(v)) view = v; } catch { /* the default is fine */ }
let overview = { projects: [], floating: [] };
let archiveData = { projects: [], tasks: [] };
let expanded = new Set();
try { expanded = new Set(JSON.parse(localStorage.getItem('focusflow-expanded') || '[]')); } catch { /* none expanded */ }
let inbox = { unread_total: 0, threads: [] };
let soundOn = false;
try { soundOn = localStorage.getItem('focusflow-sound') === '1'; } catch { /* off */ }
let lastUnread = null;
let audio = null;
let markTimer = null;
let thread = null; // the last thread response for the open drawer (its work dir is used in the confirmation)
let needs = { items: [], counts: {}, total: 0, badge: 0 };
const runInfos = new Map(); // slug -> /api/tasks/:slug/run, for tasks running headless
let knownProjects = [];
let chatHome = 'drawer'; // where the single #chat element currently lives: the drawer or the inbox pane
let inboxSel = null;
let activeTab = 'chat';
let landedSlug = null;
let dragSlug = null;
let filters = { project: '', priority: '', text: '' };
try { Object.assign(filters, JSON.parse(localStorage.getItem('focusflow-filters') || '{}')); } catch { /* no filters */ }
const drafts = new Map(); // in-progress text of the inline forms, kept across redraws
const openSchedulers = new Set();
const dirty = new Set(); // views whose redraw was postponed because the user was typing in them
const say = err => Logic.friendlyError(err?.message ?? err);

// The token arrives in the URL fragment (never sent to the server), is kept in
// localStorage and removed from the address bar.
function initToken() {
  const params = new URLSearchParams(location.hash.slice(1));
  const fromUrl = params.get('token');
  try {
    if (fromUrl) localStorage.setItem('focusflow-token', fromUrl);
    token = fromUrl || localStorage.getItem('focusflow-token');
  } catch { token = fromUrl; }
  if (params.get('mini') === '1') document.documentElement.classList.add('mini'); // the floating mini board
  for (const slug of (params.get('open') || '').split(',').filter(Boolean)) expanded.add(slug); // deep link: #open=resume
  const wanted = params.get('view'); // deep link: #view=projects
  if (VIEWS.includes(wanted)) view = wanted;
  if (fromUrl) {
    params.delete('token');
    history.replaceState(null, '', location.pathname + (params.toString() ? '#' + params : ''));
  }
}

function el(tag, opts = {}, kids = []) {
  const n = document.createElement(tag);
  if (opts.class) n.className = opts.class;
  if (opts.text != null) n.textContent = opts.text;
  for (const [k, v] of Object.entries(opts.attrs || {})) n.setAttribute(k, v);
  for (const k of [].concat(kids)) if (k) n.append(k);
  return n;
}

// Every request goes through Help.request, which tells "no token", "token rejected" and "server not
// answering" apart and puts the recovery steps on screen for each (public/help.js).
async function api(path, method = 'GET', body) {
  const headers = {};
  if (body !== undefined) headers['content-type'] = 'application/json';
  const res = await Help.request(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), token });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || `request failed (${res.status})`);
    err.data = data; // e.g. the project a failed capture had already created
    throw err;
  }
  return data;
}

// An error the help panel or banner already shows is not repeated as the generic banner.
function reportError(err) {
  if (!err.helped) showBanner(say(err));
}

function showBanner(msg) {
  $('banner').hidden = !msg;
  $('banner').textContent = msg || '';
}

let toastTimer = null;
function toast(msg, warn = false, opts = {}) {
  const t = $('toast');
  clearTimeout(toastTimer);
  t.hidden = !msg;
  t.classList.toggle('warn', warn);
  t.replaceChildren();
  if (!msg) return;
  t.append(el('span', { class: 'toast-text', text: msg }));
  if (opts.undo) {
    const u = el('button', { class: 'toast-btn', text: 'Undo', attrs: { type: 'button' } });
    u.addEventListener('click', () => { t.hidden = true; clearTimeout(toastTimer); opts.undo(); });
    t.append(u);
  }
  const x = el('button', { class: 'toast-close', text: '×', attrs: { type: 'button', 'aria-label': 'Dismiss' } });
  x.addEventListener('click', () => { t.hidden = true; clearTimeout(toastTimer); });
  t.append(x);
  toastTimer = setTimeout(() => { t.hidden = true; }, opts.ms ?? (warn ? 15000 : 8000));
}

function ago(iso) { return Logic.humanTime(iso); }

function marks(t) {
  const s = t.signals;
  const out = [];
  if (s.stale) out.push(el('span', { class: 'mark stale', text: '!', attrs: { title: 'Stale: no activity for a while', 'aria-label': 'stale' } }));
  if (s.waiting) out.push(el('span', { class: 'mark waiting', text: 'W', attrs: { title: `Waiting on: ${t.waiting_on}`, 'aria-label': 'waiting' } }));
  if (s.live) out.push(s.working
    ? el('span', { class: 'mark working', text: '*', attrs: { title: 'Session is working', 'aria-label': 'working' } })
    : el('span', { class: 'mark live', text: '*', attrs: { title: 'Session is live, idle', 'aria-label': 'live' } }));
  if (s.due) out.push(el('span', { class: 'mark due', text: 'D', attrs: { title: s.due === 'overdue' ? `Overdue: ${t.due_date}` : `Due soon: ${t.due_date}`, 'aria-label': s.due === 'overdue' ? 'overdue' : 'due soon' } }));
  return out;
}

function sortTasks(list, status) {
  return [...list].sort((a, b) =>
    (status === 'in-progress' ? Number(b.live) - Number(a.live) : 0) ||
    (PRI[a.priority] ?? 9) - (PRI[b.priority] ?? 9) ||
    String(b.updated_at).localeCompare(String(a.updated_at)));
}

// The primary button opens (or focuses) a terminal — only ever because the user clicked it.
// Start (backlog/next), Resume (in progress/paused/review), Focus (open terminal), Reopen (done:
// status only, never a terminal); nothing for archived.
function actionLabel(t) {
  if (t.archived) return null;
  if (t.status === 'done') return 'Reopen';
  if (t.terminal_open) return 'Show terminal';
  return t.status === 'backlog' || t.status === 'next' ? 'Start in terminal' : 'Open terminal';
}

// Secondary, status-only actions. None of them opens a terminal.
function stageActions(t) {
  if (t.archived) return [];
  const a = [];
  switch (t.status) {
    case 'backlog': a.push(['next', 'Move to Next']); break;
    case 'next': a.push(['pause', 'Pause'], ['backlog', 'Back to backlog']); break;
    case 'in-progress': a.push(['pause', 'Pause'], ['review', 'Ready for review']); break;
    case 'paused': a.push(['unpause', 'Resume work']); break;
    case 'review': a.push(['approve', 'Approve'], ['pause', 'Pause']); break;
    default: break;
  }
  return a.filter(([act]) => !(t.kind === 'project_agent' && (act === 'approve' || act === 'review')));
}

const isAgent = t => t.kind === 'project_agent';

// Two independent facts: the task's stage (status) and whether a terminal or headless run is open.
function stateChips(t) {
  const out = [];
  if (t.terminal_open) out.push(el('span', { class: 'chip chip-terminal', text: '⌨ open in terminal', attrs: { title: 'A terminal session is open for this task' } }));
  if (t.headless_running) {
    const ri = runInfos.get(t.slug);
    const mins = ri?.elapsed_seconds != null ? ` · ${Math.floor(ri.elapsed_seconds / 60)}m` : '';
    out.push(el('span', { class: 'chip chip-headless', text: `⚙ headless run${mins}`, attrs: { title: 'Claude is working in the background. Open the task to see what it is doing.' } }));
  }
  return out;
}

// Approve, archive and "pause all" are easy to regret, so they wait a few seconds behind an Undo.
// The page shows the result straight away; the request is only sent when the time is up (or the page
// closes), and Undo simply cancels it.
const PENDING_MS = 6000;
const pending = new Map(); // key -> { path, body, timer, apply }

function overlayTask(t) {
  const e = pending.get(`task:${t.slug}`);
  return e?.apply ? e.apply(t) : t;
}
const effectiveTasks = () => tasks.map(overlayTask).filter(Boolean);
function renderAll() {
  renderBoard(); renderNeeds();
  if (view === 'projects') renderProjects();
  if (view === 'archive') renderArchive();
}

async function flushPending(key) {
  const e = pending.get(key);
  if (!e || e.sending) return;
  e.sending = true;
  clearTimeout(e.timer);
  try {
    const r = await api(e.path, 'POST', e.body);
    if (e.done) toast(r.output || e.done);
  } catch (err) {
    toast(`Could not ${e.verb}. ${say(err)}`, true);
  }
  await reload();
  pending.delete(key);
  renderAll();
}

function deferAction({ key, path, body, label, verb, apply, done }) {
  if (pending.has(key)) return;
  const entry = { path, body, verb, apply, done, timer: setTimeout(() => flushPending(key), PENDING_MS) };
  pending.set(key, entry);
  renderAll();
  toast(label, false, { ms: PENDING_MS, undo: () => { clearTimeout(entry.timer); pending.delete(key); renderAll(); } });
}

// Leaving the page must not lose (or silently drop) an approval that was waiting out its Undo window.
window.addEventListener('pagehide', () => {
  for (const [, e] of pending) {
    try { fetch(e.path, { method: 'POST', keepalive: true, headers: { 'content-type': 'application/json', 'x-focusflow-token': token || '' }, body: JSON.stringify(e.body) }); } catch { /* best effort */ }
  }
});

const VERBS = { approve: 'approve that', pause: 'pause that', next: 'move that to Next', review: 'send that to review', unpause: 'resume that', backlog: 'move that to the backlog', archive: 'archive that', unarchive: 'restore that' };

async function stageAction(slug, action, btn, opts = {}) {
  const t = taskOf(slug);
  const name = t?.name ?? slug;
  if (action === 'approve') {
    deferAction({
      key: `task:${slug}`, path: `/api/tasks/${encodeURIComponent(slug)}/action`, body: { action }, verb: VERBS.approve,
      label: `Approved "${name}".`, done: 'Approved.', apply: x => ({ ...x, status: 'done', status_changed_at: new Date().toISOString() }),
    });
    return;
  }
  if (btn) btn.disabled = true;
  try {
    const r = await api(`/api/tasks/${encodeURIComponent(slug)}/action`, 'POST', { action });
    toast(r.output || 'Done.');
    if (opts.land) landedSlug = slug;
  } catch (err) {
    toast(`Could not ${VERBS[action] ?? action}. ${say(err)}`, true);
  }
  await reload();
}

function card(t, opts = {}) {
  const meta = [el('span', { class: `chip pri-${t.priority}`, text: t.priority })];
  if (t.project && !opts.hideProject) meta.push(el('span', { class: 'chip', text: t.project }));
  if (!opts.hideStage) meta.push(el('span', { class: `chip stage stage-${t.status}`, text: `${STAGE_ICON[t.status] ?? ''} ${t.status}` }));
  meta.push(...stateChips(t));
  for (const tag of t.tags) meta.push(el('span', { class: 'chip', text: `#${tag}` }));
  if (t.archived) meta.push(el('span', { class: 'chip', text: 'archived' }));
  meta.push(...marks(t));
  const kids = [el('div', { class: 'card-title', text: t.name }), el('div', { class: 'card-meta' }, meta)];
  if (t.due_date) kids.push(el('div', { class: 'card-note', text: `due ${t.due_date}` }));
  if (t.waiting_on) kids.push(el('div', { class: 'card-note', text: `waiting: ${t.waiting_on}` }));
  const main = el('button', { class: 'card-main', attrs: { type: 'button', 'data-slug': t.slug, 'data-key': `open:${t.slug}` } }, kids);
  main.addEventListener('click', () => openDrawer(t.slug, main));
  const wrap = el('div', { class: 'card' + (t.archived ? ' archived' : '') + (landedSlug === t.slug ? ' landed' : ''), attrs: { 'data-card': t.slug } }, [main]);
  if (opts.drag && !t.archived && !isAgent(t)) {
    wrap.draggable = true;
    wrap.addEventListener('dragstart', e => { dragSlug = t.slug; wrap.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', t.slug); });
    wrap.addEventListener('dragend', () => { dragSlug = null; wrap.classList.remove('dragging'); clearDropMarks(); });
  }
  const btns = [];
  const acts = stageActions(t);
  const mk = (cls, text, fn, key) => {
    const b = el('button', { class: `card-btn ${cls}`.trim(), text, attrs: { type: 'button', 'aria-label': `${text}: ${t.name}`, 'data-key': `${key}:${t.slug}` } });
    b.addEventListener('click', () => fn(b));
    return b;
  };
  const label = actionLabel(t);
  const term = label ? mk(t.status === 'review' ? '' : '', label, b => cardAction(t.slug, b, label), 'term') : null;
  // On a Review card the main action is Approve, not opening a terminal.
  for (const [act, text] of acts) btns.push(mk(act === 'approve' ? 'primary' : '', text, b => stageAction(t.slug, act, b), act));
  if (term) { if (t.status === 'review') btns.push(term); else btns.unshift(term); }
  if (btns.length) wrap.append(el('div', { class: 'card-actions' }, btns));
  return wrap;
}

const boardTasks = () => effectiveTasks().filter(t => !t.archived && !isAgent(t) && (t.kind ?? 'regular') === 'regular');

function isRecentDone(t) {
  const at = Date.parse(t.status_changed_at || t.updated_at);
  return Number.isNaN(at) || Date.now() - at < DONE_RECENT_DAYS * 86400000;
}

function renderBoard(force = false) {
  const board = $('board');
  const visible = Logic.filterTasks(boardTasks(), filters);
  const fp = Logic.fingerprint([visible, filters, landedSlug, [...runInfos].map(([k, v]) => [k, v.elapsed_seconds != null ? Math.floor(v.elapsed_seconds / 60) : null])]);
  if (!force && board.dataset.fp === fp) return;
  board.dataset.fp = fp;
  keepFocus(() => {
    board.replaceChildren();
    for (const col of COLUMNS) {
      const all = visible.filter(t => t.status === col.status);
      const items = sortTasks(col.status === 'done' ? all.filter(isRecentDone) : all, col.status);
      const body = items.length ? items.map(t => card(t, { hideStage: true, drag: true })) : [el('div', { class: 'empty', text: STAGE_HINT[col.status] ?? 'Nothing here.' })];
      const sec = el('section', { class: `col col-${col.status}`, attrs: { 'data-status': col.status } }, [
        el('h2', {}, [el('span', { text: `${STAGE_ICON[col.status] ?? ''} ${col.title}` }), el('span', { text: String(items.length) })]),
        el('div', { class: 'cards' }, body),
      ]);
      wireDrop(sec, col.status);
      board.append(sec);
    }
    const backlog = visible.filter(t => t.status === 'backlog').length;
    if (backlog) board.append(el('div', { class: 'board-note muted', text: `${backlog} task${backlog === 1 ? '' : 's'} in the backlog — see Projects.` }));
  });
  landedSlug = null;
}

// Redraws replace the DOM; keep keyboard focus on the same control afterwards.
function keepFocus(draw) {
  const key = document.activeElement?.dataset?.key;
  draw();
  if (key) {
    const again = [...document.querySelectorAll('[data-key]')].find(n => n.dataset.key === key);
    if (again) again.focus({ preventScroll: true });
  }
}

function clearDropMarks() { for (const c of document.querySelectorAll('.col')) c.classList.remove('drop-ok', 'drop-no'); }
function wireDrop(sec, status) {
  sec.addEventListener('dragover', e => {
    const t = taskOf(dragSlug);
    if (!t) return;
    const ok = !!Logic.dropAction(t, status).action;
    sec.classList.toggle('drop-ok', ok); sec.classList.toggle('drop-no', !ok);
    if (ok) { e.preventDefault(); e.dataTransfer.dropEffect = 'move'; }
  });
  sec.addEventListener('dragleave', e => { if (!sec.contains(e.relatedTarget)) sec.classList.remove('drop-ok', 'drop-no'); });
  sec.addEventListener('drop', e => {
    e.preventDefault();
    clearDropMarks();
    const t = taskOf(dragSlug);
    dragSlug = null;
    if (!t) return;
    const { action, hint } = Logic.dropAction(t, status);
    if (!action) { if (hint) toast(hint); return; }
    stageAction(t.slug, action, null, { land: true });
  });
}

async function loadTasks() {
  const j = await api('/api/tasks');
  tasks = j.tasks;
  fillFilterProjects();
  renderBoard();
}

function setView(name) {
  const next = VIEWS.includes(name) ? name : 'needs';
  if (view === 'inbox' && next !== 'inbox' && chatHome === 'inbox') { openSlug = null; inboxSel = null; resetChat(); }
  view = next;
  try { localStorage.setItem('focusflow-view', view); } catch { /* remembering is optional */ }
  for (const v of VIEWS) $(v === 'board' ? 'board-wrap' : v).hidden = view !== v;
  for (const b of document.querySelectorAll('#tabs .tab')) b.setAttribute('aria-selected', String(b.dataset.view === view));
  loadViewData().catch(reportError);
}


// ---- Needs you ----
const KIND_LABEL = { review: 'Ready for review', question: 'Question for you', failed: 'Run stopped', overdue: 'Overdue', waiting: 'Waiting' };

async function loadNeeds() {
  needs = await api('/api/needs-you');
  const n = needs.badge;
  $('needs-badge').hidden = n === 0;
  $('needs-badge').textContent = String(n);
  renderBadge();
}

async function loadKnownProjects() {
  try { knownProjects = (await api('/api/projects')).projects.map(p => p.slug); } catch { /* keep the last list */ }
}

async function loadRuns() {
  const running = tasks.filter(t => t.headless_running || t.auto_run_status === 'dead').map(t => t.slug);
  const results = await Promise.all(running.map(slug => api(`/api/tasks/${encodeURIComponent(slug)}/run`).then(r => [slug, r]).catch(() => null)));
  runInfos.clear();
  for (const r of results) if (r) runInfos.set(r[0], r[1]);
}

function needsItem(i) {
  const t = taskOf(i.slug);
  const acts = [];
  const btn = (text, fn, cls = '') => { const b = el('button', { class: `card-btn ${cls}`.trim(), text, attrs: { type: 'button', 'aria-label': `${text}: ${i.name}`, 'data-key': `need:${i.kind}:${i.slug}:${text}` } }); b.addEventListener('click', () => fn(b)); return b; };
  if (i.kind === 'review' && t && !isAgent(t)) acts.push(btn('Approve', () => stageAction(i.slug, 'approve'), 'primary'));
  acts.push(btn(i.kind === 'question' ? 'Reply' : 'Open', b => openDrawer(i.slug, b), i.kind === 'question' ? 'primary' : ''));
  const kind = KIND_LABEL[i.kind] + (i.urgent ? ' · urgent' : '');
  return el('div', { class: `need k-${i.kind}${i.urgent ? ' urgent' : ''}` }, [
    el('div', { class: 'need-main' }, [
      el('div', { class: 'need-kind', text: `${kind}${i.project ? ` · ${i.project}` : ''}${i.at ? ` · ${Logic.humanTime(i.at)}` : ''}` }),
      el('div', { class: 'need-title', text: i.agent ? `${i.name} (project coordinator)` : i.name }),
      el('div', { class: 'need-detail', text: i.detail }),
    ]),
    el('div', { class: 'need-actions' }, acts),
  ]);
}

function runRow(t) {
  const ri = runInfos.get(t.slug);
  const dead = t.auto_run_status === 'dead';
  const cancel = el('button', { class: 'card-btn', text: 'Cancel run', attrs: { type: 'button', 'aria-label': `Cancel run: ${t.name}` } });
  cancel.addEventListener('click', () => { cancel.disabled = true; stageAction(t.slug, 'pause'); });
  const open = el('button', { class: 'card-btn', text: 'Watch', attrs: { type: 'button', 'aria-label': `Watch: ${t.name}` } });
  open.addEventListener('click', () => openDrawer(t.slug, open));
  const doing = ri?.activity?.summary ?? (dead ? 'Stopped unexpectedly.' : 'Working…');
  return el('div', { class: 'run-row' }, [
    el('span', { class: 'chip chip-headless', text: `⚙ ${ri?.elapsed_seconds != null ? Logic.duration(ri.elapsed_seconds) : 'running'}` }),
    el('div', { class: 'grow' }, [el('div', { class: 'need-title', text: t.name }), el('div', { class: 'need-detail', text: doing })]),
    open, ...(dead ? [] : [cancel]),
  ]);
}

function renderNeeds(force = false) {
  const box = $('needs');
  const running = effectiveTasks().filter(t => t.headless_running && !isAgent(t));
  const items = needs.items.filter(i => !pending.has(`task:${i.slug}`));
  const fp = Logic.fingerprint([items, running.map(t => t.slug), [...runInfos].map(([k, v]) => [k, v.activity?.summary, Math.floor((v.elapsed_seconds ?? 0) / 10)]), tasks.length]);
  if (!force && box.dataset.fp === fp) return;
  box.dataset.fp = fp;
  keepFocus(() => {
    box.replaceChildren();
    if (!tasks.length && !knownProjects.length) { box.append(firstRun()); return; }
    const c = needs.counts;
    box.append(el('div', { class: 'needs-counts' }, [
      ['review', 'to review'], ['question', 'questions'], ['failed', 'failed runs'], ['overdue', 'overdue'], ['waiting', 'waiting'],
    ].filter(([k]) => c[k]).map(([k, label]) => el('span', { class: 'needs-count' }, [el('b', { text: String(c[k]) }), el('span', { class: 'muted', text: ` ${label}` })]))));
    if (!items.length) {
      box.append(el('div', { class: 'empty-hero' }, [
        el('h2', { text: 'Nothing needs you right now' }),
        el('div', { class: 'muted', text: running.length ? 'Work is running in the background. You will get a notification when it needs you.' : 'Capture something, or open the Board to pick up the next task.' }),
      ]));
    } else {
      for (const i of items) box.append(needsItem(i));
    }
    if (running.length) {
      box.append(el('h2', { class: 'needs-h', text: 'Running without you' }));
      for (const t of running) box.append(runRow(t));
    }
  });
}

function firstRun() {
  const b = (text, fn, cls = '') => { const x = el('button', { class: cls, text, attrs: { type: 'button' } }); x.addEventListener('click', fn); return x; };
  return el('div', { class: 'empty-hero' }, [
    el('h2', { text: 'Welcome to Focusflow' }),
    el('div', { class: 'muted', text: 'Hand work to Claude sessions, and step in only where you are needed.' }),
    el('ol', {}, [
      el('li', { text: 'Create a project for what you are working on.' }),
      el('li', { text: 'Capture tasks into it. Move the ones you will do soon to Next.' }),
      el('li', { text: 'Send a message to start a task; it runs in the background. Open a terminal only when you want to watch.' }),
      el('li', { text: 'When work is ready, it shows up here to approve.' }),
    ]),
    el('div', { class: 'need-actions', attrs: { style: 'justify-content:center' } }, [
      b('Create a project', () => { setView('projects'); openNewProject(); }, 'primary'),
      b('Capture a task', () => openCapture()),
      b('How it works', () => $('legend').showModal()),
    ]),
  ]);
}

// ---- projects ----
const saveExpanded = () => { try { localStorage.setItem('focusflow-expanded', JSON.stringify([...expanded])); } catch { /* optional */ } };

const STAGE_ORDER = ['in-progress', 'review', 'paused', 'next', 'backlog', 'done'];
function rollup(counts) {
  const parts = STAGE_ORDER.filter(s => counts[s]).map(s => `${counts[s]} ${s}`);
  return parts.length ? parts.join(' · ') : 'no tasks yet';
}

async function projectAction(slug, action, extra = {}) {
  // Archiving a project and pausing all of it wait behind an Undo; the rest act at once.
  if (action === 'archive' || (action === 'pause' && !extra.now)) {
    const p = overview.projects.find(x => x.slug === slug);
    const name = p?.name ?? slug;
    deferAction({
      key: `project:${slug}:${action}`, path: `/api/projects/${encodeURIComponent(slug)}/action`, body: { action, ...extra },
      verb: action === 'archive' ? 'archive that project' : 'pause that project',
      label: action === 'archive' ? `Archived project "${name}".` : `Pausing everything in "${name}".`,
      done: action === 'archive' ? 'Archived.' : 'Paused.',
    });
    return;
  }
  try {
    const r = await api(`/api/projects/${encodeURIComponent(slug)}/action`, 'POST', { action, ...extra });
    toast(r.output || 'Done.');
  } catch (err) {
    toast(`Could not ${action} that project. ${say(err)}`, true);
  }
  await reload();
}

// A project-level message opens the coordinator's chat: one permanent session per project.
async function messageProject(slug) {
  try {
    const r = await api(`/api/projects/${encodeURIComponent(slug)}/action`, 'POST', { action: 'ensure-agent' });
    await reload();
    openDrawer(r.agent);
  } catch (err) {
    toast(`Could not open the project chat. ${say(err)}`, true);
  }
}

function addSubtaskForm(p) {
  const input = el('input', { class: 'inline-input', attrs: { type: 'text', maxlength: '200', placeholder: 'Add a subtask (stays in the backlog)…', 'aria-label': `New subtask in ${p.name}` } });
  input.value = drafts.get(p.slug) ?? '';
  input.addEventListener('input', () => drafts.set(p.slug, input.value));
  const go = el('button', { text: 'Add', attrs: { type: 'submit' } });
  const form = el('form', { class: 'inline-form' }, [input, go]);
  form.addEventListener('submit', async e => {
    e.preventDefault();
    const name = input.value.trim();
    if (!name) return;
    go.disabled = true;
    try {
      const r = await api(`/api/projects/${encodeURIComponent(p.slug)}/task`, 'POST', { name });
      toast(`Added "${r.slug}" to ${p.name} (backlog).`);
      input.value = '';
      drafts.delete(p.slug);
    } catch (err) {
      toast(`Could not add the subtask. ${say(err)}`, true);
    }
    go.disabled = false;
    await reload();
  });
  return form;
}

// "Schedule…" reveals a cadence picker; a scheduled coordinator reviews the project on that rhythm.
function scheduleControl(p) {
  const wrap = el('span', { class: 'sched' });
  const open = el('button', { class: 'card-btn', text: 'Schedule…', attrs: { type: 'button' } });
  const sel = el('select', { class: 'inline-select', attrs: { 'aria-label': `How often should ${p.name} be reviewed?` }, }, ['12h', '1d', '3d', '1w'].map(v => el('option', { text: `every ${v}`, attrs: { value: v } })));
  sel.value = '1d';
  const set = el('button', { class: 'card-btn primary', text: 'Set', attrs: { type: 'button' } });
  const cancel = el('button', { class: 'card-btn', text: '×', attrs: { type: 'button', 'aria-label': 'Cancel' } });
  const picker = el('span', { class: 'sched-picker', }, [sel, set, cancel]);
  picker.hidden = !openSchedulers.has(p.slug);
  open.hidden = !picker.hidden;
  open.addEventListener('click', () => { openSchedulers.add(p.slug); open.hidden = true; picker.hidden = false; sel.focus(); });
  cancel.addEventListener('click', () => { openSchedulers.delete(p.slug); picker.hidden = true; open.hidden = false; });
  set.addEventListener('click', () => { set.disabled = true; openSchedulers.delete(p.slug); projectAction(p.slug, 'schedule', { every: sel.value }); });
  wrap.append(open, picker);
  return wrap;
}

function projectRow(p) {
  const open = expanded.has(p.slug);
  const head = el('button', { class: 'proj-head', attrs: { type: 'button', 'aria-expanded': String(open) } }, [
    el('span', { class: 'proj-caret', text: open ? '▾' : '▸' }),
    el('span', { class: 'proj-name', text: p.name }),
    el('span', { class: 'muted mono', text: p.slug }),
    el('span', { class: 'muted proj-rollup', text: rollup(p.counts) }),
    ...(p.agent?.terminal_open ? [el('span', { class: 'chip chip-terminal', text: '⌨ coordinator open' })] : []),
    ...(p.agent?.headless_running ? [el('span', { class: 'chip chip-headless', text: '⚙ coordinator running' })] : []),
    ...(p.schedule ? [el('span', { class: 'chip', text: `⏱ every ${p.schedule.every}${p.schedule.status === 'paused' ? ' (paused)' : ''}` })] : []),
  ]);
  head.addEventListener('click', () => {
    if (expanded.has(p.slug)) expanded.delete(p.slug); else expanded.add(p.slug);
    saveExpanded();
    renderProjects();
  });
  const btn = (text, fn, cls = '') => { const b = el('button', { class: `card-btn ${cls}`.trim(), text, attrs: { type: 'button' } }); b.addEventListener('click', () => { b.disabled = true; fn(); }); return b; };
  const acts = el('div', { class: 'proj-actions' }, [
    btn('Message', () => messageProject(p.slug)),
    btn('Open terminal', () => projectAction(p.slug, 'open-agent')),
    btn('Pause all', () => projectAction(p.slug, 'pause')),
    p.schedule ? btn('Unschedule', () => projectAction(p.slug, 'unschedule')) : scheduleControl(p),
    btn('Archive', () => projectAction(p.slug, 'archive')),
  ]);
  const kids = [el('div', { class: 'proj-top' }, [head]), acts];
  if (open) {
    kids.push(el('div', { class: 'proj-body' }, [
      addSubtaskForm(p),
      el('div', { class: 'cards' }, p.tasks.length ? p.tasks.map(t => card(t, { hideProject: true })) : [el('div', { class: 'empty', text: 'No subtasks yet.' })]),
    ]));
  }
  return el('section', { class: 'proj' + (open ? ' open' : '') }, kids);
}

function renderProjects(force = false) {
  const box = $('projects');
  const view2 = {
    projects: overview.projects.filter(p => !pending.has(`project:${p.slug}:archive`)).map(p => ({ ...p, tasks: p.tasks.map(overlayTask).filter(Boolean) })),
    floating: overview.floating.map(overlayTask).filter(Boolean),
  };
  const fp = Logic.fingerprint([view2, [...expanded], [...openSchedulers], [...runInfos].map(([k, v]) => [k, Math.floor((v.elapsed_seconds ?? 0) / 60)])]);
  if (!force && box.dataset.fp === fp) return;
  // Someone typing in a subtask box or picking a schedule must not lose it to a background refresh.
  if (!force && box.contains(document.activeElement) && Logic.isEditing(document.activeElement)) { dirty.add('projects'); return; }
  box.dataset.fp = fp;
  dirty.delete('projects');
  keepFocus(() => {
    box.replaceChildren();
    const bar = el('div', { class: 'view-bar' }, [el('button', { class: 'primary', text: '+ New project', attrs: { type: 'button', id: 'new-project-btn' } })]);
    bar.firstChild.addEventListener('click', openNewProject);
    box.append(bar);
    if (!view2.projects.length && !view2.floating.length) {
      box.append(el('div', { class: 'empty', text: 'No projects yet. Create one to group tasks under it.' }));
    }
    for (const p of view2.projects) box.append(projectRow(p));
    if (view2.floating.length) {
      box.append(el('section', { class: 'proj open' }, [
        el('div', { class: 'proj-top' }, [el('span', { class: 'proj-name', text: 'No project' }), el('span', { class: 'muted', text: `${view2.floating.length} task${view2.floating.length === 1 ? '' : 's'}` })]),
        el('div', { class: 'proj-body' }, [el('div', { class: 'cards' }, view2.floating.map(t => card(t)))]),
      ]));
    }
  });
}
$('projects').addEventListener('focusout', () => { setTimeout(() => { if (dirty.has('projects') && !Logic.isEditing(document.activeElement)) renderProjects(); }, 0); });

// ---- archive ----
function renderArchive() {
  const box = $('archive');
  box.replaceChildren();
  const unarchiveProject = async slug => { await projectAction(slug, 'unarchive', { now: true }); };
  box.append(el('h2', { class: 'arch-h', text: `Archived projects (${archiveData.projects.length})` }));
  if (!archiveData.projects.length) box.append(el('div', { class: 'empty', text: 'None.' }));
  for (const p of archiveData.projects) {
    const b = el('button', { class: 'card-btn', text: 'Unarchive', attrs: { type: 'button' } });
    b.addEventListener('click', () => { b.disabled = true; unarchiveProject(p.slug); });
    box.append(el('section', { class: 'proj open archived' }, [
      el('div', { class: 'proj-top' }, [el('span', { class: 'proj-name', text: p.name }), el('span', { class: 'muted mono', text: p.slug }), el('span', { class: 'muted', text: `${p.tasks.length} task${p.tasks.length === 1 ? '' : 's'}` }), b]),
      ...(p.tasks.length ? [el('div', { class: 'proj-body' }, [el('div', { class: 'cards' }, p.tasks.map(card))])] : []),
    ]));
  }
  box.append(el('h2', { class: 'arch-h', text: `Archived tasks (${archiveData.tasks.length})` }));
  if (!archiveData.tasks.length) box.append(el('div', { class: 'empty', text: 'None.' }));
  const cards = archiveData.tasks.map(t => {
    const c = card(t);
    const b = el('button', { class: 'card-btn', text: 'Unarchive', attrs: { type: 'button', 'aria-label': `Unarchive: ${t.name}` } });
    b.addEventListener('click', () => { b.disabled = true; stageAction(t.slug, 'unarchive'); });
    c.append(el('div', { class: 'card-actions' }, [b]));
    return c;
  });
  if (cards.length) box.append(el('div', { class: 'cards arch-cards' }, cards));
}

async function loadViewData() {
  if (view === 'projects') { overview = await api('/api/projects/overview'); renderProjects(); }
  if (view === 'archive') { archiveData = await api('/api/archive'); renderArchive(); }
  if (view === 'needs') renderNeeds();
}

// ---- new project ----
const npDlg = $('newproject');
const npError = msg => { $('np-error').hidden = !msg; $('np-error').textContent = msg || ''; };
function openNewProject() {
  if (npDlg.open) return;
  $('np-form').reset();
  npError('');
  npDlg.showModal();
  $('np-name').focus();
}
// "Choose folder…": the native macOS chooser (it has a New Folder button), opened by the server on this
// machine. Cancelling leaves the field as it was.
async function chooseFolder(btn) {
  const input = $(btn.dataset.target);
  const showError = btn.dataset.target === 'np-dir' ? npError : capError;
  const label = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Waiting for the dialog…';
  showError('');
  try {
    const r = await api('/api/pick-folder', 'POST', { start: input.value.trim() || undefined, prompt: 'Choose or create the working folder' });
    if (r.path) { input.value = r.path; input.dispatchEvent(new Event('input', { bubbles: true })); }
  } catch (err) {
    showError(`Could not open the folder dialog. ${say(err)}`);
  } finally {
    btn.disabled = false;
    btn.textContent = label;
  }
}
for (const b of document.querySelectorAll('.pick-btn')) b.addEventListener('click', () => chooseFolder(b));

$('np-cancel').addEventListener('click', () => npDlg.close());
$('np-form').addEventListener('submit', async e => {
  e.preventDefault();
  const name = $('np-name').value.trim();
  const dir = $('np-dir').value.trim();
  if (!name) { npError('Give the project a name.'); return; }
  if (name.startsWith('-')) { npError('The name must not start with "-".'); return; }
  if (!dir.startsWith('/')) { npError('The work directory must be an absolute path (start with "/").'); return; }
  $('np-save').disabled = true;
  try {
    const r = await api('/api/projects/new', 'POST', { name, work_dir: dir, priority: $('np-priority').value });
    npDlg.close();
    expanded.add(r.slug);
    saveExpanded();
    toast(`Created project "${r.slug}". Add subtasks below — nothing starts until you say so.`);
    await reload();
  } catch (err) {
    npError(say(err));
  } finally {
    $('np-save').disabled = false;
  }
});

// A short tone (Web Audio). Browsers may block it until the first click on the page.
function primeAudio() {
  try {
    const Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx) return;
    audio = audio || new Ctx();
    if (audio.state === 'suspended') audio.resume();
  } catch { /* sound is best effort */ }
}

function beep() {
  try {
    if (!audio) return;
    const o = audio.createOscillator();
    const g = audio.createGain();
    o.frequency.value = 880;
    g.gain.setValueAtTime(0.08, audio.currentTime);
    g.gain.exponentialRampToValueAtTime(0.0001, audio.currentTime + 0.25);
    o.connect(g);
    g.connect(audio.destination);
    o.start();
    o.stop(audio.currentTime + 0.26);
  } catch { /* sound is best effort */ }
}

// Badge and tab title follow the unread count; the tone plays when it rises (never on first load).
function renderBadge() {
  const n = inbox.unread_total;
  $('inbox-badge').hidden = n === 0;
  $('inbox-badge').textContent = String(n);
  const title = needs.badge || n;
  document.title = title ? `(${title}) Focusflow` : 'Focusflow';
  if (soundOn && lastUnread !== null && n > lastUnread) beep();
  lastUnread = n;
}

function renderInbox(force = false) {
  const box = $('inbox-list');
  const fp = Logic.fingerprint([inbox, inboxSel]);
  if (!force && box.dataset.fp === fp) return;
  box.dataset.fp = fp;
  keepFocus(() => {
    box.replaceChildren();
    if (!inbox.threads.length) {
      box.append(el('div', { class: 'empty', text: 'No messages yet. Open a task and send one.' }));
      return;
    }
    const readAll = el('button', { class: 'card-btn', text: 'Mark all read', attrs: { type: 'button' } });
    readAll.disabled = inbox.unread_total === 0;
    readAll.addEventListener('click', markAllRead);
    box.append(el('div', { class: 'inbox-tools' }, [el('span', { class: 'muted', text: `${inbox.threads.length} conversation${inbox.threads.length === 1 ? '' : 's'}` }), readAll]));
    for (const th of inbox.threads) {
      const urgent = th.last.urgent && th.last.direction === 'in';
      const cur = inboxSel === th.slug;
      const b = el('button', { class: 'thread-row' + (th.unread ? ' unread' : '') + (urgent ? ' urgent' : ''), attrs: { type: 'button', 'data-slug': th.slug, 'data-key': `thread:${th.slug}`, ...(cur ? { 'aria-current': 'true' } : {}) } }, [
        el('div', { class: 'thread-top' }, [
          el('span', { class: 'thread-name', text: th.name }),
          urgent ? el('span', { class: 'chip urgent', text: 'urgent' }) : null,
          th.live ? el('span', { class: 'mark live', text: '*', attrs: { title: 'Session is live', 'aria-label': 'live' } }) : null,
          th.unread ? el('span', { class: 'badge', text: String(th.unread), attrs: { 'aria-label': `${th.unread} unread` } }) : null,
          el('span', { class: 'muted thread-time', text: ago(th.last.at) }),
        ]),
        el('div', { class: 'card-meta' }, [
          ...(th.project ? [el('span', { class: 'chip', text: th.project })] : []),
          el('span', { class: `chip stage stage-${th.status}`, text: `${STAGE_ICON[th.status] ?? ''} ${th.status}` }),
        ]),
        el('div', { class: 'thread-preview', text: `${th.last.direction === 'out' ? 'You: ' : ''}${th.last.preview}` }),
      ]);
      b.addEventListener('click', () => selectThread(th.slug));
      box.append(b);
    }
  });
}

async function markAllRead() {
  await Promise.all(inbox.threads.filter(t => t.unread).map(t => api(`/api/tasks/${encodeURIComponent(t.slug)}/read`, 'POST', {}).catch(() => null)));
  await loadInbox();
}

// The inbox pane and the drawer share one chat element, so sending, receipts and read-marking behave
// the same in both places.
function moveChat(home) {
  chatHome = home;
  const chat = $('chat');
  if (home === 'inbox') {
    $('inbox-pane').append(chat);
    $('inbox-empty').hidden = true;
    chat.hidden = false;
  } else {
    $('drawer-body').prepend(chat);
    showTab(activeTab);
    $('inbox-empty').hidden = false;
    $('chat-title').hidden = true;
  }
}

async function selectThread(slug) {
  if (openSlug !== slug) resetChat();
  openSlug = slug;
  inboxSel = slug;
  if (!$('drawer').hidden) closeDrawer(true);
  moveChat('inbox');
  renderInbox(true);
  try {
    await refreshThread();
    await markReadNow();
  } catch (err) { chatError(say(err)); }
}

function chatVisible() {
  return chatHome === 'inbox' ? view === 'inbox' && !!inboxSel : !$('drawer').hidden;
}

async function loadInbox() {
  inbox = await api('/api/inbox');
  renderBadge();
  renderInbox();
}

// ---- headless run panel (drawer) ----
async function refreshRun() {
  const slug = openSlug;
  const box = $('drawer-run');
  if (!slug || $('drawer').hidden) { box.hidden = true; return; }
  const t = taskOf(slug);
  if (!t || !(t.headless_running || t.auto_run_status === 'dead')) { box.hidden = true; return; }
  let r;
  try { r = await api(`/api/tasks/${encodeURIComponent(slug)}/run`); } catch { return; }
  if (slug !== openSlug) return;
  runInfos.set(slug, r);
  const failed = !r.running && r.status === 'dead';
  box.hidden = !(r.running || failed);
  box.classList.toggle('failed', failed);
  const head = el('div', { class: 'run-head' }, [
    el('span', { class: 'grow', text: failed ? 'The headless run stopped unexpectedly' : `⚙ Running in the background${r.elapsed_seconds != null ? ` · ${Logic.duration(r.elapsed_seconds)}` : ''}` }),
  ]);
  if (r.running) {
    const cancel = el('button', { class: 'card-btn', text: 'Cancel run', attrs: { type: 'button' } });
    cancel.addEventListener('click', async () => { cancel.disabled = true; await stageAction(slug, 'pause'); });
    head.append(cancel);
  }
  const kids = [head];
  if (r.activity) kids.push(el('div', { class: 'run-activity', text: `${r.running ? 'Now' : 'Last step'}: ${r.activity.summary}${r.activity.at ? ` (${Logic.humanTime(r.activity.at)})` : ''}` }));
  else if (r.running) kids.push(el('div', { class: 'muted', text: 'Starting up…' }));
  if (r.log_tail) {
    const d = el('details', { class: 'run-log fold' }, [el('summary', { text: 'Show log' }), el('pre', { text: r.log_tail })]);
    d.open = box.querySelector('details')?.open ?? false;
    kids.push(d);
  }
  box.replaceChildren(...kids);
}

async function loadHealth() {
  const h = await api('/api/health');
  const foot = $('foot');
  foot.replaceChildren();
  const add = (text, bad) => foot.append(el('span', { class: bad ? 'bad' : '', text }));
  add(`${h.task_count} tasks`);
  add(h.cache_age_seconds == null ? 'not read yet' : `Updated ${ago(h.last_tick_at)}`);
  if (h.sync?.status === 'error') add(`sync: ${h.sync.last_error || 'error'}`, true);
  if (h.mirror_error) add(`mirror: ${h.mirror_error}`, true);
  if (h.malformed_count) add(`${h.malformed_count} malformed event line(s)`, true);
  if (h.last_error) add(`last read failed: ${h.last_error}`, true);
}

// ---- stats strip, health chip, search ----
const fmtTokens = n => (n >= 1e9 ? `${(n / 1e9).toFixed(1)}B` : n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${Math.round(n / 1e3)}k` : String(n));

async function loadStats() {
  try {
    const st = await api('/api/stats?since=30d');
    const box = $('stats');
    box.replaceChildren();
    const chip = (label, value, title) => box.append(el('span', { class: 'stat', attrs: title ? { title } : {} }, [el('b', { text: String(value) }), el('span', { class: 'muted', text: ` ${label}` })]));
    chip('in progress', st.tasks['in-progress'] ?? 0);
    chip('paused', st.tasks.paused ?? 0);
    chip('in review', st.tasks.review ?? 0);
    chip('done (30d)', st.done_in_window ?? 0);
    if (st.sessions) chip('sessions (30d)', st.sessions);
    if (st.tokens?.output) chip('output tokens', fmtTokens(st.tokens.output), 'Across the sessions active in the last 30 days');
    if (st.bus?.answered) chip('median reply', Logic.duration(st.bus.median_wait_seconds), `${st.bus.answered} answered, ${st.bus.pending} pending`);
    box.hidden = false;
  } catch { $('stats').hidden = true; } // the strip is a nicety: never a banner
}

let doctorState = null;
async function loadDoctor() {
  try { doctorState = await api('/api/doctor'); } catch { doctorState = null; }
  renderHealthChip();
}
function renderHealthChip() {
  let chip = document.getElementById('health-chip');
  if (!chip) { chip = el('button', { class: 'health-chip', attrs: { id: 'health-chip', type: 'button' } }); $('foot').append(chip); chip.addEventListener('click', () => {
    if (!doctorState) return;
    toast(doctorState.problems.length ? doctorState.problems.map(p => `${p.level === 'fail' ? '✗' : '!'} ${p.check}: ${p.detail || ''}${p.fix ? `  → ${p.fix}` : ''}`).join('\n') : 'Everything looks healthy.', !!doctorState.problems.length);
  }); }
  if (!doctorState) { chip.textContent = 'health: unknown'; chip.className = 'health-chip'; return; }
  chip.className = 'health-chip ' + (doctorState.fail ? 'bad' : doctorState.warn ? 'warn' : 'good');
  chip.textContent = doctorState.fail ? `health: ${doctorState.fail} failing` : doctorState.warn ? `health: ${doctorState.warn} warning${doctorState.warn === 1 ? '' : 's'}` : 'health: ok';
}

let searchTimer = null;
const searchBox = () => $('search-results');
function closeSearch() { searchBox().hidden = true; searchBox().replaceChildren(); }
async function runSearch(q) {
  try {
    const { hits } = await api(`/api/search?q=${encodeURIComponent(q)}`);
    const box = searchBox();
    box.replaceChildren();
    if (!hits.length) box.append(el('div', { class: 'empty', text: 'No matches.' }));
    for (const h of hits) {
      const b = el('button', { class: 'search-hit', attrs: { type: 'button' } }, [
        el('div', { class: 'muted mono', text: `${h.scope} · ${h.kind}${h.line ? `:${h.line}` : ''}` }),
        el('div', { text: h.text }),
      ]);
      b.addEventListener('click', () => {
        closeSearch();
        if (h.scope.startsWith('task:')) openDrawer(h.scope.slice(5));
        else if (h.scope.startsWith('project:')) { expanded.add(h.scope.slice(8)); saveExpanded(); setView('projects'); }
        else toast(`${h.file}${h.line ? `:${h.line}` : ''}\n${h.text}`);
      });
      box.append(b);
    }
    box.hidden = false;
  } catch (err) { toast(`Search failed. ${say(err)}`, true); }
}

async function reload() {
  try {
    await loadTasks();
    await Promise.all([loadHealth(), loadInbox(), loadViewData(), loadNeeds(), loadRuns(), loadKnownProjects()]);
    renderAll();
    loadStats(); loadDoctor();
    showBanner('');
    if (openSlug && !$('drawer').hidden) await fillDrawer(openSlug);
  } catch (err) {
    reportError(err);
  }
}

function briefNode(text) {
  const box = el('div', { class: 'brief' });
  for (const line of text.split('\n')) {
    const h = /^#{1,6}\s+(.*)$/.exec(line);
    box.append(el('div', { class: h ? 'h' : '', text: h ? h[1] : line || ' ' }));
  }
  return box;
}

function sect(title, ...kids) {
  return el('div', { class: 'sect' }, [el('h3', { text: title }), ...kids]);
}

const STAGE_NAME = { backlog: 'In the backlog', next: 'Next up', 'in-progress': 'In progress', paused: 'Paused', review: 'In review', done: 'Done' };

async function fillDrawer(slug) {
  const d = await api(`/api/tasks/${encodeURIComponent(slug)}`);
  const t = d.task;
  $('drawer-title').textContent = t.name;
  $('drawer-slug').textContent = t.slug;
  $('drawer-summary').textContent = [STAGE_NAME[t.status] ?? t.status, `${t.priority} priority`, t.updated_at ? `updated ${ago(t.updated_at)}` : null].filter(Boolean).join(' · ') + (t.archived ? ' · archived' : '');
  setAction(t);

  // Only rows that have something to say.
  const kv = el('dl', { class: 'kv' });
  const row = (k, v) => { if (v != null && v !== '') kv.append(el('dt', { text: k }), el('dd', { text: v })); };
  row('Stage', STAGE_NAME[t.status] ?? t.status);
  row('Priority', t.priority);
  if (isAgent(t)) row('Role', 'Project coordinator (one permanent session)');
  row('Project', t.project);
  row('Tags', t.tags.length ? t.tags.map(x => `#${x}`).join(' ') : null);
  row('Due', t.due_date);
  row('Waiting on', t.waiting_on);
  row('Assignee', t.assignee);
  row('Terminal', t.terminal_open ? 'Open' : t.headless_running ? 'None (running in the background)' : null);
  row('Updated', t.updated_at ? `${Logic.humanTime(t.updated_at)} (${fmtTime(t.updated_at)})` : null);

  const s = d.session;
  const skv = el('dl', { class: 'kv' });
  const srow = (k, v) => { if (v != null && v !== '') skv.append(el('dt', { text: k }), el('dd', { text: v })); };
  srow('Session', s.has_session ? (s.live ? 'Live' : 'Exists, not running') : 'None yet');
  srow('Runs in', s.harness);
  srow('Started', s.started ? Logic.humanTime(s.started) : null);
  srow('Last resumed', s.last_resumed ? Logic.humanTime(s.last_resumed) : null);
  srow('Headless run', s.auto_run_status);

  const sig = [];
  const g = t.signals;
  if (g.stale) sig.push(['stale', '!', 'Stale: no activity for a while']);
  if (g.waiting) sig.push(['waiting', 'W', 'Waiting on someone']);
  if (g.live) sig.push(['live', '*', 'Session is live']);
  if (g.due) sig.push(['due', 'D', g.due === 'overdue' ? 'Overdue' : 'Due soon']);
  const details = [sect('Details', kv)];
  if (sig.length) details.push(sect('Alerts', el('div', { class: 'signals' }, sig.map(([cls, m, label]) => el('span', { class: 'signal' }, [el('span', { class: `mark ${cls}`, text: m }), el('span', { text: label })])))));
  const fold = el('details', { class: 'fold' }, [el('summary', { text: 'Session' }), skv]);
  fold.open = $('drawer-details').querySelector('details')?.open ?? false;
  details.push(el('div', { class: 'sect' }, [fold]));
  $('drawer-details').replaceChildren(...details);

  $('drawer-brief').replaceChildren(d.brief == null ? el('div', { class: 'muted', text: 'No brief.' }) : briefNode(d.brief));
  $('notes-count').textContent = d.notes.length ? `(${d.notes.length})` : '';
  $('drawer-notes').replaceChildren(...(d.notes.length
    ? d.notes.map(n => el('div', { class: 'note' }, [el('div', { class: 'note-date', text: n.file }), el('div', { class: 'note-text', text: n.text })]))
    : [el('div', { class: 'muted', text: 'No progress notes yet.' })]));
  refreshRun();
}

function showTab(name) {
  activeTab = ['chat', 'details', 'brief', 'notes'].includes(name) ? name : 'chat';
  for (const b of document.querySelectorAll('#drawer-tabs button')) b.setAttribute('aria-selected', String(b.dataset.tab === activeTab));
  if (chatHome === 'drawer') $('chat').hidden = activeTab !== 'chat';
  $('drawer-details').hidden = activeTab !== 'details';
  $('drawer-brief').hidden = activeTab !== 'brief';
  $('drawer-notes').hidden = activeTab !== 'notes';
}
for (const b of document.querySelectorAll('#drawer-tabs button')) b.addEventListener('click', () => showTab(b.dataset.tab));

function setAction(t) {
  const b = $('drawer-action');
  const label = actionLabel(t);
  b.hidden = !label;
  b.disabled = false;
  // In review, approving is the main action; opening a terminal is the quieter one.
  b.classList.toggle('primary', t.status !== 'review');
  if (label) b.textContent = label;
  const box = $('drawer-actions');
  const acts = stageActions(t);
  box.replaceChildren(
    ...stateChips(t),
    ...acts.map(([act, text]) => {
      const x = el('button', { class: 'card-btn' + (act === 'approve' ? ' primary' : ''), text, attrs: { type: 'button' } });
      x.addEventListener('click', async () => { x.disabled = true; await stageAction(t.slug, act, x); if (act === 'approve') closeDrawer(); });
      return x;
    }),
  );
  box.hidden = box.childElementCount === 0;
}

function drawerMsg(text, isError) {
  const m = $('drawer-msg');
  m.hidden = !text;
  m.textContent = text || '';
  m.classList.toggle('err', !!isError);
}

// Ask focusflow to open the task's session. focusflow's own output or error text is what the user sees.
async function requestOpen(slug) {
  try {
    const r = await api('/api/open', 'POST', { slug });
    return { ok: true, text: r.output || 'Opened.' };
  } catch (err) {
    return { ok: false, text: err.message };
  }
}

async function cardAction(slug, btn, label) {
  btn.disabled = true;
  btn.textContent = 'Opening…';
  const r = await requestOpen(slug);
  toast(r.ok ? r.text : `Could not ${label.toLowerCase()}. ${say(r.text)}`, !r.ok);
  await reload();
}

async function runAction() {
  const slug = openSlug;
  const b = $('drawer-action');
  const label = b.textContent;
  b.disabled = true;
  b.textContent = 'Opening…';
  drawerMsg('');
  const r = await requestOpen(slug);
  drawerMsg(r.text, !r.ok);
  b.textContent = label;
  b.disabled = false;
  await reload();
}

// ---- chat ----
const MAX_TEXT = 380;
const chatError = msg => { $('chat-error').hidden = !msg; $('chat-error').textContent = msg || ''; };

function fmtTime(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

// Mirrors the server's rules so mistakes show up before a round trip; the server still decides.
function checkText(raw) {
  const text = raw.trim();
  if (!text) return 'Type a message first.';
  if (text.length > MAX_TEXT) return `The message must be at most ${MAX_TEXT} characters.`;
  if (text.startsWith('-')) return 'The message must not start with "-".';
  return null;
}

function updateCount() {
  $('chat-count').textContent = `${$('chat-text').value.length}/${MAX_TEXT}`;
}

function resetChat() {
  thread = null;
  $('chat-text').value = '';
  updateCount();
  $('chat-thread').replaceChildren();
  $('chat-note').hidden = true;
  $('chat-confirm').hidden = true;
  $('chat-state').textContent = '';
  $('chat-hint').hidden = true;
  chatError('');
  clearTimeout(markTimer);
  markTimer = null;
}

function renderThread(t) {
  const box = $('chat-thread');
  const nearBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  const items = t.messages.length
    ? t.messages.map(m => el('div', { class: `msg ${m.direction}${m.unread ? ' unread' : ''}${m.urgent ? ' urgent' : ''}` }, [
      el('div', { class: 'msg-body', text: m.body }),
      el('div', { class: 'msg-meta', text: `${Logic.humanTime(m.at)}${m.urgent ? ' · urgent' : ''}${m.unread ? ' · new' : ''}${m.direction === 'out' && m.delivered_at !== undefined ? (m.delivered_at ? ' · delivered' : ' · sent') : ''}` }),
    ]))
    : [el('div', { class: 'muted', text: 'No messages yet.' })];
  box.replaceChildren(...items);
  if (nearBottom) box.scrollTop = box.scrollHeight;
  $('chat-note').hidden = !t.awaiting_reply;
  // A real receipt: "sent" until the session has surfaced the message, then "delivered".
  $('chat-note').textContent = t.delivery === 'sent'
    ? (t.stalled ? 'Sent, but the session has not seen it yet — it may not be watching its inbox.' : 'Sent — waiting for the session to pick it up.')
    : (t.stalled ? 'Delivered, but no reply yet, well past the usual wait.' : 'Delivered, no reply yet.');
  $('chat-note').classList.toggle('stalled', !!t.stalled);
}

// Send is headless by default: when nothing is running, a message starts (or continues) an unattended
// run — a fresh run when there is no session yet, or a continuation of the existing one when the
// harness is claude (the only one this app can talk to directly). No terminal is ever opened for a
// message; the user opens one explicitly with Resume. When a session is already live (a terminal the
// user opened), Send goes to its inbox instead, so two runs never share a session. "Queue only" stays
// available for a stopped task. Other harnesses on a stopped session can only Queue, then Resume.
// Closed tasks: no composer.
const taskOf = slug => tasks.find(t => t.slug === slug);

function canSendHeadless(t) {
  return !!t && !t.live && (!t.has_session || t.harness === 'claude');
}

function renderChatTitle(t) {
  const box = $('chat-title');
  box.hidden = chatHome !== 'inbox';
  if (box.hidden) return;
  const task = taskOf(openSlug);
  const open = el('button', { class: 'card-btn', text: 'Details', attrs: { type: 'button' } });
  open.addEventListener('click', () => openDrawer(openSlug, open));
  box.replaceChildren(
    el('span', { text: task?.name ?? openSlug }),
    ...(task ? [el('span', { class: `chip stage stage-${task.status}`, text: `${STAGE_ICON[task.status] ?? ''} ${task.status}` })] : []),
    el('span', { class: 'muted', text: $('chat-state').textContent }),
    open,
  );
}

function renderComposer(t) {
  const closed = t.archived || t.status === 'done';
  const headlessAllowed = !t.has_session || t.harness === 'claude';
  $('chat-composer').hidden = closed;
  $('chat-state').textContent = t.archived ? 'Archived' : t.status === 'done' ? 'Done'
    : t.live ? (t.working ? 'Working' : 'Idle') : t.auto_run_status === 'running' ? 'Running in the background' : 'Not running';
  $('chat-send').hidden = false;
  $('chat-send').title = canSendHeadless(t) ? 'Runs in the background — no terminal is opened' : 'Delivered to the running session';
  renderChatTitle(t);
  $('chat-fanout-row').hidden = !(taskOf(openSlug) && isAgent(taskOf(openSlug)));
  $('chat-queue').hidden = !!t.live || !headlessAllowed;
  $('chat-queue').textContent = 'Leave for later';
  $('chat-headless').hidden = true;
  $('chat-hint').hidden = closed || !!t.live || headlessAllowed;
  $('chat-hint').textContent = headlessAllowed ? '' :
    `This task's session runs on ${t.harness ?? 'an unrecorded harness'}; headless continuation is only supported for claude sessions yet. Leave it for later, then open a terminal to get the answer.`;
  if (!headlessAllowed || closed) $('chat-confirm').hidden = true;
}

async function markReadNow() {
  if (!openSlug) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(openSlug)}/read`, 'POST', {});
    await loadInbox();
  } catch { /* the next poll shows the true state */ }
}

// Messages that arrive while the thread is open are marked read after 2 seconds.
function scheduleMarkRead(t) {
  if (markTimer || !t.messages.some(m => m.unread)) return;
  markTimer = setTimeout(() => { markTimer = null; markReadNow(); }, 2000);
}

async function refreshThread() {
  const slug = openSlug;
  if (!slug) return;
  const t = await api(`/api/tasks/${encodeURIComponent(slug)}/thread`);
  if (slug !== openSlug) return; // the drawer moved on while we waited
  thread = t;
  renderThread(t);
  renderComposer(t);
  scheduleMarkRead(t);
}

function setChatBusy(busy) {
  for (const id of ['chat-send', 'chat-queue', 'chat-headless', 'chat-confirm-go', 'chat-confirm-cancel']) $(id).disabled = busy;
}

async function sendChat(mode, confirm) {
  const problem = checkText($('chat-text').value);
  if (problem) { chatError(problem); return; }
  chatError('');
  setChatBusy(true);
  try {
    const r = await api(`/api/tasks/${encodeURIComponent(openSlug)}/message`, 'POST', {
      text: $('chat-text').value.trim(), mode, ...(confirm ? { confirm: true } : {}),
      ...($('chat-fanout').checked && !$('chat-fanout-row').hidden ? { fanout: true } : {}),
    });
    $('chat-text').value = '';
    $('chat-fanout').checked = false;
    updateCount();
    $('chat-confirm').hidden = true;
    if (r.note) toast(r.note);
    await refreshThread();
    await loadInbox();
  } catch (err) {
    chatError(say(err));
  } finally {
    setChatBusy(false);
  }
}

// The first-ever headless send for a task confirms, naming the task and where it will run. Once a
// task has run headlessly at least once (thread.ever_headless), later sends skip the dialog.
function askHeadless() {
  const problem = checkText($('chat-text').value);
  if (problem) { chatError(problem); return; }
  if (thread?.ever_headless) { sendChat('headless', true); return; }
  chatError('');
  const where = thread?.work_dir ? ` in ${thread.work_dir}` : '';
  $('chat-confirm-text').textContent = `Run "${taskOf(openSlug)?.name ?? openSlug}" in the background${where}? It runs unattended with permission prompts off.`;
  $('chat-confirm').hidden = false;
  $('chat-confirm-go').focus();
}

$('chat-text').addEventListener('input', updateCount);
$('chat-send').addEventListener('click', () => { if (canSendHeadless(thread)) askHeadless(); else sendChat('queue'); });
$('chat-queue').addEventListener('click', () => sendChat('queue'));
$('chat-headless').addEventListener('click', askHeadless);
$('chat-confirm-cancel').addEventListener('click', () => { $('chat-confirm').hidden = true; });
$('chat-confirm-go').addEventListener('click', () => sendChat('headless', true));
$('chat-text').addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && !$('chat-send').hidden) { e.preventDefault(); if (canSendHeadless(thread)) askHeadless(); else sendChat('queue'); }
});

const splitLayout = () => window.matchMedia('(min-width: 1400px)').matches;

async function openDrawer(slug, from) {
  lastFocus = from || document.activeElement;
  if (openSlug !== slug) resetChat();
  openSlug = slug;
  inboxSel = null;
  drawerMsg('');
  if (chatHome !== 'drawer') moveChat('drawer');
  try {
    await fillDrawer(slug);
  } catch (err) {
    showBanner(say(err));
    return;
  }
  const split = splitLayout();
  $('scrim').hidden = split;
  $('drawer').setAttribute('aria-modal', String(!split));
  document.body.classList.toggle('has-drawer', split);
  $('drawer').hidden = false;
  showTab(activeTab);
  history.replaceState(null, '', `${location.pathname}#task=${encodeURIComponent(slug)}`);
  $('drawer-close').focus();
  refreshThread().then(markReadNow).catch(err => chatError(say(err)));
}

function closeDrawer(keepSlug = false) {
  $('drawer').hidden = true;
  $('scrim').hidden = true;
  document.body.classList.remove('has-drawer');
  if (keepSlug !== true) openSlug = null;
  clearTimeout(markTimer);
  markTimer = null;
  history.replaceState(null, '', location.pathname);
  if (keepSlug !== true && lastFocus && document.contains(lastFocus)) lastFocus.focus();
}

$('drawer-close').addEventListener('click', () => closeDrawer());
$('drawer-action').addEventListener('click', runAction);
$('scrim').addEventListener('click', () => closeDrawer());
document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('drawer').hidden && !document.getElementById('capture').open) closeDrawer(); });
// While the drawer covers the board (narrow windows), Tab must stay inside it.
document.addEventListener('keydown', e => {
  if (e.key !== 'Tab' || $('drawer').hidden || splitLayout() || document.querySelector('dialog[open]')) return;
  const items = [...$('drawer').querySelectorAll('button, textarea, input, select, summary, a[href]')].filter(n => !n.disabled && n.offsetParent !== null);
  if (!items.length) return;
  const first = items[0]; const last = items[items.length - 1];
  if (!$('drawer').contains(document.activeElement)) { e.preventDefault(); first.focus(); }
  else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
});
$('refresh').addEventListener('click', async () => {
  const btn = $('refresh');
  btn.disabled = true;
  try {
    await api('/api/refresh', 'POST');
    await reload();
  } catch (err) {
    showBanner(err.message);
  } finally {
    btn.disabled = false;
  }
});

// ---- capture ----
const dlg = $('capture');
const capError = msg => { $('cap-error').hidden = !msg; $('cap-error').textContent = msg || ''; };
const NEW_PROJECT = '__new__';
let defaultProjectDir = '';

function showNewProject() {
  const on = $('cap-project').value === NEW_PROJECT;
  $('cap-newproject').hidden = !on;
  if (on) {
    if (!$('cap-np-dir').value) $('cap-np-dir').value = defaultProjectDir;
    $('cap-np-name').focus();
  }
}

// Lists the projects (plus "+ New project") and optionally selects one.
async function loadProjects(select = '') {
  const sel = $('cap-project');
  sel.replaceChildren(el('option', { text: 'None (floating)', attrs: { value: '' } }));
  try {
    const { projects, default_work_dir } = await api('/api/projects');
    defaultProjectDir = default_work_dir || '';
    for (const p of projects) sel.append(el('option', { text: p.name === p.slug ? p.slug : `${p.name} (${p.slug})`, attrs: { value: p.slug } }));
  } catch (err) {
    capError(`Could not load projects: ${err.message}`);
  }
  sel.append(el('option', { text: '+ New project…', attrs: { value: NEW_PROJECT } }));
  sel.value = select;
  showNewProject();
}

async function openCapture() {
  if (dlg.open) return;
  $('capture-form').reset();
  capError('');
  $('cap-newproject').hidden = true;
  dlg.showModal();
  $('cap-name').focus();
  await loadProjects();
}

// Mirrors the server's rules so mistakes show up before a round trip; the server still decides.
function checkName(raw) {
  const name = raw.trim();
  if (!name) return 'Give the task a name.';
  if (name.startsWith('-')) return 'The name must not start with "-".';
  if (name.length > 200) return 'The name must be at most 200 characters.';
  return null;
}

// Same rules as the server's validateNewProject; the server still decides.
function checkNewProject() {
  const name = $('cap-np-name').value.trim();
  if (!name) return ['Give the project a name.', 'cap-np-name'];
  if (name.startsWith('-')) return ['The project name must not start with "-".', 'cap-np-name'];
  if (name.length > 200) return ['The project name must be at most 200 characters.', 'cap-np-name'];
  if (!$('cap-np-dir').value.trim().startsWith('/')) return ['The work directory must be an absolute path (start with "/").', 'cap-np-dir'];
  return null;
}

async function submitCapture(andOpen) {
  const problem = checkName($('cap-name').value);
  if (problem) { capError(problem); $('cap-name').focus(); return; }
  const creating = $('cap-project').value === NEW_PROJECT;
  const npProblem = creating && checkNewProject();
  if (npProblem) { capError(npProblem[0]); $(npProblem[1]).focus(); return; }
  const buttons = ['cap-save', 'cap-save-open', 'cap-cancel'].map($);
  buttons.forEach(b => { b.disabled = true; });
  capError('');
  try {
    const r = await api('/api/capture', 'POST', {
      name: $('cap-name').value.trim(),
      project: creating ? undefined : ($('cap-project').value || undefined),
      new_project: creating ? { name: $('cap-np-name').value.trim(), work_dir: $('cap-np-dir').value.trim(), priority: $('cap-np-priority').value } : undefined,
      priority: $('cap-priority').value,
      note: $('cap-note').value.trim() || undefined,
      open: andOpen,
    });
    dlg.close();
    const problems = [r.warning, r.open_error && `Opening failed: ${r.open_error}`].filter(Boolean);
    toast(problems.length ? `Captured "${r.slug}", but:\n${problems.join('\n')}` : `Captured "${r.slug}"${r.opened ? ' and opened it' : ''}.`, problems.length > 0);
    await reload();
  } catch (err) {
    capError(say(err));
    // The project exists now, so retrying must pick it instead of creating a second one.
    if (err.data?.project) await loadProjects(err.data.project);
  } finally {
    buttons.forEach(b => { b.disabled = false; });
  }
}

$('cap-project').addEventListener('change', showNewProject);
$('capture-btn').addEventListener('click', openCapture);
$('cap-cancel').addEventListener('click', () => dlg.close());
$('capture-form').addEventListener('submit', e => { e.preventDefault(); submitCapture(false); });
$('cap-save-open').addEventListener('click', () => submitCapture(true));
document.addEventListener('keydown', e => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openCapture(); }
});

for (const b of document.querySelectorAll('#tabs .tab')) b.addEventListener('click', () => setView(b.dataset.view));

// ---- filters + quick-add ----
function saveFilters() { try { localStorage.setItem('focusflow-filters', JSON.stringify(filters)); } catch { /* optional */ } }
function fillFilterProjects() {
  const sel = $('f-project');
  const slugs = [...new Set(tasks.filter(t => t.project && !isAgent(t)).map(t => t.project))].sort();
  const want = ['', '__none__', ...slugs];
  if (JSON.stringify([...sel.options].map(o => o.value)) !== JSON.stringify(want)) {
    sel.replaceChildren(el('option', { text: 'All projects', attrs: { value: '' } }), el('option', { text: 'No project', attrs: { value: '__none__' } }), ...slugs.map(x => el('option', { text: x, attrs: { value: x } })));
  }
  sel.value = want.includes(filters.project) ? filters.project : '';
  $('f-priority').value = filters.priority;
  if (document.activeElement !== $('f-text')) $('f-text').value = filters.text;
  $('f-clear').hidden = !(filters.project || filters.priority || filters.text);
}
function onFilter() {
  filters = { project: $('f-project').value, priority: $('f-priority').value, text: $('f-text').value };
  saveFilters();
  $('f-clear').hidden = !(filters.project || filters.priority || filters.text);
  renderBoard();
}
for (const id of ['f-project', 'f-priority']) $(id).addEventListener('change', onFilter);
$('f-text').addEventListener('input', onFilter);
$('f-clear').addEventListener('click', () => { filters = { project: '', priority: '', text: '' }; saveFilters(); fillFilterProjects(); renderBoard(); });

$('quick-add').addEventListener('submit', async e => {
  e.preventDefault();
  if (!knownProjects.length) { try { knownProjects = (await api('/api/projects')).projects.map(p => p.slug); } catch { /* the shorthand just will not match */ } }
  const parsed = Logic.parseQuickAdd($('qa-input').value, knownProjects);
  if (!parsed.name) return;
  const project = parsed.project ?? (filters.project && filters.project !== '__none__' ? filters.project : undefined);
  $('qa-go').disabled = true;
  try {
    const r = await api('/api/capture', 'POST', { name: parsed.name, project, priority: parsed.priority ?? 'medium', stage: 'next' });
    $('qa-input').value = '';
    landedSlug = r.slug;
    toast(`Added "${parsed.name}" to Next${project ? ` in ${project}` : ''}.`);
    await reload();
  } catch (err) {
    toast(`Could not add that. ${say(err)}`, true);
  } finally {
    $('qa-go').disabled = false;
  }
});

// ---- "since you were away" ----
const SEEN_KEY = 'focusflow-last-seen';
const stampSeen = () => { try { localStorage.setItem(SEEN_KEY, new Date().toISOString()); } catch { /* optional */ } };
async function maybeDigest() {
  let last = null;
  try { last = localStorage.getItem(SEEN_KEY); } catch { /* none */ }
  stampSeen();
  if (!last || Date.now() - Date.parse(last) < 10 * 60000 || document.documentElement.classList.contains('mini')) return;
  const s = await api(`/api/since?ts=${encodeURIComponent(last)}`);
  if (!s.total) return;
  const bits = [
    s.reviews.length && `${s.reviews.length} ready for review`,
    s.replies.length && `${s.replies.reduce((n, r) => n + r.count, 0)} repl${s.replies.reduce((n, r) => n + r.count, 0) === 1 ? 'y' : 'ies'}`,
    s.failed.length && `${s.failed.length} failed run${s.failed.length === 1 ? '' : 's'}`,
    s.done.length && `${s.done.length} approved`,
  ].filter(Boolean);
  const box = $('digest');
  const go = el('button', { class: 'primary', text: 'See what needs you', attrs: { type: 'button' } });
  const close = el('button', { text: 'Dismiss', attrs: { type: 'button' } });
  go.addEventListener('click', () => { box.hidden = true; setView('needs'); });
  close.addEventListener('click', () => { box.hidden = true; });
  box.replaceChildren(el('span', { class: 'digest-text', text: `Since ${Logic.humanTime(last)}: ${bits.join(', ')}.` }), go, close);
  box.hidden = false;
}
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') stampSeen(); });
window.addEventListener('pagehide', stampSeen);
setInterval(() => { if (document.visibilityState === 'visible') stampSeen(); }, 60000);

// ---- legend ----
$('help-btn').addEventListener('click', () => $('legend').showModal());
$('legend-close').addEventListener('click', () => $('legend').close());
document.addEventListener('keydown', e => {
  if (e.key === '?' && !Logic.isEditing(document.activeElement) && !document.querySelector('dialog[open]')) { e.preventDefault(); $('legend').showModal(); }
  // j / k step through conversations in the inbox
  if ((e.key === 'j' || e.key === 'k') && view === 'inbox' && !Logic.isEditing(document.activeElement) && !e.metaKey && !e.ctrlKey) {
    const list = inbox.threads.map(t => t.slug);
    if (!list.length) return;
    const i = list.indexOf(inboxSel);
    const next = list[Math.min(list.length - 1, Math.max(0, i + (e.key === 'j' ? 1 : -1)))];
    if (next) selectThread(next);
  }
});

$('search').addEventListener('input', e => {
  clearTimeout(searchTimer);
  const q = e.target.value.trim();
  if (!q || q.startsWith('-')) { closeSearch(); return; }
  searchTimer = setTimeout(() => runSearch(q), 250);
});
$('search').addEventListener('keydown', e => { if (e.key === 'Escape') { closeSearch(); e.target.blur(); } });
document.addEventListener('click', e => { if (!e.target.closest('.search')) closeSearch(); });
document.addEventListener('keydown', e => {
  if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName)) { e.preventDefault(); $('search').focus(); }
});
$('sound-toggle').checked = soundOn;
$('sound-toggle').addEventListener('change', e => {
  soundOn = e.target.checked;
  try { localStorage.setItem('focusflow-sound', soundOn ? '1' : '0'); } catch { /* remembering is optional */ }
  if (soundOn) primeAudio();
});
document.addEventListener('click', () => { if (soundOn) primeAudio(); });

initToken();
Help.init({
  onRecovered: () => reload(), // the server is back: refresh now instead of at the next 30s tick
  onRetry: () => { // the user ran the command (or another tab stored a token): look again
    try { token = localStorage.getItem('focusflow-token') || token; } catch { /* keep what we have */ }
    reload();
  },
});
setView(view);
reload().then(() => {
  const slug = new URLSearchParams(location.hash.slice(1)).get('task');
  if (slug && tasks.some(t => t.slug === slug)) openDrawer(slug);
  maybeDigest().catch(() => {});
});
setInterval(reload, POLL_MS);
setInterval(() => { loadInbox().catch(reportError); }, INBOX_POLL_MS);
setInterval(() => { if (openSlug && chatVisible()) refreshThread().catch(() => {}); if (!$('drawer').hidden) refreshRun(); }, INBOX_POLL_MS);
