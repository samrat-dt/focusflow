import { h, $, clear, isEditing } from './dom.js';
import { icon } from './icons.js';
import { api, setToken, reloadToken, helpApi } from './api.js';
import { S, subscribe, load, loadLight, capture, tasksNow } from './state.js';
import { route, onRoute, go, routeOpts, reparse } from './router.js';
import { dialog, toast, oops } from './ui.js';
import { parseQuickAdd, fingerprint } from './logic.js';
import { VIEWS } from './views/index.js';

// ---------- start-up: token, legacy links from the Mac app ----------
(function boot() {
  const raw = location.hash.replace(/^#/, '');
  if (!raw.startsWith('/')) {
    const q = new URLSearchParams(raw);
    setToken(q.get('token'));
    if (q.get('mini') === '1') document.documentElement.classList.add('mini');
    const wanted = q.get('route');
    const legacy = wanted && wanted.startsWith('/') ? wanted : q.get('task') ? `/task/${q.get('task')}` : { board: '/tasks', projects: '/projects', archive: '/archive', inbox: '/inbox', needs: '/home' }[q.get('view')] ?? '/home';
    history.replaceState(null, '', `#${legacy}`);
    try { if (q.get('mini') === '1') sessionStorage.setItem('focusflow-mini', '1'); } catch { /* optional */ }
  } else {
    setToken(null);
  }
  try { if (sessionStorage.getItem('focusflow-mini') === '1') document.documentElement.classList.add('mini'); } catch { /* optional */ }
  reparse();
})();

const mini = document.documentElement.classList.contains('mini');

// ---------- shell ----------
const NAV = [
  { id: 'home', label: 'Home', icon: 'home', path: '/home', count: () => S.needs.total || null, key: 'g h' },
  { id: 'inbox', label: 'Inbox', icon: 'inbox', path: '/inbox', count: () => S.inbox.unread_total || null, key: 'g i' },
  { id: 'tasks', label: 'Tasks', icon: 'tasks', path: '/tasks', key: 'g t' },
  { id: 'projects', label: 'Projects', icon: 'folder', path: '/projects', key: 'g p' },
  { id: 'memory', label: 'Memory', icon: 'memory', path: '/memory', key: 'g m' },
];
const FOOT = [
  { id: 'archive', label: 'Archive', icon: 'archive', path: '/archive' },
  { id: 'settings', label: 'Settings', icon: 'settings', path: '/settings' },
  { id: 'about', label: 'About & Help', icon: 'help', path: '/about', match: ['about', 'help'] },
];

const sidebar = h('nav.sidebar', { 'aria-label': 'Main' });
const main = h('main.main', { id: 'main', tabindex: '-1' });
const menuToggle = h('button.btn.icon.menu-toggle', { type: 'button', 'aria-label': 'Menu', on: { click: () => sidebar.classList.toggle('open') } }, icon('menu'));
document.getElementById('root').append(h('div.app', sidebar, main), menuToggle);

function navItem(n) {
  const current = (n.match ?? [n.id]).includes(route.name) || (n.id === 'tasks' && route.name === 'task') || (n.id === 'projects' && route.name === 'project');
  const c = n.count?.();
  return h('a.nav-item', { href: `#${n.path}`, 'aria-current': current ? 'page' : undefined, title: n.key ? `${n.label} (${n.key})` : n.label, on: { click: () => sidebar.classList.remove('open') } },
    icon(n.icon), h('span.label', n.label), c ? h('span', { class: n.id === 'home' ? 'badge' : 'count' }, String(c)) : null);
}

function renderSidebar() {
  const projects = S.overview.projects.slice(0, 8);
  clear(sidebar,
    h('div.brand', h('span.brand-mark', icon('logo')), 'Focusflow'),
    h('button.search-trigger', { type: 'button', on: { click: openPalette } }, icon('search'), 'Search or jump to…', h('span.kbd', '⌘K')),
    h('button.btn.primary.new-task', { type: 'button', on: { click: () => newTask() } }, icon('plus'), 'New task'),
    h('div.nav', NAV.map(navItem)),
    projects.length ? h('div.nav-h', 'Projects') : null,
    h('div.nav.nav-sub', projects.map(p => navItem({ id: `p:${p.slug}`, label: p.name, icon: 'folder', path: `/project/${p.slug}`, match: route.name === 'project' && route.id === p.slug ? [route.name] : []}))),
    h('div.sidebar-foot', FOOT.map(navItem)));
}

// ---------- rendering ----------
let current = { key: null, node: null, cleanup: null, fp: null };
let stale = false;

function render(force = false) {
  const view = VIEWS[route.name] ?? VIEWS.home;
  const key = `${route.name}/${route.id ?? ''}?${new URLSearchParams(route.query)}`;
  const fp = fingerprint([view.deps ? view.deps(route) : [S.tasks, S.needs, S.inbox, S.overview, [...S.runs]], tasksNow().map(t => t.status)]);
  if (!force && key === current.key && fp === current.fp) return;
  // Never pull the page out from under someone typing: a keyed field keeps its text, cursor and focus
  // across the redraw; an unkeyed one postpones the redraw until it loses focus.
  const ae = document.activeElement;
  let keep = null;
  if (key === current.key && main.contains(ae) && isEditing(ae)) {
    const k = ae.closest('[data-key]')?.dataset.key;
    if (!k && !force) { stale = true; return; }
    if (k) keep = { k, value: ae.value, s: ae.selectionStart, e: ae.selectionEnd };
  }
  const sameView = key === current.key;
  const focusKey = sameView ? document.activeElement?.closest?.('[data-key]')?.dataset.key : null;
  const scroll = sameView || routeOpts.keepScroll ? scrollY : 0;
  current.cleanup?.();
  const ctx = { cleanups: [], onCleanup(fn) { this.cleanups.push(fn); }, refresh: () => render(true), opts: routeOpts };
  const node = view.render(route, ctx);
  clear(main, node);
  current = { key, node, fp, cleanup: () => ctx.cleanups.forEach(fn => fn()) };
  stale = false;
  document.title = `${view.title?.(route) ?? 'Focusflow'}${S.needs.badge ? ` (${S.needs.badge})` : ''}`;
  scrollTo(0, scroll);
  const again = focusKey ? [...main.querySelectorAll('[data-key]')].find(n => n.dataset.key === focusKey) : null;
  if (again && keep && isEditing(again)) {
    again.value = keep.value;
    again.focus({ preventScroll: true });
    try { again.setSelectionRange(keep.s, keep.e); } catch { /* not a text field */ }
  } else if (again) again.focus({ preventScroll: true });
  else if (!sameView && !routeOpts.focus) main.focus({ preventScroll: true });
}
main.addEventListener('focusout', () => setTimeout(() => { if (stale && !isEditing(document.activeElement)) render(); }, 0));

subscribe(() => { renderSidebar(); render(); });
onRoute(() => { renderSidebar(); render(true); });

// ---------- new task ----------
export function newTask({ project = '', stage = 'next' } = {}) {
  dialog((box, close) => {
    const name = h('input.input', { placeholder: 'What needs doing?', maxlength: '200', autofocus: true, 'aria-label': 'Task name' });
    const proj = h('select.select', { 'aria-label': 'Project' }, h('option', { value: '' }, 'No project'), S.projectList.map(p => h('option', { value: p.slug, selected: p.slug === project }, p.name)));
    const pri = h('select.select', { 'aria-label': 'Priority' }, ['high', 'medium', 'low'].map(v => h('option', { value: v, selected: v === 'medium' }, v[0].toUpperCase() + v.slice(1))));
    const where = h('select.select', { 'aria-label': 'Where it goes' }, h('option', { value: 'next', selected: stage === 'next' }, 'Up next'), h('option', { value: 'backlog', selected: stage === 'backlog' }, 'Backlog'));
    const note = h('textarea.textarea', { rows: '4', maxlength: '2000', placeholder: 'Details, links, what done looks like (optional). This becomes the brief Claude reads first.' });
    const err = h('div.banner.error', { hidden: true });
    const create = async start => {
      const parsed = parseQuickAdd(name.value, S.projectList.map(p => p.slug));
      if (!parsed.name) { name.focus(); return; }
      try {
        const r = await capture({ name: parsed.name, project: parsed.project ?? proj.value, priority: parsed.priority ?? pri.value, note: note.value.trim(), stage: where.value });
        close();
        toast(`Added “${parsed.name}”.`);
        if (start) go(`/task/${r.slug}`, { focus: 'composer' });
      } catch (e) { err.hidden = false; err.textContent = e.message; }
    };
    name.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); create(e.metaKey || e.ctrlKey); } });
    box.append(h('h2', 'New task'), err,
      h('label.field', h('span', 'Task'), name, h('span.hint', 'Tip: type #project and !high to set them as you write.')),
      h('div.row', h('label.field.grow', h('span', 'Project'), proj), h('label.field.grow', h('span', 'Priority'), pri), h('label.field.grow', h('span', 'Add to'), where)),
      h('label.field', h('span', 'Brief'), note),
      h('div.dialog-foot',
        h('button.btn', { type: 'button', on: { click: close } }, 'Cancel'),
        h('button.btn', { type: 'button', on: { click: () => create(false) } }, 'Add'),
        h('button.btn.primary', { type: 'button', title: '⌘ Enter', on: { click: () => create(true) } }, 'Add and open')));
  }, { label: 'New task' });
}

export function newProject() {
  dialog((box, close) => {
    const name = h('input.input', { placeholder: 'e.g. Website relaunch', maxlength: '200', autofocus: true });
    const dir = h('input.input', { placeholder: 'Choose a folder, or paste its path', value: '', spellcheck: 'false' });
    const pri = h('select.select', ['high', 'medium', 'low'].map(v => h('option', { value: v, selected: v === 'medium' }, v[0].toUpperCase() + v.slice(1))));
    const err = h('div.banner.error', { hidden: true });
    const choose = h('button.btn', { type: 'button', on: { click: async () => {
      choose.disabled = true; choose.lastChild.textContent = 'Waiting for Finder…';
      try { const r = await api('/api/pick-folder', 'POST', { start: dir.value.trim() || undefined, prompt: 'Choose or create the project folder' }); if (r.path) dir.value = r.path; }
      catch (e) { err.hidden = false; err.textContent = e.message; }
      choose.disabled = false; choose.lastChild.textContent = 'Choose…';
    } } }, icon('folder'), h('span', 'Choose…'));
    const create = async () => {
      if (!name.value.trim()) { name.focus(); return; }
      if (!dir.value.trim().startsWith('/')) { err.hidden = false; err.textContent = 'Choose the folder where this project’s code or files live.'; return; }
      try {
        const r = await api('/api/projects/new', 'POST', { name: name.value.trim(), work_dir: dir.value.trim(), priority: pri.value });
        close(); await load(); toast(`Created “${name.value.trim()}”.`); go(`/project/${r.slug}`);
      } catch (e) { err.hidden = false; err.textContent = e.message; }
    };
    box.append(h('h2', 'New project'), err,
      h('label.field', h('span', 'Name'), name),
      h('label.field', h('span', 'Folder'), h('div.row', dir, choose), h('span.hint', 'Claude works in this folder. Pick an existing one or create a new one in the Finder dialog.')),
      h('label.field', h('span', 'Priority'), pri),
      h('div.dialog-foot', h('button.btn', { type: 'button', on: { click: close } }, 'Cancel'), h('button.btn.primary', { type: 'button', on: { click: create } }, 'Create project')));
  }, { label: 'New project' });
}
window.addEventListener('ff:new-task', e => newTask(e.detail ?? {}));
window.addEventListener('ff:new-project', () => newProject());
window.addEventListener('ff:search', () => openPalette());

// ---------- command palette ----------
function openPalette() {
  dialog((box, close) => {
    box.classList.add('palette');
    const input = h('input.input', { placeholder: 'Search tasks, projects and commands…', autofocus: true, 'aria-label': 'Search' });
    const list = h('div.palette-list', { role: 'listbox' });
    let items = [];
    let sel = 0;
    let serverHits = [];
    let timer;
    const commands = [
      { label: 'New task', icon: 'plus', hint: 'N', run: () => newTask() },
      { label: 'New project', icon: 'folder', run: () => newProject() },
      ...NAV.concat(FOOT).map(n => ({ label: `Go to ${n.label}`, icon: n.icon, run: () => go(n.path) })),
      { label: 'Board view', icon: 'board', run: () => go('/tasks?view=board') },
      { label: 'Keyboard shortcuts', icon: 'help', run: () => go('/help/shortcuts') },
      { label: 'Troubleshooting', icon: 'shield', run: () => go('/help/troubleshooting') },
    ];
    const draw = () => {
      const q = input.value.trim().toLowerCase();
      const match = s => !q || s.toLowerCase().includes(q);
      const tasks = tasksNow().filter(t => (t.kind ?? 'regular') !== 'playbook_run' && match(`${t.name} ${t.slug} ${(t.tags ?? []).join(' ')}`)).slice(0, 8)
        .map(t => ({ label: t.name, icon: t.kind === 'project_agent' ? 'sparkle' : 'tasks', hint: t.status, run: () => go(`/task/${t.slug}`) }));
      const projects = S.overview.projects.filter(p => match(p.name)).slice(0, 5).map(p => ({ label: p.name, icon: 'folder', hint: 'Project', run: () => go(`/project/${p.slug}`) }));
      const cmds = commands.filter(c => match(c.label)).slice(0, q ? 6 : 8);
      const found = serverHits.map(x => ({ label: x.text, icon: 'search', hint: x.scope, run: () => x.scope.startsWith('task:') ? go(`/task/${x.scope.slice(5)}`) : x.scope.startsWith('project:') ? go(`/project/${x.scope.slice(8)}`) : toast(`${x.file}: ${x.text}`) }));
      const groups = [['Commands', cmds], ['Tasks', tasks], ['Projects', projects], ['In briefs and notes', found]].filter(([, g]) => g.length);
      items = groups.flatMap(([, g]) => g);
      sel = Math.min(sel, Math.max(0, items.length - 1));
      let i = 0;
      clear(list, groups.length ? groups.map(([title, g]) => [h('div.palette-h', title), g.map(it => {
        const idx = i++;
        return h('div.palette-item', { role: 'option', 'aria-selected': String(idx === sel), on: { mousemove: () => { if (sel !== idx) { sel = idx; draw(); } }, click: () => { close(); it.run(); } } }, icon(it.icon), h('span', it.label), it.hint && h('span.hint', it.hint));
      })]) : h('div.empty', { style: 'padding:24px' }, 'Nothing matches.'));
    };
    input.addEventListener('input', () => {
      sel = 0; serverHits = []; draw();
      clearTimeout(timer);
      const q = input.value.trim();
      if (q.length >= 3 && !q.startsWith('-')) timer = setTimeout(async () => { try { serverHits = (await api(`/api/search?q=${encodeURIComponent(q)}`)).hits.slice(0, 6); draw(); } catch { /* search is optional */ } }, 250);
    });
    input.addEventListener('keydown', e => {
      if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(items.length - 1, sel + 1); draw(); }
      if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(0, sel - 1); draw(); }
      if (e.key === 'Enter' && items[sel]) { e.preventDefault(); close(); items[sel].run(); }
    });
    box.append(input, list);
    draw();
  }, { label: 'Search' });
}

// ---------- keyboard ----------
let chord = null;
document.addEventListener('keydown', e => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openPalette(); return; }
  if (document.querySelector('.overlay') || isEditing(document.activeElement) || e.metaKey || e.ctrlKey || e.altKey) return;
  if (chord === 'g') {
    chord = null;
    const to = { h: '/home', i: '/inbox', t: '/tasks', p: '/projects', m: '/memory', s: '/settings', a: '/about' }[e.key];
    if (to) { e.preventDefault(); go(to); }
    return;
  }
  if (e.key === 'g') { chord = 'g'; setTimeout(() => { chord = null; }, 1200); return; }
  if (e.key === 'n' || e.key === 'c') { e.preventDefault(); newTask(); }
  if (e.key === '/') { e.preventDefault(); openPalette(); }
  if (e.key === '?') { e.preventDefault(); go('/help/shortcuts'); }
});

// ---------- go ----------
helpApi().init({ onRecovered: () => load(), onRetry: () => { reloadToken(); load(); } });
renderSidebar();
render(true);
load().then(() => {
  if (!mini && S.loaded && !S.tasks.length && !S.overview.projects.length && route.name === 'home') render(true);
});
setInterval(load, 20000);
setInterval(loadLight, 5000);
document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'visible') load(); });
