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
const VIEWS = ['board', 'projects', 'archive', 'inbox'];
const PRI = { high: 0, medium: 1, low: 2 };
const POLL_MS = 30000;

const $ = id => document.getElementById(id);
let token = null;
let tasks = [];
let showArchived = false;
let openSlug = null;
let lastFocus = null;
const INBOX_POLL_MS = 5000;
let view = 'board';
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
  if (!err.helped) showBanner(err.message);
}

function showBanner(msg) {
  $('banner').hidden = !msg;
  $('banner').textContent = msg || '';
}

let toastTimer = null;
function toast(msg, warn = false) {
  const t = $('toast');
  clearTimeout(toastTimer);
  t.hidden = !msg;
  t.textContent = msg || '';
  t.classList.toggle('warn', warn);
  if (msg && !warn) toastTimer = setTimeout(() => { t.hidden = true; }, 8000);
}

function ago(iso) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const m = Math.max(0, Math.round((Date.now() - t) / 60000));
  if (m < 60) return `${m}m ago`;
  if (m < 60 * 48) return `${Math.round(m / 60)}h ago`;
  return `${Math.round(m / 1440)}d ago`;
}

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
  if (t.terminal_open) return 'Focus';
  return t.status === 'backlog' || t.status === 'next' ? 'Start' : 'Resume';
}

// Secondary, status-only actions. None of them opens a terminal.
function stageActions(t) {
  if (t.archived) return [];
  const a = [];
  switch (t.status) {
    case 'backlog': a.push(['next', 'Next']); break;
    case 'next': a.push(['pause', 'Pause'], ['backlog', 'Backlog']); break;
    case 'in-progress': a.push(['pause', 'Pause'], ['review', 'Send to review']); break;
    case 'paused': a.push(['unpause', 'Unpause']); break;
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
  if (t.headless_running) out.push(el('span', { class: 'chip chip-headless', text: '⚙ running headless', attrs: { title: 'An unattended run is in progress' } }));
  return out;
}

async function stageAction(slug, action, btn) {
  if (btn) btn.disabled = true;
  try {
    const r = await api(`/api/tasks/${encodeURIComponent(slug)}/action`, 'POST', { action });
    toast(r.output || 'Done.');
  } catch (err) {
    toast(`${action} failed:\n${err.message}`, true);
  }
  await reload();
}

function card(t) {
  const meta = [el('span', { class: `chip pri-${t.priority}`, text: t.priority })];
  if (t.project) meta.push(el('span', { class: 'chip', text: t.project }));
  meta.push(el('span', { class: `chip stage stage-${t.status}`, text: t.status }));
  meta.push(...stateChips(t));
  for (const tag of t.tags) meta.push(el('span', { class: 'chip', text: `#${tag}` }));
  if (t.archived) meta.push(el('span', { class: 'chip', text: 'archived' }));
  meta.push(...marks(t));
  const kids = [el('div', { class: 'card-title', text: t.name }), el('div', { class: 'card-meta' }, meta)];
  if (t.due_date) kids.push(el('div', { class: 'card-note', text: `due ${t.due_date}` }));
  if (t.waiting_on) kids.push(el('div', { class: 'card-note', text: `waiting: ${t.waiting_on}` }));
  const main = el('button', { class: 'card-main', attrs: { type: 'button', 'data-slug': t.slug } }, kids);
  main.addEventListener('click', () => openDrawer(t.slug, main));
  const wrap = el('div', { class: 'card' + (t.archived ? ' archived' : '') }, [main]);
  const label = actionLabel(t);
  if (label) {
    const b = el('button', { class: 'card-action', text: label, attrs: { type: 'button', 'aria-label': `${label}: ${t.name}` } });
    b.addEventListener('click', () => cardAction(t.slug, b, label));
    wrap.append(b);
  }
  const acts = stageActions(t);
  if (acts.length) {
    wrap.append(el('div', { class: 'card-actions' }, acts.map(([act, text]) => {
      const b = el('button', { class: 'card-btn' + (act === 'approve' ? ' primary' : ''), text, attrs: { type: 'button', 'aria-label': `${text}: ${t.name}` } });
      b.addEventListener('click', () => stageAction(t.slug, act, b));
      return b;
    })));
  }
  return wrap;
}

const boardTasks = () => tasks.filter(t => !t.archived && !isAgent(t) && (t.kind ?? 'regular') === 'regular');

function isRecentDone(t) {
  const at = Date.parse(t.status_changed_at || t.updated_at);
  return Number.isNaN(at) || Date.now() - at < DONE_RECENT_DAYS * 86400000;
}

function renderBoard() {
  const board = $('board');
  board.replaceChildren();
  const visible = boardTasks();
  for (const col of COLUMNS) {
    const all = visible.filter(t => t.status === col.status);
    const items = sortTasks(col.status === 'done' ? all.filter(isRecentDone) : all, col.status);
    const body = items.length ? items.map(card) : [el('div', { class: 'empty', text: 'Nothing here.' })];
    board.append(el('section', { class: `col col-${col.status}` }, [
      el('h2', {}, [el('span', { text: col.title }), el('span', { text: String(items.length) })]),
      el('div', { class: 'cards' }, body),
    ]));
  }
  const backlog = visible.filter(t => t.status === 'backlog').length;
  if (backlog) board.append(el('div', { class: 'board-note muted', text: `${backlog} task${backlog === 1 ? '' : 's'} in the backlog — see Projects.` }));
}

async function loadTasks() {
  const j = await api('/api/tasks');
  tasks = j.tasks;
  renderBoard();
}

function setView(name) {
  view = VIEWS.includes(name) ? name : 'board';
  try { localStorage.setItem('focusflow-view', view); } catch { /* remembering is optional */ }
  for (const v of VIEWS) $(v).hidden = view !== v;
  for (const b of document.querySelectorAll('#tabs .tab')) b.setAttribute('aria-pressed', String(b.dataset.view === view));
  loadViewData().catch(reportError);
}

// ---- projects ----
const saveExpanded = () => { try { localStorage.setItem('focusflow-expanded', JSON.stringify([...expanded])); } catch { /* optional */ } };

const STAGE_ORDER = ['in-progress', 'review', 'paused', 'next', 'backlog', 'done'];
function rollup(counts) {
  const parts = STAGE_ORDER.filter(s => counts[s]).map(s => `${counts[s]} ${s}`);
  return parts.length ? parts.join(' · ') : 'no tasks yet';
}

async function projectAction(slug, action, extra = {}) {
  try {
    const r = await api(`/api/projects/${encodeURIComponent(slug)}/action`, 'POST', { action, ...extra });
    toast(r.output || 'Done.');
  } catch (err) {
    toast(`${action} failed:\n${err.message}`, true);
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
    toast(`Could not open the project chat:\n${err.message}`, true);
  }
}

function addSubtaskForm(p) {
  const input = el('input', { class: 'inline-input', attrs: { type: 'text', maxlength: '200', placeholder: 'Add a subtask (stays in the backlog)…', 'aria-label': `New subtask in ${p.name}` } });
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
    } catch (err) {
      toast(`Could not add the subtask:\n${err.message}`, true);
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
  picker.hidden = true;
  open.addEventListener('click', () => { open.hidden = true; picker.hidden = false; sel.focus(); });
  cancel.addEventListener('click', () => { picker.hidden = true; open.hidden = false; });
  set.addEventListener('click', () => { set.disabled = true; projectAction(p.slug, 'schedule', { every: sel.value }); });
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
      el('div', { class: 'cards' }, p.tasks.length ? p.tasks.map(card) : [el('div', { class: 'empty', text: 'No subtasks yet.' })]),
    ]));
  }
  return el('section', { class: 'proj' + (open ? ' open' : '') }, kids);
}

function renderProjects() {
  const box = $('projects');
  box.replaceChildren();
  const bar = el('div', { class: 'view-bar' }, [el('button', { class: 'primary', text: '+ New project', attrs: { type: 'button', id: 'new-project-btn' } })]);
  bar.firstChild.addEventListener('click', openNewProject);
  box.append(bar);
  if (!overview.projects.length && !overview.floating.length) {
    box.append(el('div', { class: 'empty', text: 'No projects yet. Create one to group tasks under it.' }));
  }
  for (const p of overview.projects) box.append(projectRow(p));
  if (overview.floating.length) {
    box.append(el('section', { class: 'proj open' }, [
      el('div', { class: 'proj-top' }, [el('span', { class: 'proj-name', text: 'No project' }), el('span', { class: 'muted', text: `${overview.floating.length} task${overview.floating.length === 1 ? '' : 's'}` })]),
      el('div', { class: 'proj-body' }, [el('div', { class: 'cards' }, overview.floating.map(card))]),
    ]));
  }
}

// ---- archive ----
function renderArchive() {
  const box = $('archive');
  box.replaceChildren();
  const unarchiveProject = async slug => { await projectAction(slug, 'unarchive'); };
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
    npError(err.message);
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
  document.title = n ? `(${n}) Focusflow` : 'Focusflow';
  if (soundOn && lastUnread !== null && n > lastUnread) beep();
  lastUnread = n;
}

function renderInbox() {
  const box = $('inbox');
  box.replaceChildren();
  if (!inbox.threads.length) {
    box.append(el('div', { class: 'empty', text: 'No messages yet. Open a task and send one.' }));
    return;
  }
  for (const th of inbox.threads) {
    const b = el('button', { class: 'thread-row' + (th.unread ? ' unread' : ''), attrs: { type: 'button', 'data-slug': th.slug } }, [
      el('div', { class: 'thread-top' }, [
        el('span', { class: 'thread-name', text: th.name }),
        th.live ? el('span', { class: 'mark live', text: '*', attrs: { title: 'Session is live', 'aria-label': 'live' } }) : null,
        th.unread ? el('span', { class: 'badge', text: String(th.unread), attrs: { 'aria-label': `${th.unread} unread` } }) : null,
        el('span', { class: 'muted thread-time', text: ago(th.last.at) }),
      ]),
      el('div', { class: 'muted mono', text: th.slug }),
      el('div', { class: 'thread-preview', text: `${th.last.direction === 'out' ? 'You: ' : ''}${th.last.preview}` }),
    ]);
    b.addEventListener('click', () => openDrawer(th.slug, b));
    box.append(b);
  }
}

async function loadInbox() {
  inbox = await api('/api/inbox');
  renderBadge();
  renderInbox();
}

async function loadHealth() {
  const h = await api('/api/health');
  const foot = $('foot');
  foot.replaceChildren();
  const add = (text, bad) => foot.append(el('span', { class: bad ? 'bad' : '', text }));
  add(`${h.task_count} tasks`);
  add(h.last_snapshot_date ? `last snapshot ${h.last_snapshot_date}` : 'no snapshot yet', !h.last_snapshot_date);
  add(`${h.gap_count} gap${h.gap_count === 1 ? '' : 's'}`, h.gap_count > 0);
  add(h.cache_age_seconds == null ? 'not read yet' : `read ${ago(h.last_tick_at)}`);
  const sy = h.sync;
  add(`sync: ${sy.status}${sy.last_push_at ? ` (pushed ${ago(sy.last_push_at)})` : ''}`, sy.status === 'error');
  if (sy.last_error) add(sy.last_error, true);
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
    chip('sessions (30d)', st.sessions ?? 0);
    chip('output tokens', fmtTokens(st.tokens?.output ?? 0), 'Across the sessions active in the last 30 days');
    if (st.bus) chip('median reply', `${Math.round(st.bus.median_wait_seconds)}s`, `${st.bus.answered} answered, ${st.bus.pending} pending`);
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
  } catch (err) { toast(`Search failed:\n${err.message}`, true); }
}

async function reload() {
  try {
    await Promise.all([loadTasks(), loadHealth(), loadInbox(), loadViewData()]);
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

async function fillDrawer(slug) {
  const d = await api(`/api/tasks/${encodeURIComponent(slug)}`);
  const t = d.task;
  $('drawer-title').textContent = t.name;
  $('drawer-slug').textContent = t.slug;
  setAction(t);

  const kv = el('dl', { class: 'kv' });
  const row = (k, v) => { kv.append(el('dt', { text: k }), el('dd', { text: v ?? '—' })); };
  row('Status', t.status + (t.archived ? ' (archived)' : ''));
  row('Terminal', t.terminal_open ? 'open' : t.headless_running ? 'none (running headless)' : 'not open');
  if (isAgent(t)) row('Role', 'Project coordinator (one permanent session)');
  row('Priority', t.priority);
  row('Project', t.project);
  row('Tags', t.tags.length ? t.tags.map(x => `#${x}`).join(' ') : null);
  row('Due', t.due_date);
  row('Waiting on', t.waiting_on);
  row('Assignee', t.assignee);
  row('Updated', t.updated_at ? `${t.updated_at} (${ago(t.updated_at)})` : null);

  const s = d.session;
  const skv = el('dl', { class: 'kv' });
  const srow = (k, v) => { skv.append(el('dt', { text: k }), el('dd', { text: v ?? '—' })); };
  srow('Session', s.has_session ? (s.live ? 'live' : 'exists, not running') : 'none yet');
  srow('Harness', s.harness);
  srow('Started', s.started);
  srow('Last resumed', s.last_resumed);
  srow('Auto-run', s.auto_run_status);

  const sig = [];
  const g = t.signals;
  if (g.stale) sig.push(['stale', '!', 'Stale: no activity for a while']);
  if (g.waiting) sig.push(['waiting', 'W', 'Waiting on someone']);
  if (g.live) sig.push(['live', '*', 'Session is live']);
  if (g.due) sig.push(['due', 'D', g.due === 'overdue' ? 'Overdue' : 'Due soon']);
  const signals = sig.length
    ? el('div', { class: 'signals' }, sig.map(([cls, m, label]) => el('span', { class: 'signal' }, [el('span', { class: `mark ${cls}`, text: m }), el('span', { text: label })])))
    : el('div', { class: 'muted', text: 'No signals.' });

  const notes = d.notes.length
    ? d.notes.map(n => el('div', { class: 'note' }, [el('div', { class: 'note-date', text: n.file }), el('div', { class: 'note-text', text: n.text })]))
    : [el('div', { class: 'muted', text: 'No progress notes yet.' })];

  $('drawer-details').replaceChildren(
    sect('Details', kv),
    sect('Signals', signals),
    sect('Session', skv),
    sect('Brief', d.brief == null ? el('div', { class: 'muted', text: 'No brief.' }) : briefNode(d.brief)),
    sect(`Progress notes (${d.notes.length})`, ...notes),
  );
}

function setAction(t) {
  const b = $('drawer-action');
  const label = actionLabel(t);
  b.hidden = !label;
  b.disabled = false;
  if (label) b.textContent = label;
  // Status-only actions (none of them opens a terminal), plus a marker for the two independent states.
  const box = $('drawer-actions');
  const acts = stageActions(t);
  box.replaceChildren(
    el('span', { class: `chip stage stage-${t.status}`, text: t.status }),
    ...stateChips(t),
    ...acts.map(([act, text]) => {
      const x = el('button', { class: 'card-btn' + (act === 'approve' ? ' primary' : ''), text, attrs: { type: 'button' } });
      x.addEventListener('click', async () => { x.disabled = true; await stageAction(t.slug, act, x); });
      return x;
    }),
  );
  box.hidden = false;
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
  toast(r.ok ? r.text : `${label} failed:\n${r.text}`, !r.ok);
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
    ? t.messages.map(m => el('div', { class: `msg ${m.direction}${m.unread ? ' unread' : ''}` }, [
      el('div', { class: 'msg-body', text: m.body }),
      el('div', { class: 'msg-meta', text: `${fmtTime(m.at)}${m.unread ? ' · new' : ''}${m.direction === 'out' && m.delivered_at !== undefined ? (m.delivered_at ? ' · delivered' : ' · sent') : ''}` }),
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

function renderComposer(t) {
  const closed = t.archived || t.status === 'done';
  const headlessAllowed = !t.has_session || t.harness === 'claude';
  $('chat-composer').hidden = closed;
  $('chat-state').textContent = t.archived ? 'Archived' : t.status === 'done' ? 'Done'
    : t.live ? (t.working ? 'Working' : 'Idle') : 'Not running';
  $('chat-send').hidden = false;
  $('chat-send').title = canSendHeadless(t) ? 'Runs headlessly — no terminal is opened' : 'Delivered to the running session';
  $('chat-fanout-row').hidden = !(taskOf(openSlug) && isAgent(taskOf(openSlug)));
  $('chat-queue').hidden = !!t.live || !headlessAllowed;
  $('chat-queue').textContent = 'Queue only';
  $('chat-headless').hidden = true;
  $('chat-hint').hidden = closed || !!t.live || headlessAllowed;
  $('chat-hint').textContent = headlessAllowed ? '' :
    `This task's session runs on ${t.harness ?? 'an unrecorded harness'}; headless continuation is only supported for claude sessions yet. Queue it, then Resume to get the answer.`;
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
    if (r.note) drawerMsg(r.note, false);
    await refreshThread();
    await loadInbox();
  } catch (err) {
    chatError(err.message);
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
  $('chat-confirm-text').textContent = `Run "${$('drawer-title').textContent}" headless${where}? It runs unattended with permission prompts off.`;
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

async function openDrawer(slug, from) {
  lastFocus = from || document.activeElement;
  if (openSlug !== slug) resetChat();
  openSlug = slug;
  drawerMsg('');
  try {
    await fillDrawer(slug);
  } catch (err) {
    showBanner(err.message);
    return;
  }
  $('scrim').hidden = false;
  $('drawer').hidden = false;
  history.replaceState(null, '', `${location.pathname}#task=${encodeURIComponent(slug)}`);
  $('drawer-close').focus();
  // Opening a thread marks it read; the first fetch renders the "new" marks before that lands.
  refreshThread().then(markReadNow).catch(err => chatError(err.message));
}

function closeDrawer() {
  $('drawer').hidden = true;
  $('scrim').hidden = true;
  openSlug = null;
  clearTimeout(markTimer);
  markTimer = null;
  history.replaceState(null, '', location.pathname);
  if (lastFocus && document.contains(lastFocus)) lastFocus.focus();
}

$('drawer-close').addEventListener('click', closeDrawer);
$('drawer-action').addEventListener('click', runAction);
$('scrim').addEventListener('click', closeDrawer);
document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('drawer').hidden && !document.getElementById('capture').open) closeDrawer(); });
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
    capError(err.message);
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
});
setInterval(reload, POLL_MS);
setInterval(() => { loadInbox().catch(reportError); }, INBOX_POLL_MS);
setInterval(() => { if (openSlug && !$('drawer').hidden) refreshThread().catch(() => {}); }, INBOX_POLL_MS);
