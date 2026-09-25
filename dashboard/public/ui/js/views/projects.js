import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { S, projectAct, capture, tasksNow, projectHidden, notify, send, edit } from '../state.js';
import { go, setQuery, onRoute } from '../router.js';
import { menu, toast, oops, markdown, empty } from '../ui.js';
import { STAGES, STAGE_NAME, humanTime, groupTasks, parseQuickAdd } from '../logic.js';
import { taskRow } from '../parts.js';
import { memoryList, rememberForm, askText } from './task.js';

const COLORS = { done: 'var(--done)', review: 'var(--review)', 'in-progress': 'var(--progress)', paused: 'var(--paused)', next: 'var(--next)', backlog: 'var(--line-strong)' };

function progress(counts, total) {
  if (!total) return h('div.progress');
  return h('div.progress', { title: STAGES.filter(s => counts[s]).map(s => `${counts[s]} ${STAGE_NAME[s].toLowerCase()}`).join(', ') },
    ['done', 'review', 'in-progress', 'paused', 'next', 'backlog'].filter(s => counts[s]).map(s => h('span', { style: `width:${(counts[s] / total) * 100}%;background:${COLORS[s]}` })));
}

function nextStep(p) {
  if (p.counts.review) return `${p.counts.review} ready for your review`;
  if (p.counts['in-progress']) return `${p.counts['in-progress']} in progress`;
  if (p.counts.next) return `${p.counts.next} up next`;
  if (p.total && p.counts.done === p.total) return 'All done';
  return p.total ? `${p.counts.backlog} in the backlog` : 'No tasks yet';
}

export const projects = {
  title: () => 'Projects',
  deps: () => [S.overview, [...S.runs.keys()]],
  render() {
    const list = S.overview.projects.filter(p => !projectHidden(p.slug));
    return h('div.page',
      h('div.page-head', h('div', h('h1', 'Projects'), h('div.sub', 'Each project is a folder Claude works in, with its own tasks, memory and assistant.')),
        h('div.actions', h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-project')) } }, icon('plus'), 'New project'))),
      list.length ? h('div.grid', list.map(p => h('div.tile', { tabindex: '0', role: 'link', on: { click: () => go(`/project/${p.slug}`), keydown: e => { if (e.key === 'Enter') go(`/project/${p.slug}`); } } },
        h('div.row', h('div.attn-icon', icon('folder')), h('h3.grow', p.name), p.priority === 'high' && h('span.pri.high', 'High')),
        progress(p.counts, p.total),
        h('div', { style: 'color:var(--ink-2)' }, nextStep(p)),
        h('div.meta', { style: 'margin-top:auto' }, `${p.total} task${p.total === 1 ? '' : 's'}`, p.agent?.headless_running && [h('span.sep'), 'Assistant working'], p.schedule && [h('span.sep'), `Checks in every ${p.schedule.every}`]))))
        : empty({ art: 'folder', title: 'No projects yet', body: 'A project is a folder Claude works in: a codebase, a writing folder, anything. Tasks live inside it.', actions: [h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-project')) } }, 'Create a project')] }),
      S.overview.floating.length ? h('div.section', h('div.section-h', 'Tasks without a project', h('span.count', String(S.overview.floating.length))), h('div.list', S.overview.floating.slice(0, 20).map(t => taskRow(t)))) : null);
  },
};

// ---------- one project ----------
const pcache = new Map(); // slug -> { brief, memories, thread }
let mountedP = null;
onRoute(r => { if (r.name !== 'project' || r.id !== mountedP) mountedP = null; });
let editingBrief = false;
const drafts = new Map();

async function fetchProject(slug, parts = ['brief', 'memories']) {
  const c = pcache.get(slug) ?? {};
  const enc = encodeURIComponent(slug);
  await Promise.all(parts.map(async k => {
    try {
      if (k === 'brief') c.brief = (await api(`/api/projects/${enc}/brief`)).brief;
      if (k === 'memories') c.memories = (await api(`/api/memories?project=${enc}`)).memories;
      if (k === 'thread' && c.agent) c.thread = await api(`/api/tasks/${encodeURIComponent(c.agent)}/thread`);
    } catch { /* shown as missing */ }
  }));
  pcache.set(slug, c);
  notify();
}

function assistantTab(p, c) {
  const intro = h('div.callout', { style: 'margin-bottom:16px' }, icon('sparkle'), h('div',
    h('strong', 'The project assistant'),
    h('div', 'One permanent Claude session for this whole project. Ask it anything that isn’t one task’s job: it decides whether your ask belongs to an existing task or needs a new one, and tells you what it did.')));
  if (!c.agent) {
    return h('div', intro, h('button.btn.primary', { type: 'button', on: { click: async e => {
      e.currentTarget.disabled = true;
      try { const r = await api(`/api/projects/${encodeURIComponent(p.slug)}/action`, 'POST', { action: 'ensure-agent' }); c.agent = r.agent; await fetchProject(p.slug, ['thread']); } catch (err) { oops(err, 'Could not start the assistant'); }
    } } }, icon('sparkle'), 'Start the assistant'));
  }
  const msgs = c.thread?.messages ?? [];
  const ta = h('textarea', { rows: '3', maxlength: '380', placeholder: `Ask the ${p.name} assistant…`, value: drafts.get(p.slug) ?? '', dataset: { key: 'p-composer' }, 'aria-label': 'Message the assistant' });
  const fan = h('input', { type: 'checkbox', id: 'fanout' });
  ta.addEventListener('input', () => drafts.set(p.slug, ta.value));
  const doSend = async () => {
    const text = ta.value.trim();
    if (!text) return;
    try {
      if (fan.checked) await api(`/api/tasks/${encodeURIComponent(c.agent)}/message`, 'POST', { text, mode: c.thread?.live ? 'queue' : 'headless', confirm: true, fanout: true });
      else await send(c.agent, text);
      drafts.delete(p.slug); toast(fan.checked ? 'Sent to the assistant and every open task.' : 'Sent to the assistant.');
      await fetchProject(p.slug, ['thread']);
    } catch (err) { oops(err, 'Could not send'); }
  };
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); doSend(); } });
  return h('div', intro,
    msgs.length ? h('div.chat', msgs.map(m => [h('div.msg', { class: m.direction }, m.body.replace(/\s*\(via focusflow:[\s\S]*\)\s*$/, '')), h('div.msg-meta', { class: m.direction }, `${m.direction === 'out' ? 'You' : 'Assistant'} · ${humanTime(m.at)}`)])) : null,
    h('div.composer', ta, h('div.composer-foot', h('label.hint', { style: 'display:flex;gap:6px;align-items:center' }, fan, 'Also tell every open task in this project'), h('button.btn.primary.sm', { type: 'button', on: { click: doSend } }, icon('send'), 'Send'))));
}

function tasksTab(p) {
  const all = tasksNow().filter(t => t.project === p.slug && (t.kind ?? 'regular') === 'regular' && !t.archived);
  const input = h('input.input', { placeholder: `Add a task to ${p.name}…`, maxlength: '200', dataset: { key: 'p-quick' }, 'aria-label': 'Add a task' });
  input.addEventListener('keydown', async e => {
    if (e.key !== 'Enter') return;
    const q = parseQuickAdd(input.value, []);
    if (!q.name) return;
    try { await capture({ name: q.name, project: p.slug, priority: q.priority ?? 'medium', stage: e.shiftKey ? 'backlog' : 'next' }); toast(`Added to ${e.shiftKey ? 'the backlog' : 'Up next'}.`); input.value = ''; } catch (err) { oops(err, 'Could not add that'); }
  });
  const groups = groupTasks(all, 'stage');
  return h('div', h('div', { style: 'margin-bottom:24px' }, input, h('div', { style: 'font-size:12px;color:var(--muted);margin-top:6px' }, 'Enter adds to Up next · Shift+Enter adds to the backlog')),
    groups.length ? groups.map(g => h('div.section', { style: 'margin-top:20px' }, h('div.section-h', h('span.dot', { class: g.key }), STAGE_NAME[g.key], h('span.count', String(g.tasks.length))), h('div.list', g.tasks.map(t => taskRow(t, { hideProject: true, hideStatus: true })))))
      : h('div', { style: 'color:var(--muted)' }, 'No tasks yet. Add the first one above, or ask the assistant to plan the work.'));
}

function briefTab(p, c) {
  if (editingBrief) {
    const ta = h('textarea.textarea', { rows: '18', value: c.brief ?? '', style: 'font:14px/1.6 var(--mono)', dataset: { key: 'p-brief' } });
    return h('div', ta, h('div.row', { style: 'margin-top:12px' },
      h('button.btn.primary', { type: 'button', on: { click: async () => { try { await api(`/api/projects/${encodeURIComponent(p.slug)}/brief`, 'POST', { text: ta.value }); editingBrief = false; toast('Saved.'); await fetchProject(p.slug, ['brief']); } catch (err) { oops(err, 'Could not save'); } } } }, 'Save'),
      h('button.btn', { type: 'button', on: { click: () => { editingBrief = false; notify(); } } }, 'Cancel')));
  }
  return h('div', h('div.row', { style: 'margin-bottom:12px' }, h('span.grow', { style: 'color:var(--muted);font-size:13px' }, 'What this project is for. Every session in the project reads it.'), h('button.btn.sm', { type: 'button', on: { click: () => { editingBrief = true; notify(); } } }, icon('edit'), 'Edit')),
    c.brief ? markdown(c.brief) : h('div', { style: 'color:var(--muted)' }, 'No brief yet.'));
}

function projectMenu(anchor, p) {
  menu(anchor, [
    { label: 'Rename', icon: 'edit', run: async () => { const v = await askText({ title: 'Rename project', label: 'Name', value: p.name }); if (v && v.trim() && v.trim() !== p.name) { try { await edit('projects', p.slug, { name: v.trim() }); toast('Renamed.'); } catch (err) { oops(err); } } } },
    { header: 'Priority' },
    ...['high', 'medium', 'low'].map(v => ({ label: v[0].toUpperCase() + v.slice(1), run: async () => { try { await edit('projects', p.slug, { priority: v }); toast('Priority updated.'); } catch (err) { oops(err); } } })),
    { header: 'Check-ins' },
    ...(p.schedule ? [{ label: 'Stop scheduled check-ins', icon: 'x', run: () => projectAct(p.slug, 'unschedule') }]
      : ['12h', '1d', '3d', '1w'].map(v => ({ label: `Assistant checks in every ${v}`, icon: 'clock', run: () => projectAct(p.slug, 'schedule', { every: v }) }))),
    'sep',
    { label: 'Open assistant in terminal', icon: 'terminal', run: () => projectAct(p.slug, 'open-agent') },
    { label: 'Pause everything', icon: 'pause', run: () => projectAct(p.slug, 'pause') },
    { label: 'Archive project', icon: 'archive', danger: true, run: () => { projectAct(p.slug, 'archive'); go('/projects'); } },
  ]);
}

export const project = {
  title: r => S.overview.projects.find(p => p.slug === r.id)?.name ?? 'Project',
  deps: r => [S.overview.projects.find(p => p.slug === r.id), tasksNow().filter(t => t.project === r.id).map(t => [t.slug, t.status, t.name, t.updated_at, t.headless_running]), pcache.get(r.id), r.query, editingBrief],
  render(r, ctx) {
    const p = S.overview.projects.find(x => x.slug === r.id);
    if (!p) return h('div.page', S.loaded ? empty({ art: 'folder', title: 'Project not found', body: 'It may be archived.', actions: [h('a.btn', { href: '#/projects' }, 'All projects')] }) : h('div.empty', 'Loading…'));
    const c = pcache.get(p.slug) ?? {};
    c.agent = p.agent?.slug ?? c.agent;
    pcache.set(p.slug, c);
    if (mountedP !== p.slug) { mountedP = p.slug; fetchProject(p.slug, ['brief', 'memories', 'thread']); }
    const poll = setInterval(() => { if (r.query.tab === 'assistant') fetchProject(p.slug, ['thread']); }, 5000);
    ctx.onCleanup(() => clearInterval(poll));
    const tab = r.query.tab ?? 'tasks';
    const rules = (c.memories ?? []).filter(m => m.kind === 'rule');
    const tabs = [['tasks', 'Tasks', p.total], ['assistant', 'Assistant'], ['rules', 'House rules', rules.length || null], ['brief', 'Brief'], ['memory', 'Memory', (c.memories?.length ?? 0) - rules.length || null]];
    return h('div.page',
      h('div.crumbs', h('a', { href: '#/projects' }, 'Projects')),
      h('div.page-head',
        h('div.grow', h('h1', p.name), h('div.sub', { style: 'font-size:15px' }, nextStep(p), p.work_dir && h('div', { style: 'font-size:12px;color:var(--faint);margin-top:4px;font-family:var(--mono)', title: 'Claude works in this folder' }, p.work_dir.replace(/^\/Users\/[^/]+/, '~')))),
        h('div.actions',
          h('button.btn', { type: 'button', on: { click: () => setQuery({ tab: 'assistant' }) } }, icon('sparkle'), 'Ask the project'),
          h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-task', { detail: { project: p.slug } })) } }, icon('plus'), 'New task'),
          h('button.btn.icon', { type: 'button', 'aria-label': 'Project actions', on: { click: e => projectMenu(e.currentTarget, p) } }, icon('more')))),
      h('div', { style: 'max-width:520px' }, progress(p.counts, p.total)),
      h('div.tabs', { role: 'tablist' }, tabs.map(([id, label, n]) => h('button', { type: 'button', role: 'tab', 'aria-selected': String(tab === id), on: { click: () => setQuery({ tab: id === 'tasks' ? '' : id }) } }, label, n ? h('span.badge.soft', String(n)) : null))),
      tab === 'assistant' ? assistantTab(p, c) : tab === 'brief' ? briefTab(p, c) : tab === 'rules'
        ? h('div', h('div.callout.good', { style: 'margin-bottom:16px' }, icon('shield'), h('span', 'House rules are your standards for this project. Every session is told to follow them and to check its work against them. They grow from the changes you request at review.')),
          rememberForm({ project: p.slug, kind: 'rule', onDone: () => fetchProject(p.slug, ['memories']) }),
          rules.length ? memoryList(rules, { onChange: () => fetchProject(p.slug, ['memories']), scopeLabel: false }) : h('div', { style: 'color:var(--muted)' }, 'No house rules yet. When you request changes, tick “Make this a house rule”.'))
        : tab === 'memory'
        ? h('div', rememberForm({ project: p.slug, onDone: () => fetchProject(p.slug, ['memories']) }), c.memories?.filter(m => m.kind !== 'rule').length ? memoryList(c.memories.filter(m => m.kind !== 'rule'), { onChange: () => fetchProject(p.slug, ['memories']) }) : h('div', { style: 'color:var(--muted)' }, 'Nothing remembered for this project yet. Answers you give its tasks show up here.'))
        : tasksTab(p));
  },
};
