import { h } from '../dom.js';
import { icon } from '../icons.js';
import { S, act, tasksNow } from '../state.js';
import { route, setQuery } from '../router.js';
import { toast, menu, empty } from '../ui.js';
import { STAGES, STAGE_NAME, filterTasks, groupTasks, sortTasks, dropAction, PRIORITY_NAME } from '../logic.js';
import { taskRow, taskCard, projectName } from '../parts.js';

const selected = new Set();
let dragSlug = null;
let landed = null;
let refresh = () => {};
const DONE_DAYS = 7;
const LANES = ['backlog', 'next', 'in-progress', 'paused', 'review', 'done'];

function prefs() {
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem('focusflow-tasks-view') || '{}'); } catch { /* none */ }
  const q = route.query;
  return { view: q.view ?? saved.view ?? 'board', group: q.group ?? saved.group ?? 'stage', project: q.project ?? '', priority: q.priority ?? '', q: q.q ?? '', done: q.done ?? '' };
}
function savePrefs(p) { try { localStorage.setItem('focusflow-tasks-view', JSON.stringify({ view: p.view, group: p.group })); } catch { /* optional */ } }

const recentDone = t => t.status !== 'done' || Date.now() - Date.parse(t.status_changed_at || t.updated_at) < DONE_DAYS * 86400000;

function toolbar(p) {
  const seg = (key, opts) => h('div.seg', { role: 'group' }, opts.map(([v, label, ic]) => h('button', { type: 'button', 'aria-pressed': String(p[key] === v), on: { click: () => { setQuery({ [key]: v }); savePrefs({ ...p, [key]: v }); } } }, ic && icon(ic), label)));
  const search = h('input.input', { type: 'search', placeholder: 'Filter…', value: p.q, style: 'width:170px', 'aria-label': 'Filter tasks', dataset: { key: 'task-filter' } });
  let t;
  search.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => setQuery({ q: search.value }), 250); });
  const projSel = h('select.select', { style: 'width:auto', 'aria-label': 'Project', on: { change: e => setQuery({ project: e.target.value }) } },
    h('option', { value: '' }, 'All projects'), h('option', { value: '__none__', selected: p.project === '__none__' }, 'No project'),
    S.projectList.map(x => h('option', { value: x.slug, selected: p.project === x.slug }, x.name)));
  const priSel = h('select.select', { style: 'width:auto', 'aria-label': 'Priority', on: { change: e => setQuery({ priority: e.target.value }) } },
    h('option', { value: '' }, 'Any priority'), ['high', 'medium', 'low'].map(v => h('option', { value: v, selected: p.priority === v }, PRIORITY_NAME[v])));
  const filtered = p.project || p.priority || p.q;
  return h('div.toolbar',
    seg('view', [['board', 'Board', 'board'], ['list', 'List', 'list']]),
    p.view === 'list' && seg('group', [['stage', 'By stage'], ['project', 'By project'], ['priority', 'By priority']]),
    h('span.grow'),
    search, projSel, priSel,
    filtered && h('button.btn.ghost.sm', { type: 'button', on: { click: () => setQuery({ project: '', priority: '', q: '' }) } }, icon('x'), 'Clear'));
}

function lane(status, tasks) {
  const body = h('div.lane-body', tasks.length ? tasks.map(t => taskCard(t, { landed: landed === t.slug })) : h('div.lane-empty', {
    next: 'Nothing queued. Add tasks you want to hand over.', 'in-progress': 'Nothing is being worked on.', paused: 'Nothing set aside.',
    review: 'Nothing waiting for your approval.', done: `Approved work from the last ${DONE_DAYS} days.`, backlog: 'Ideas and later work.',
  }[status]));
  const el = h('section.lane', { dataset: { status }, 'aria-label': STAGE_NAME[status] },
    h('div.lane-h', h('span.dot', { class: status }), STAGE_NAME[status], h('span.count', String(tasks.length)),
      status === 'next' || status === 'backlog' ? h('button.btn.ghost.icon.sm', { type: 'button', style: 'margin-left:auto', 'aria-label': `Add to ${STAGE_NAME[status]}`, on: { click: () => dispatchEvent(new CustomEvent('ff:new-task', { detail: { stage: status } })) } }, icon('plus')) : null),
    body);
  el.addEventListener('dragover', e => {
    const t = S.tasks.find(x => x.slug === dragSlug);
    if (t && dropAction(t, status).action) { e.preventDefault(); el.classList.add('drop-ok'); }
  });
  el.addEventListener('dragleave', e => { if (!el.contains(e.relatedTarget)) el.classList.remove('drop-ok'); });
  el.addEventListener('drop', e => {
    e.preventDefault(); el.classList.remove('drop-ok');
    const t = S.tasks.find(x => x.slug === dragSlug);
    dragSlug = null;
    if (!t) return;
    const { action, hint } = dropAction(t, status);
    if (!action) { if (hint) toast(hint); return; }
    landed = t.slug;
    act(t.slug, action);
  });
  return el;
}

function bulkBar(p) {
  if (!selected.size) return null;
  const run = action => { for (const slug of selected) act(slug, action); selected.clear(); refresh(); };
  return h('div.banner', { style: 'position:sticky;top:12px;z-index:5;box-shadow:var(--shadow-md)' },
    h('strong', `${selected.size} selected`), h('span.grow'),
    h('button.btn.sm', { type: 'button', on: { click: e => menu(e.currentTarget, [
      { header: 'Move to' },
      ...[['next', 'Up next'], ['unpause', 'In progress'], ['pause', 'Paused'], ['review', 'Ready for review'], ['backlog', 'Backlog']].map(([a, l]) => ({ label: l, run: () => run(a) })),
    ]) } }, 'Move to…'),
    h('button.btn.sm', { type: 'button', on: { click: () => run('approve') } }, icon('check'), 'Approve'),
    h('button.btn.sm', { type: 'button', on: { click: () => run('archive') } }, icon('archive'), 'Archive'),
    h('button.btn.ghost.sm', { type: 'button', on: { click: () => { selected.clear(); refresh(); } } }, 'Cancel'));
}

export const tasks = {
  title: () => 'Tasks',
  deps: () => [tasksNow().map(t => [t.slug, t.status, t.name, t.priority, t.project, t.tags, t.due_date, t.waiting_on, t.headless_running, t.terminal_open, t.auto_run_status]), [...S.runs].map(([k, v]) => [k, v.activity?.summary]), [...selected], route.query, S.projectList],
  render(r, ctx) {
    refresh = ctx.refresh;
    const p = prefs();
    const all = tasksNow().filter(t => (t.kind ?? 'regular') === 'regular' && !t.archived);
    const list = filterTasks(all, { project: p.project, priority: p.priority, text: p.q }).filter(recentDone);
    let body;
    if (!all.length) body = empty({ art: 'tasks', title: 'No tasks yet', body: 'Add what you want Claude to do. Tasks in Up next are ready to hand over.', actions: [h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-task')) } }, icon('plus'), 'New task')] });
    else if (p.view === 'board') {
      body = h('div.board', LANES.map(st => lane(st, sortTasks(list.filter(t => t.status === st)))));
      body.addEventListener('dragstart', e => { const c = e.target.closest('.card'); if (!c) return; dragSlug = c.dataset.slug; c.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', dragSlug); });
      body.addEventListener('dragend', e => { e.target.closest?.('.card')?.classList.remove('dragging'); });
    } else {
      const groups = groupTasks(list, p.group);
      const toggle = slug => { if (selected.has(slug)) selected.delete(slug); else selected.add(slug); refresh(); };
      body = h('div', bulkBar(p), groups.length ? groups.map(g => h('div.section', { style: 'margin-top:24px' },
        h('div.section-h', p.group === 'stage' ? [h('span.dot', { class: g.key }), STAGE_NAME[g.key]] : p.group === 'project' ? (g.key ? projectName(g.key) : 'No project') : PRIORITY_NAME[g.key], h('span.count', String(g.tasks.length))),
        h('div.list', g.tasks.map(t => taskRow(t, { hideStatus: p.group === 'stage', hideProject: p.group === 'project', selectable: toggle, selected: selected.has(t.slug) })))))
        : empty({ art: 'search', title: 'Nothing matches', body: 'Try a different filter.' }));
    }
    setTimeout(() => { landed = null; }, 1500);
    return h(`div.page${p.view === 'board' ? '.wide' : ''}`,
      h('div.page-head', h('div', h('h1', 'Tasks'), h('div.sub', `${all.filter(t => t.status !== 'done').length} open · ${all.filter(t => t.status === 'review').length} ready for review`)),
        h('div.actions', h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-task')) } }, icon('plus'), 'New task'))),
      toolbar(p), body);
  },
};
