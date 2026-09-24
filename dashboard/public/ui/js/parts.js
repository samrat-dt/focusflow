import { h } from './dom.js';
import { icon } from './icons.js';
import { menu, toast } from './ui.js';
import { STAGE_NAME, PRIORITY_NAME, humanTime, liveLine, primaryAction, duration } from './logic.js';
import { S, act, openTerminal, taskBy } from './state.js';
import { go } from './router.js';

export const projectName = slug => S.overview.projects.find(p => p.slug === slug)?.name ?? S.projectList.find(p => p.slug === slug)?.name ?? slug;

export function statusLine(t) {
  const live = t.headless_running || (t.terminal_open && t.working);
  return h('span.status', h('span.dot', { class: `${t.status}${live ? ' pulse' : ''}` }), STAGE_NAME[t.status] ?? t.status);
}

export function metaLine(t, { hideProject = false, hideStatus = false } = {}) {
  const bits = [];
  if (!hideStatus) bits.push(statusLine(t));
  if (t.project && !hideProject) bits.push(h('span', projectName(t.project)));
  if (t.priority === 'high') bits.push(h('span.pri.high', 'High'));
  if (t.due_date) bits.push(h('span', { class: t.signals?.due === 'overdue' ? 'pri high' : '' }, t.signals?.due === 'overdue' ? `Overdue · ${t.due_date}` : `Due ${t.due_date}`));
  if (t.waiting_on) bits.push(h('span', `Waiting on ${t.waiting_on}`));
  for (const tag of (t.tags ?? []).slice(0, 3)) bits.push(h('span.tag', `#${tag}`));
  return h('div.meta', bits.flatMap((b, i) => (i ? [h('span.sep'), b] : [b])));
}

export function liveNote(t) {
  const text = liveLine(t, S.runs.get(t.slug));
  if (!text) return null;
  const run = S.runs.get(t.slug);
  const failed = t.auto_run_status === 'dead';
  return h('div.card-live', { style: failed ? 'color:var(--danger)' : '' }, icon(failed ? 'alert' : t.headless_running ? 'bolt' : 'terminal'),
    h('span', text + (t.headless_running && run?.elapsed_seconds != null ? ` · ${duration(run.elapsed_seconds)}` : '')));
}

// Every task gets the same menu, wherever it appears.
export function taskMenu(anchor, t) {
  const moves = [
    ['next', 'Up next', 'next'], ['unpause', 'In progress', 'in-progress'], ['pause', 'Paused', 'paused'],
    ['review', 'Ready for review', 'review'], ['backlog', 'Backlog', 'backlog'],
  ].filter(([, , st]) => st !== t.status && !(t.status === 'done' && st !== 'in-progress'));
  const agent = t.kind === 'project_agent';
  menu(anchor, [
    { label: 'Open', icon: 'chevronRight', run: () => go(`/task/${t.slug}`) },
    !t.archived && { label: t.terminal_open ? 'Show terminal' : 'Open in terminal', icon: 'terminal', run: () => openTerminal(t.slug) },
    t.headless_running && { label: 'Stop background run', icon: 'stop', run: () => act(t.slug, 'pause') },
    t.status === 'review' && !agent && { label: 'Approve', icon: 'check', run: () => act(t.slug, 'approve') },
    !t.archived && !agent && { header: 'Move to' },
    ...(!t.archived && !agent ? moves.map(([a, label, st]) => ({ label, icon: null, run: () => act(t.slug, a), dot: st })) : []),
    'sep',
    { label: 'Copy link', icon: 'external', run: () => { navigator.clipboard?.writeText(`${location.origin}/#/task/${t.slug}`); toast('Link copied.'); } },
    !agent && (t.archived ? { label: 'Restore', icon: 'undo', run: () => act(t.slug, 'unarchive') } : { label: 'Archive', icon: 'archive', danger: true, run: () => act(t.slug, 'archive') }),
  ]);
}

export function moreButton(t) {
  return h('button.btn.ghost.icon.sm.more-btn', { type: 'button', 'aria-label': `More actions for ${t.name}`, on: { click: e => { e.stopPropagation(); taskMenu(e.currentTarget, taskBy(t.slug) ?? t); } } }, icon('more'));
}

export function primaryButton(t, { small = true } = {}) {
  const p = primaryAction(t);
  if (p.action === 'message') return h(`button.btn${small ? '.sm' : ''}`, { type: 'button', on: { click: e => { e.stopPropagation(); go(`/task/${t.slug}`, { focus: 'composer' }); } } }, icon('message'), p.label);
  return h(`button.btn${small ? '.sm' : ''}${p.action === 'approve' ? '.primary' : ''}`, { type: 'button', on: { click: e => { e.stopPropagation(); act(t.slug, p.action); } } }, p.action === 'approve' && icon('check'), p.label);
}

export function taskRow(t, { hideProject = false, hideStatus = false, selectable = null, selected = false } = {}) {
  const row = h('div.item', { tabindex: '0', role: 'link', dataset: { key: `task:${t.slug}` }, class: selected ? 'selected' : '',
    on: { click: () => go(`/task/${t.slug}`), keydown: e => { if (e.key === 'Enter') go(`/task/${t.slug}`); } } },
    selectable && h('button.check', { type: 'button', role: 'checkbox', 'aria-checked': String(selected), 'aria-label': `Select ${t.name}`, on: { click: e => { e.stopPropagation(); selectable(t.slug); } } }, selected && icon('check')),
    h('div.body', h('div.title', t.name), h('div.sub', metaLine(t, { hideProject, hideStatus })), liveNote(t)),
    h('div.end', h('span.reveal', primaryButton(t)), h('span.meta', humanTime(t.updated_at)), moreButton(t)));
  return row;
}

export function taskCard(t, { draggable = true, landed = false } = {}) {
  const card = h('div.card', { tabindex: '0', draggable: draggable && !t.archived && t.kind !== 'project_agent' ? 'true' : undefined, dataset: { slug: t.slug, key: `card:${t.slug}` }, class: landed ? 'landed' : '',
    on: { click: () => go(`/task/${t.slug}`), keydown: e => { if (e.key === 'Enter') go(`/task/${t.slug}`); } } },
    h('div.card-title', t.name), metaLine(t, { hideStatus: true }), liveNote(t),
    t.status === 'review' && h('div', { style: 'margin-top:10px' }, primaryButton(t)),
    moreButton(t));
  return card;
}

export { STAGE_NAME, PRIORITY_NAME };
