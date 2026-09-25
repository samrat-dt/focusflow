import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { S, act, edit, send, openTerminal, taskBy, notify, load } from '../state.js';
import { route, go, setQuery, routeOpts, onRoute } from '../router.js';
import { menu, toast, oops, markdown, dialog, empty } from '../ui.js';
import { STAGE_NAME, PRIORITY_NAME, humanTime, duration, primaryAction } from '../logic.js';
import { statusLine, projectName, taskMenu, displayName, requestChanges } from '../parts.js';

const cache = new Map(); // slug -> { detail, thread, run, memories }
const drafts = new Map();
let editingBrief = null;
let mounted = null;
const readThrough = new Map();
onRoute(r => { if (r.name !== 'task' || r.id !== mounted) mounted = null; });

async function fetchAll(slug, parts = ['detail', 'thread', 'run', 'memories']) {
  const c = cache.get(slug) ?? {};
  const enc = encodeURIComponent(slug);
  const get = {
    detail: () => api(`/api/tasks/${enc}`),
    thread: () => api(`/api/tasks/${enc}/thread`),
    run: () => api(`/api/tasks/${enc}/run`),
    memories: () => api(`/api/memories?task=${enc}`).then(r => r.memories),
    proof: () => api(`/api/tasks/${enc}/proof`),
  };
  if (parts.includes('detail')) {
    try { c.detail = await get.detail(); c.error = null; } catch (err) { c.error = err; cache.set(slug, c); notify(); return; } // no such task: nothing else to ask
  }
  await Promise.all(parts.filter(k => k !== 'detail').map(async k => { try { c[k] = await get[k](); } catch { /* shown as missing */ } }));
  cache.set(slug, c);
  notify();
}

function markRead(slug, thread) {
  const last = thread?.messages?.filter(m => m.unread).at(-1);
  if (!last || readThrough.get(slug) === last.id) return;
  readThrough.set(slug, last.id);
  api(`/api/tasks/${encodeURIComponent(slug)}/read`, 'POST', {}).then(() => load()).catch(() => {});
}

// ---------- properties ----------
export function askText({ title, label, value = '', hint, placeholder }) {
  return new Promise(resolve => {
    dialog((box, close) => {
      const input = h('input.input', { value, placeholder, autofocus: true, maxlength: '200' });
      const done = v => { close(); resolve(v); };
      input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); done(input.value); } });
      box.append(h('h2', title), h('label.field', h('span', label), input, hint && h('span.hint', hint)),
        h('div.dialog-foot', h('button.btn', { type: 'button', on: { click: () => done(null) } }, 'Cancel'), h('button.btn.primary', { type: 'button', on: { click: () => done(input.value) } }, 'Save')));
    }, { label: title });
  });
}

async function save(slug, body, msg = 'Saved.') {
  try { const r = await edit('tasks', slug, body); if (r.changed !== false) toast(msg); await fetchAll(slug, ['detail']); } catch (err) { oops(err, 'Could not save'); }
}

function props(t, c) {
  const agent = t.kind === 'project_agent';
  const run = c.run;
  const session = t.headless_running ? `Working in the background${run?.elapsed_seconds != null ? ` · ${duration(run.elapsed_seconds)}` : ''}`
    : t.terminal_open ? 'Open in a terminal' : t.has_session ? `Idle · last active ${humanTime(t.session_last_resumed || t.session_started)}` : 'Not started yet';
  const row = (label, ic, value) => [h('dt', icon(ic), label), h('dd', value)];
  const pbtn = (text, fn, emptyText) => h('button.prop-btn', { type: 'button', class: text ? '' : 'unset', on: { click: e => fn(e.currentTarget) } }, text || emptyText);
  const due = h('input.input', { type: 'date', value: /^\d{4}-\d{2}-\d{2}$/.test(t.due_date ?? '') ? t.due_date : '', style: 'width:auto;min-height:30px;padding:3px 8px', 'aria-label': 'Due date', on: { change: e => save(t.slug, { due: e.target.value || null }, e.target.value ? 'Due date set.' : 'Due date cleared.') } });
  return h('dl.props',
    row('Stage', 'route', agent ? h('span', 'Project assistant (always on)') : pbtn(statusLine(t), el => menu(el, [
      ['next', 'Up next', 'next'], ['unpause', 'In progress', 'in-progress'], ['pause', 'Paused', 'paused'], ['review', 'Ready for review', 'review'], ['backlog', 'Backlog', 'backlog'],
      ...(t.status === 'review' ? [['approve', 'Done (approve)', 'done']] : []),
    ].filter(([, , st]) => st !== t.status).map(([a, l]) => ({ label: l, run: () => act(t.slug, a) }))), 'Set stage')),
    row('Priority', 'flag', pbtn(PRIORITY_NAME[t.priority], el => menu(el, ['high', 'medium', 'low'].map(v => ({ label: PRIORITY_NAME[v], run: () => save(t.slug, { priority: v }, 'Priority updated.') }))), 'Set priority')),
    row('Project', 'folder', t.project ? h('a', { href: `#/project/${t.project}` }, projectName(t.project)) : h('span', { style: 'color:var(--faint)' }, 'None')),
    row('Due', 'calendar', h('span.row', due, t.signals?.due === 'overdue' && h('span.tag.danger', 'Overdue'))),
    row('Waiting on', 'clock', pbtn(t.waiting_on, async () => { const v = await askText({ title: 'Waiting on', label: 'Who or what is this waiting on?', value: t.waiting_on ?? '', hint: 'Leave empty if it isn’t waiting on anything.', placeholder: 'e.g. Legal sign-off' }); if (v !== null) save(t.slug, { waiting: v.trim() || null }); }, 'Nobody')),
    row('Tags', 'hash', pbtn((t.tags ?? []).map(x => `#${x}`).join('  '), async () => { const v = await askText({ title: 'Tags', label: 'Tags, separated by commas', value: (t.tags ?? []).join(', '), placeholder: 'design, launch' }); if (v !== null) save(t.slug, { tags: v.split(',').map(x => x.trim()).filter(Boolean) }, 'Tags updated.'); }, 'Add tags')),
    row('Session', 'terminal', h('span', { style: 'color:var(--ink-2)' }, session)));
}

// ---------- review panel: proof of work, and the two decisions ----------
let showFiles = false;
function reviewPanel(t, c) {
  if (t.status !== 'review' || t.kind === 'project_agent') return null;
  const p = c.proof;
  if (!p) { fetchAll(t.slug, ['proof']); return h('div.callout', { style: 'margin-top:8px' }, icon('clock'), 'Gathering what changed…'); }
  const g = p.git;
  return h('div', { style: 'margin-top:12px;border:1px solid var(--line);border-radius:var(--r-lg);background:var(--surface);padding:20px 24px' },
    h('div.row', { style: 'margin-bottom:12px' }, h('div.attn-icon.review', icon('check')), h('div.grow', h('strong', 'Ready for your review'), h('div', { style: 'color:var(--muted);font-size:13px' }, 'Check the work, then approve it or send it back with what to change.')),
      h('button.btn', { type: 'button', on: { click: () => requestChanges(t, { onDone: () => fetchAll(t.slug) }) } }, 'Request changes'),
      h('button.btn.primary', { type: 'button', on: { click: () => act(t.slug, 'approve') } }, icon('check'), 'Approve')),
    p.summary && h('div', { style: 'margin-top:8px' }, h('div.attn-kind', `What Claude says · ${humanTime(p.summary.at)}`), h('div.attn-detail.quote', p.summary.text)),
    p.note && h('div', { style: 'margin-top:14px' }, h('div.attn-kind', 'Latest progress note'), markdown(p.note.text)),
    g && h('div', { style: 'margin-top:14px' },
      h('div.attn-kind', `In the project folder since this task started${g.branch ? ` · branch ${g.branch}` : ''}`),
      g.commits.length ? h('ul', { style: 'margin:6px 0;padding-left:20px' }, g.commits.slice(0, 6).map(cm => h('li', h('code', cm.hash), ' ', cm.subject))) : h('div', { style: 'color:var(--muted);margin-top:4px' }, 'No commits.'),
      g.files.length ? h('div', h('button.btn.ghost.sm', { type: 'button', on: { click: () => { showFiles = !showFiles; notify(); } } }, `${g.files.length} file${g.files.length === 1 ? '' : 's'} changed${g.uncommitted ? ` (${g.uncommitted} not committed)` : ''}`, icon(showFiles ? 'chevronDown' : 'chevronRight')),
        showFiles && h('pre', { style: 'font:12px/1.6 var(--mono);background:var(--sunken);padding:8px 12px;border-radius:8px;max-height:200px;overflow:auto' }, g.files.join('\n'))) : null,
      h('div', { style: 'font-size:12px;color:var(--faint);margin-top:4px' }, 'Other tasks in the same folder may have contributed some of these.')),
    p.rules?.length ? h('div', { style: 'margin-top:14px' }, h('div.attn-kind', 'Your house rules: does it follow them?'), h('ul', { style: 'margin:6px 0;padding-left:20px' }, p.rules.map(r => h('li', r.body)))) : null);
}

// ---------- run panel ----------
let showLog = false;
function runPanel(t, c) {
  const r = c.run;
  if (!t.headless_running && t.auto_run_status !== 'dead') return null;
  const failed = t.auto_run_status === 'dead';
  return h('div.callout.run', { class: failed ? 'failed' : '', style: 'margin:8px 0 0' },
    icon(failed ? 'alert' : 'bolt'),
    h('div.grow',
      h('strong', failed ? 'The background run stopped unexpectedly' : `Claude is working on this in the background${r?.elapsed_seconds != null ? ` · ${duration(r.elapsed_seconds)}` : ''}`),
      h('div', { style: 'margin-top:4px' }, r?.activity ? `${failed ? 'Last step' : 'Now'}: ${r.activity.summary}` : failed ? 'Send a message to try again, or open it in a terminal to see what happened.' : 'Getting started…'),
      r?.log_tail && h('div', { style: 'margin-top:6px' }, h('button.btn.ghost.sm', { type: 'button', on: { click: () => { showLog = !showLog; notify(); } } }, showLog ? 'Hide log' : 'Show log'), showLog && h('pre', r.log_tail))),
    !failed && h('button.btn.sm', { type: 'button', on: { click: () => act(t.slug, 'pause') } }, icon('stop'), 'Stop'));
}

// ---------- tabs ----------
function conversation(t, c) {
  const th = c.thread;
  const msgs = th?.messages ?? [];
  const closed = t.archived || t.status === 'done';
  const background = th && !th.live && (!th.has_session || th.harness === 'claude');
  const ta = h('textarea', { rows: '3', maxlength: '380', placeholder: closed ? '' : `Message ${t.kind === 'project_agent' ? 'the assistant' : `Claude about “${t.name}”`}…`, 'aria-label': 'Message', value: drafts.get(t.slug) ?? '', dataset: { key: 'composer' } });
  const count = h('span', `${(drafts.get(t.slug) ?? '').length}/380`);
  const sendBtn = h('button.btn.primary.sm', { type: 'button' }, icon('send'), 'Send');
  const doSend = async () => {
    const text = ta.value.trim();
    if (!text) return ta.focus();
    if (text.startsWith('-')) return toast('A message cannot start with “-”.', { error: true });
    sendBtn.disabled = true;
    try {
      const r = await send(t.slug, text);
      drafts.delete(t.slug); ta.value = '';
      toast(r.background ? 'Sent. Claude is working on it in the background.' : 'Sent to the open session.');
      await fetchAll(t.slug, ['thread', 'run', 'memories']);
    } catch (err) { oops(err, 'Could not send'); }
    sendBtn.disabled = false;
  };
  ta.addEventListener('input', () => { drafts.set(t.slug, ta.value); count.textContent = `${ta.value.length}/380`; });
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); doSend(); } });
  sendBtn.addEventListener('click', doSend);
  if (routeOpts.focus === 'composer') setTimeout(() => ta.focus(), 50);
  const receipt = th?.awaiting_reply ? (th.delivery === 'delivered' ? 'Delivered · waiting for a reply' : 'Sent · waiting for the session to pick it up') : null;
  return h('div',
    msgs.length ? h('div.chat', msgs.map(m => [
      h('div.msg', { class: `${m.direction}${m.urgent ? ' urgent' : ''}` }, m.body.replace(/\s*\(via focusflow:[\s\S]*\)\s*$/, '')),
      h('div.msg-meta', { class: m.direction }, `${m.direction === 'out' ? 'You' : 'Claude'} · ${humanTime(m.at)}${m.urgent ? ' · urgent' : ''}`),
    ])) : h('div', { style: 'color:var(--muted);padding:8px 0' }, closed ? 'No messages.' : 'No messages yet. Tell Claude what you want done. Everything you say here is remembered for this task.'),
    receipt && h('div.msg-meta', { style: 'text-align:right;margin-top:6px' }, receipt),
    !closed && h('div.composer', ta, h('div.composer-foot',
      h('span.hint', background ? 'Claude will work on this in the background. No terminal opens.' : th?.live ? 'Goes to the session that is open in your terminal.' : 'Delivered the next time this task runs.'),
      count, sendBtn)),
    closed && h('div.callout', { style: 'margin-top:16px' }, icon('info'), h('span', t.archived ? 'This task is archived. Restore it to continue.' : 'This task is done. Reopen it to continue the conversation.')));
}

const MEMORY_KIND = { answer: 'You answered', instruction: 'You said', decision: 'Decision', note: 'Note', rule: 'House rule' };
export function memoryList(list, { onChange, scopeLabel = true } = {}) {
  const op = async (m, o) => { try { await api(`/api/memories/${m.id}/${o}`, 'POST', {}); toast({ pin: 'Pinned.', unpin: 'Unpinned.', promote: 'Now applies more widely.', forget: 'Forgotten.' }[o]); } catch (err) { oops(err); } onChange?.(); };
  if (!list.length) return null;
  return h('div.list', list.map(m => h('div.item', { style: 'cursor:default;align-items:flex-start' },
    h('div.attn-icon', { style: 'width:28px;height:28px' }, icon(m.kind === 'rule' ? 'shield' : m.pinned ? 'pin' : m.kind === 'answer' ? 'message' : 'memory')),
    h('div.body',
      h('div.attn-kind', [MEMORY_KIND[m.kind] ?? m.kind, scopeLabel && (m.scope === 'global' ? 'Everywhere' : m.scope === 'project' ? `Project: ${projectName(m.project)}` : m.task ? `Task: ${taskBy(m.task)?.name ?? m.task}` : null), humanTime(m.created_at)].filter(Boolean).join(' · ')),
      m.question && h('div', { style: 'color:var(--muted);font-size:13px;margin-top:2px' }, `Q: ${m.question}`),
      h('div', { style: 'margin-top:2px;white-space:pre-wrap' }, m.body)),
    h('div.end', h('button.btn.ghost.icon.sm', { type: 'button', 'aria-label': 'Memory actions', on: { click: e => menu(e.currentTarget, [
      m.kind !== 'rule' && { label: m.pinned ? 'Unpin' : 'Pin (always include)', icon: 'pin', run: () => op(m, m.pinned ? 'unpin' : 'pin') },
      m.scope !== 'global' && { label: m.scope === 'task' && m.project ? 'Apply to the whole project' : 'Apply everywhere', icon: 'sparkle', run: () => op(m, 'promote') },
      'sep',
      { label: 'Forget', icon: 'trash', danger: true, run: () => op(m, 'forget') },
    ]) } }, icon('more'))))));
}

export function rememberForm({ task, project, onDone, kind = null }) {
  const input = h('input.input', { placeholder: kind === 'rule' ? 'Add a house rule, e.g. Every change needs a test' : 'Teach Claude something to remember…', maxlength: '380', 'aria-label': 'Remember something', dataset: { key: 'remember' } });
  const scope = h('select.select', { style: 'width:auto', 'aria-label': 'Applies to' },
    kind !== 'rule' && task && h('option', { value: 'task' }, 'This task'), project && h('option', { value: 'project' }, 'Whole project'), h('option', { value: 'global' }, 'Everywhere'));
  const go2 = async () => {
    const text = input.value.trim();
    if (!text) return;
    try { await api('/api/memories/new', 'POST', { text, scope: scope.value, task, project, ...(kind ? { kind } : {}) }); toast(kind === 'rule' ? 'House rule added. Every future session will follow it.' : 'Remembered. Every future session will know this.'); input.value = ''; onDone?.(); }
    catch (err) { oops(err, 'Could not remember that'); }
  };
  input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); go2(); } });
  return h('div.row', { style: 'margin-bottom:16px' }, input, scope, h('button.btn', { type: 'button', on: { click: go2 } }, kind === 'rule' ? 'Add rule' : 'Remember'));
}

function memoryTab(t, c) {
  const list = c.memories ?? [];
  return h('div',
    h('div.callout.good', { style: 'margin-bottom:16px' }, icon('memory'), h('span', 'Everything you tell this task is remembered and handed to every future session, so you never have to say it twice. Pin what matters most; apply it to the whole project when it’s true everywhere.')),
    rememberForm({ task: t.slug, project: t.project, onDone: () => fetchAll(t.slug, ['memories']) }),
    list.length ? memoryList(list, { onChange: () => fetchAll(t.slug, ['memories']), scopeLabel: true }) : h('div', { style: 'color:var(--muted)' }, 'Nothing remembered yet. Answers and instructions you send will appear here.'));
}

function briefTab(t, c, kind = 'tasks') {
  const brief = c.detail?.brief;
  if (editingBrief === t.slug) {
    const ta = h('textarea.textarea', { rows: '18', value: brief ?? '', style: 'font:14px/1.6 var(--mono)', dataset: { key: 'brief' }, 'aria-label': 'Brief' });
    return h('div', ta, h('div.row', { style: 'margin-top:12px' },
      h('button.btn.primary', { type: 'button', on: { click: async () => { try { await api(`/api/${kind}/${encodeURIComponent(t.slug)}/brief`, 'POST', { text: ta.value }); toast('Brief saved. The next session will read it.'); editingBrief = null; await fetchAll(t.slug, ['detail']); } catch (err) { oops(err, 'Could not save the brief'); } } } }, 'Save'),
      h('button.btn', { type: 'button', on: { click: () => { editingBrief = null; notify(); } } }, 'Cancel'),
      h('span', { style: 'color:var(--muted);font-size:13px' }, 'Markdown. Claude reads this first in every session.')));
  }
  return h('div',
    h('div.row', { style: 'margin-bottom:12px' }, h('span.grow', { style: 'color:var(--muted);font-size:13px' }, 'The brief is what Claude reads first: what, why, and what done looks like.'), h('button.btn.sm', { type: 'button', on: { click: () => { editingBrief = t.slug; notify(); } } }, icon('edit'), 'Edit')),
    brief ? markdown(brief) : h('div', { style: 'color:var(--muted)' }, 'No brief yet.'));
}

function notesTab(t, c) {
  const notes = c.detail?.notes ?? [];
  const ta = h('textarea.textarea', { rows: '3', placeholder: 'Add a progress note…', value: drafts.get(`note:${t.slug}`) ?? '', dataset: { key: 'note' }, 'aria-label': 'New note' });
  ta.addEventListener('input', () => drafts.set(`note:${t.slug}`, ta.value));
  const add = async () => {
    if (!ta.value.trim()) return;
    try { await api(`/api/tasks/${encodeURIComponent(t.slug)}/note`, 'POST', { text: ta.value }); drafts.delete(`note:${t.slug}`); toast('Note added.'); await fetchAll(t.slug, ['detail']); } catch (err) { oops(err, 'Could not add the note'); }
  };
  return h('div', h('div', { style: 'margin-bottom:24px' }, ta, h('div.row', { style: 'margin-top:8px;justify-content:flex-end' }, h('button.btn', { type: 'button', on: { click: add } }, 'Add note'))),
    notes.length ? notes.map(n => h('div', { style: 'border-top:1px solid var(--line);padding:16px 0' }, h('div', { style: 'color:var(--muted);font-size:13px;margin-bottom:6px' }, n.date ?? n.file), markdown(n.text))) : h('div', { style: 'color:var(--muted)' }, 'No progress notes yet. Sessions write them as they go.'));
}

// ---------- page ----------
export const task = {
  title: r => displayName(taskBy(r.id)) || 'Task',
  deps: r => [taskBy(r.id), cache.get(r.id), r.query, editingBrief, showLog, showFiles, drafts.size],
  render(r, ctx) {
    const slug = r.id;
    const c = cache.get(slug);
    if (mounted !== slug) { mounted = slug; if (!c) cache.set(slug, {}); fetchAll(slug); }
    const poll = setInterval(() => { if (document.visibilityState === 'visible') fetchAll(slug, ['thread', 'run']); }, 5000);
    ctx.onCleanup(() => clearInterval(poll));
    const t = taskBy(slug) ?? c?.detail?.task;
    if (!t) return h('div.page', c?.error ? empty({ art: 'alert', title: 'This task doesn’t exist', body: 'It may have been renamed or removed.', actions: [h('a.btn', { href: '#/tasks' }, 'Back to tasks')] }) : h('div.empty', 'Loading…'));
    const cc = cache.get(slug) ?? {};
    if (cc.thread) markRead(slug, cc.thread);
    const tab = r.query.tab ?? 'conversation';
    const unread = cc.thread?.messages?.filter(m => m.unread).length ?? 0;
    const tabs = [['conversation', 'Conversation', unread || null], ['memory', 'Memory', cc.memories?.length || null], ['brief', 'Brief'], ['notes', 'Notes', cc.detail?.notes?.length || null]];
    const agent = t.kind === 'project_agent';
    const title = agent ? h('h1', displayName(t)) : h('input.title-edit', { value: t.name, 'aria-label': 'Task name', maxlength: '200', dataset: { key: 'title' } });
    if (!agent) {
      title.addEventListener('keydown', e => { if (e.key === 'Enter') title.blur(); if (e.key === 'Escape') { title.value = t.name; title.blur(); } });
      title.addEventListener('change', () => { const v = title.value.trim(); if (v && v !== t.name) save(slug, { name: v }, 'Renamed.'); else title.value = t.name; });
    }
    const p = primaryAction(t);
    const primary = p.action === 'message' ? null : h('button.btn', { type: 'button', class: p.action === 'approve' ? 'primary' : '', on: { click: () => act(slug, p.action) } }, p.action === 'approve' && icon('check'), p.label);
    return h('div.page',
      h('div.crumbs', h('a', { href: '#/tasks' }, 'Tasks'), t.project && [icon('chevronRight'), h('a', { href: `#/project/${t.project}` }, projectName(t.project))]),
      h('div.page-head.task-head',
        h('div.grow', title),
        h('div.actions',
          primary,
          !t.archived && h('button.btn', { type: 'button', title: 'Opens this task’s Claude session in your terminal app, so you can watch or type directly.', on: { click: () => openTerminal(slug) } }, icon('terminal'), t.terminal_open ? 'Show terminal' : 'Open in terminal'),
          h('button.btn.icon', { type: 'button', 'aria-label': 'More actions', on: { click: e => taskMenu(e.currentTarget, t) } }, icon('more')))),
      runPanel(t, cc),
      reviewPanel(t, cc),
      props(t, cc),
      h('div.tabs', { role: 'tablist' }, tabs.map(([id, label, n]) => h('button', { type: 'button', role: 'tab', 'aria-selected': String(tab === id), on: { click: () => setQuery({ tab: id === 'conversation' ? '' : id }) } }, label, n ? h('span.badge.soft', String(n)) : null))),
      h('div', { role: 'tabpanel' },
        tab === 'memory' ? memoryTab(t, cc) : tab === 'brief' ? briefTab(t, cc) : tab === 'notes' ? notesTab(t, cc) : conversation(t, cc)));
  },
};
