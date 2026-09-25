import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { S, act, load, send, capture, tasksNow, pendingFor } from '../state.js';
import { go } from '../router.js';
import { menu, toast, oops, empty } from '../ui.js';
import { greeting, humanTime, duration, parseQuickAdd } from '../logic.js';
import { taskRow, projectName, displayName, requestChanges } from '../parts.js';

const KIND = {
  question: { label: 'Question for you', icon: 'message' },
  review: { label: 'Ready for review', icon: 'check' },
  failed: { label: 'Background run stopped', icon: 'alert' },
  stalled: { label: 'Might be stuck', icon: 'hourglass' },
  overdue: { label: 'Overdue', icon: 'calendar' },
  waiting: { label: 'Waiting on someone', icon: 'clock' },
};
const replyDrafts = new Map();
let openReply = null;
let showHidden = false;
let refresh = () => {};

async function attention(op, body, msg) {
  try { await api(`/api/needs-you/${op}`, 'POST', body); if (msg) toast(msg); } catch (err) { oops(err); }
  await load();
}

function laterMenu(anchor, i) {
  menu(anchor, [
    { header: 'Remind me' },
    { label: 'In 1 hour', icon: 'clock', run: () => attention('snooze', { key: i.key, fp: i.fp, preset: '1h' }, 'Snoozed for an hour.') },
    { label: 'In 3 hours', icon: 'clock', run: () => attention('snooze', { key: i.key, fp: i.fp, preset: '3h' }, 'Snoozed for three hours.') },
    { label: 'Tomorrow morning', icon: 'sun', run: () => attention('snooze', { key: i.key, fp: i.fp, preset: 'tomorrow' }, 'See you tomorrow at 8.') },
    { label: 'When something changes', icon: 'eye', run: () => attention('snooze', { key: i.key, fp: i.fp, preset: 'change' }, 'Hidden until something new happens.') },
    'sep',
    { label: 'Dismiss', icon: 'x', run: () => attention('dismiss', { key: i.key, fp: i.fp }, 'Dismissed. It comes back if something new happens.') },
  ]);
}

function replyBox(i) {
  const ta = h('textarea.textarea', { rows: '2', maxlength: '380', placeholder: 'Reply… (it will be remembered for this task)', 'aria-label': `Reply to ${i.name}`, value: replyDrafts.get(i.slug) ?? '', dataset: { key: `reply-box:${i.slug}` } });
  const sendBtn = h('button.btn.primary', { type: 'button' }, icon('send'), 'Send');
  const doSend = async () => {
    const text = ta.value.trim();
    if (!text) return ta.focus();
    if (text.startsWith('-')) return toast('A message cannot start with “-”.', { error: true });
    sendBtn.disabled = true;
    try {
      const r = await send(i.slug, text);
      replyDrafts.delete(i.slug); openReply = null;
      toast(r.background ? 'Sent. Claude is on it in the background.' : 'Sent to the open session.');
    } catch (err) { sendBtn.disabled = false; oops(err, 'Could not send'); }
  };
  ta.addEventListener('input', () => replyDrafts.set(i.slug, ta.value));
  ta.addEventListener('keydown', e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); doSend(); } if (e.key === 'Escape') { openReply = null; refresh(); } });
  sendBtn.addEventListener('click', doSend);
  setTimeout(() => ta.focus(), 0);
  return h('div.reply-box', ta, sendBtn);
}

function attnRow(i) {
  const k = KIND[i.kind] ?? KIND.waiting;
  const t = S.tasks.find(x => x.slug === i.slug);
  const buttons = [];
  if (i.kind === 'question') buttons.push(h('button.btn.primary.sm', { type: 'button', dataset: { key: `reply:${i.slug}` }, on: { click: () => { openReply = openReply === i.slug ? null : i.slug; refresh(); } } }, 'Reply'));
  if (i.kind === 'review' && !i.agent) {
    buttons.push(h('button.btn.primary.sm', { type: 'button', dataset: { key: `approve:${i.slug}` }, on: { click: () => act(i.slug, 'approve') } }, icon('check'), 'Approve'));
    if (t) buttons.push(h('button.btn.sm', { type: 'button', on: { click: () => requestChanges(t, { onDone: load }) } }, 'Changes…'));
  }
  if (i.kind === 'stalled') buttons.push(h('button.btn.sm', { type: 'button', on: { click: () => act(i.slug, 'pause') } }, icon('stop'), 'Stop'));
  if (i.kind !== 'question') buttons.push(h('button.btn.sm', { type: 'button', on: { click: () => go(`/task/${i.slug}`) } }, i.kind === 'review' ? 'Look' : 'Open'));
  buttons.push(h('button.btn.ghost.sm', { type: 'button', 'aria-label': `Remind me later about ${i.name}`, on: { click: e => laterMenu(e.currentTarget, i) } }, 'Later'));
  const iconCls = i.urgent ? 'urgent' : i.kind;
  return h('div.attn', { dataset: { key: `attn:${i.key}` } },
    h('div.attn-icon', { class: iconCls }, icon(k.icon)),
    h('div.attn-main',
      h('div.attn-kind', [k.label, i.urgent && 'Urgent', i.project && projectName(i.project), humanTime(i.at)].filter(Boolean).join(' · ')),
      h('div.attn-title', { role: 'link', tabindex: '0', on: { click: () => go(`/task/${i.slug}`), keydown: e => { if (e.key === 'Enter') go(`/task/${i.slug}`); } } }, i.agent ? `${projectName(i.project)} assistant` : i.name),
      h('div.attn-detail', { class: i.kind === 'question' ? 'quote' : '' }, i.kind === 'review' && t ? 'Claude says this is finished. Take a look, then approve it or ask for changes.' : i.detail),
      i.reasons?.length ? h('div.attn-why', `Why it’s here: ${i.reasons.join(' · ')}`) : null,
      openReply === i.slug && replyBox(i)),
    h('div.attn-actions', buttons));
}

function firstRun() {
  return h('div.page',
    h('div', { style: 'text-align:center;padding:48px 0 16px' },
      h('div.empty', { style: 'padding:0' }, h('div.art', icon('logo', 'i-lg'))),
      h('h1', { style: 'font-size:34px;letter-spacing:-.03em;margin:8px 0' }, 'Welcome to Focusflow'),
      h('p', { style: 'font-size:17px;color:var(--ink-2);max-width:560px;margin:0 auto 32px' }, 'Hand work to Claude, get on with your day, and step in only when you’re needed. Focusflow remembers everything you decide, so you never have to say it twice.')),
    h('div.grid', [
      ['folder', '1. Create a project', 'A project is a folder Claude works in, like a codebase or a writing folder.'],
      ['plus', '2. Add tasks', 'Describe what you want. Anything in Up next is ready to hand over.'],
      ['send', '3. Start with a message', 'Tell Claude what to do. It works in the background; no terminal needed.'],
      ['check', '4. Approve the result', 'Finished work shows up on Home. Approve it, or ask for changes.'],
    ].map(([ic, title, body]) => h('div.tile', { style: 'cursor:default;min-height:0' }, h('div.attn-icon', icon(ic)), h('h3', title), h('div', { style: 'color:var(--ink-2)' }, body)))),
    h('div.row', { style: 'justify-content:center;margin-top:32px' },
      h('button.btn.primary', { type: 'button', on: { click: () => dispatchEvent(new CustomEvent('ff:new-project')) } }, icon('folder'), 'Create your first project'),
      h('button.btn', { type: 'button', on: { click: () => go('/about') } }, 'What is Focusflow?')));
}

function quickAdd() {
  const input = h('input.input', { placeholder: 'Add a task to Up next…   try “Draft the launch post #website !high”', maxlength: '200', 'aria-label': 'Quick add a task', dataset: { key: 'quick-add' } });
  input.addEventListener('keydown', async e => {
    if (e.key !== 'Enter') return;
    const p = parseQuickAdd(input.value, S.projectList.map(x => x.slug));
    if (!p.name) return;
    input.disabled = true;
    try { await capture({ name: p.name, project: p.project, priority: p.priority ?? 'medium', stage: 'next' }); toast(`Added “${p.name}” to Up next.`); input.value = ''; }
    catch (err) { oops(err, 'Could not add that'); }
    input.disabled = false; input.focus();
  });
  return h('div', { style: 'margin-bottom:32px' }, input);
}

export const home = {
  title: () => 'Home',
  deps: () => [S.needs, S.tasks.map(t => [t.slug, t.status, t.headless_running, t.auto_run_status, t.updated_at, t.name]), [...S.runs].map(([k, v]) => [k, v.activity?.summary, Math.floor((v.elapsed_seconds ?? 0) / 60)]), openReply, showHidden, S.loaded],
  render(route, ctx) {
    refresh = ctx.refresh;
    if (!S.loaded) return h('div.page', h('div.empty', 'Loading…'));
    const work = tasksNow().filter(t => (t.kind ?? 'regular') === 'regular' && !t.archived);
    if (!work.length && !S.overview.projects.length) return firstRun();
    const items = S.needs.items.filter(i => !pendingFor(`task:${i.slug}`));
    const running = tasksNow().filter(t => t.headless_running);
    const next = work.filter(t => t.status === 'next').slice(0, 5);
    const summary = [
      items.length ? `${items.length} thing${items.length === 1 ? '' : 's'} need${items.length === 1 ? 's' : ''} you` : 'Nothing needs you',
      running.length ? `${running.length} working in the background` : null,
    ].filter(Boolean).join(' · ');
    const hidden = S.needs.hidden ?? [];
    return h('div.page',
      h('div.page-head', h('div', h('h1', greeting()), h('div.sub', summary))),
      document.documentElement.classList.contains('mini') ? null : quickAdd(),
      h('div.section-h', 'Needs you', h('span.count', String(items.length))),
      items.length ? h('div.list', items.map(attnRow))
        : h('div.list', h('div.empty', { style: 'padding:40px' }, h('div.art', icon('check')), h('h2', 'You’re all caught up'), h('p', running.length ? 'Claude is working in the background. You’ll hear from Focusflow when something needs you.' : 'Hand something over from Up next, or add a new task.'))),
      hidden.length ? h('div.row', { style: 'margin-top:10px;color:var(--muted);font-size:13px' },
        h('span', `${hidden.length} snoozed`),
        h('button.btn.ghost.sm', { type: 'button', on: { click: () => { showHidden = !showHidden; refresh(); } } }, showHidden ? 'Hide' : 'Show')) : null,
      showHidden && hidden.length ? h('div.list', { style: 'margin-top:8px' }, hidden.map(i => h('div.item', { style: 'cursor:default' },
        h('div.body', h('div.title', i.name), h('div.meta', `${KIND[i.kind]?.label ?? i.kind} · ${i.dismissed ? 'dismissed' : i.snoozed_until ? `back ${humanTime(i.snoozed_until)}` : 'back when something changes'}`)),
        h('div.end', h('button.btn.sm', { type: 'button', on: { click: () => attention('restore', { key: i.key }, 'Brought back.') } }, 'Bring back'))))) : null,
      running.length ? h('div.section', h('div.section-h', 'Working in the background', h('span.count', String(running.length))),
        h('div.list', running.map(t => {
          const r = S.runs.get(t.slug);
          return h('div.item', { tabindex: '0', on: { click: () => go(`/task/${t.slug}`) } },
            h('span.dot.in-progress.pulse'),
            h('div.body', h('div.title', t.name), h('div.meta', r?.activity?.summary ?? 'Getting started…')),
            h('div.end', h('span.meta', r?.elapsed_seconds != null ? duration(r.elapsed_seconds) : ''), h('button.btn.sm', { type: 'button', on: { click: e => { e.stopPropagation(); act(t.slug, 'pause'); } } }, icon('stop'), 'Stop')));
        }))) : null,
      next.length ? h('div.section', h('div.section-h', 'Up next', h('span.count', String(work.filter(t => t.status === 'next').length)), h('a.more', { href: '#/tasks?view=board' }, 'See all')),
        h('div.list', next.map(t => taskRow(t)))) : null);
  },
};
