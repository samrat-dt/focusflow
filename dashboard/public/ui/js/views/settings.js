import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { notify } from '../state.js';
import { onRoute } from '../router.js';
import { toast, oops } from '../ui.js';

let data = null;
let health = null;
let doctor = null;
let mounted = false;
onRoute(r => { if (r.name !== 'settings') mounted = false; });
async function fetchAll() {
  const [s, hl, d] = await Promise.all([api('/api/settings').catch(() => null), api('/api/health').catch(() => null), api('/api/doctor').catch(() => null)]);
  data = s; health = hl; doctor = d;
  notify();
}
async function saveSetting(patch, msg = 'Saved.') {
  try { await api('/api/settings', 'POST', patch); toast(msg); await fetchAll(); } catch (err) { oops(err, 'Could not save'); }
}

const group = (title, desc, ...rows) => h('div.section', h('div.section-h', title), desc && h('p', { style: 'margin:-4px 0 12px;color:var(--muted)' }, desc), h('div.list', rows));
const row = (label, hint, control) => h('div.item', { style: 'cursor:default' }, h('div.body', h('div.title', { style: 'white-space:normal' }, label), hint && h('div', { style: 'color:var(--muted);font-size:13px;margin-top:2px' }, hint)), h('div.end', control));
const toggle = (checked, on, label) => h('label.switch', h('input', { type: 'checkbox', checked, 'aria-label': label, on: { change: e => on(e.target.checked) } }), h('span'));
const number = (value, min, max, on, suffix, label) => {
  const i = h('input.input', { type: 'number', value, min, max, style: 'width:80px', 'aria-label': label });
  i.addEventListener('change', () => { const v = Number(i.value); if (Number.isInteger(v) && v >= min && v <= max) on(v); else i.value = value; });
  return h('span.row', { style: 'gap:6px' }, i, suffix && h('span', { style: 'color:var(--muted)' }, suffix));
};

export const settings = {
  title: () => 'Settings',
  deps: () => [data, health, doctor],
  render() {
    if (!mounted) { mounted = true; fetchAll(); }
    if (!data) return h('div.page', h('div.page-head', h('h1', 'Settings')), h('div.empty', 'Loading…'));
    const s = data.settings;
    const [qFrom, qTo] = (s.quiet_hours ?? '').split('-');
    const from = h('input.input', { type: 'time', value: qFrom ?? '', style: 'width:auto', 'aria-label': 'Quiet hours start' });
    const to = h('input.input', { type: 'time', value: qTo ?? '', style: 'width:auto', 'aria-label': 'Quiet hours end' });
    const saveQuiet = () => { if (from.value && to.value && from.value !== to.value) saveSetting({ quiet_hours: `${from.value}-${to.value}` }, 'Quiet hours updated.'); };
    from.addEventListener('change', saveQuiet); to.addEventListener('change', saveQuiet);
    const problems = doctor?.problems ?? [];
    return h('div.page',
      h('div.page-head', h('div', h('h1', 'Settings'), h('div.sub', 'How Focusflow gets your attention, and how it runs on this Mac.'))),
      group('Notifications', 'Focusflow only interrupts you when something needs you. Urgent questions always get through.',
        row('Show notifications', 'Questions, finished work and failed runs appear as macOS notifications.', toggle(s.notifications !== false, v => saveSetting({ notifications: v }, v ? 'Notifications on.' : 'Notifications off.'), 'Show notifications')),
        row('Quiet hours', s.quiet_hours ? 'Non-urgent notifications wait, then arrive as one summary.' : 'Off. Notifications arrive at any time.', h('span.row', { style: 'gap:6px' }, from, 'to', to, s.quiet_hours && h('button.btn.ghost.sm', { type: 'button', on: { click: () => saveSetting({ quiet_hours: '' }, 'Quiet hours off.') } }, 'Turn off'))),
        row('Flag a background run as stuck after', 'No visible progress for this long puts it on Home.', number(s.stall_minutes ?? 15, 5, 240, v => saveSetting({ stall_minutes: v }), 'minutes', 'Minutes before a run counts as stuck'))),
      group('Tasks', null,
        row('Call a task stale after', 'In-progress tasks with no activity for this long get a heads-up.', number(s.stale_days ?? 5, 1, 60, v => saveSetting({ stale_days: v }), 'days', 'Days before a task is stale')),
        row('Warn about due dates', 'How early a task counts as due soon.', number(s.due_soon_days ?? 3, 0, 30, v => saveSetting({ due_soon_days: v }), 'days before', 'Days before a due date to warn'))),
      group('Terminal', 'Used only when you choose “Open in terminal”. Everything else runs in the background.',
        row('Terminal app', null, h('select.select', { style: 'width:auto', 'aria-label': 'Terminal app', on: { change: e => saveSetting({ terminal_app: e.target.value }, 'Saved. Applies to the next terminal you open.') } }, ['iTerm2', 'Terminal.app'].map(v => h('option', { value: v, selected: s.terminal_app === v }, v))))),
      group('Background runs', 'When you message a task that isn’t open in a terminal, Claude works on it in the background, in the project’s folder, without asking for permission for each step. Only message tasks whose folder you trust with that.',
        row('How it works', 'Read what background runs can do and how to stop them.', h('a.btn.sm', { href: '#/help/background-runs' }, 'Learn more'))),
      group('Status', null,
        row('Health', problems.length ? problems.map(p => `${p.level === 'fail' ? 'Problem' : 'Warning'}: ${p.check}${p.detail ? ` (${p.detail})` : ''}`).join(' · ') : 'Everything looks healthy.', h('span.tag', { class: problems.some(p => p.level === 'fail') ? 'danger' : problems.length ? 'warn' : 'accent' }, problems.some(p => p.level === 'fail') ? 'Needs attention' : problems.length ? `${problems.length} warning${problems.length === 1 ? '' : 's'}` : 'Healthy')),
        row('Tasks known', health ? `${health.task_count} tasks · updated ${health.last_tick_at ? new Date(health.last_tick_at).toLocaleTimeString() : 'never'}` : '', h('button.btn.sm', { type: 'button', on: { click: async () => { await api('/api/refresh', 'POST'); await fetchAll(); toast('Refreshed.'); } } }, icon('refresh'), 'Refresh')),
        row('Backups', 'A copy of everything is saved every night at 02:30 in ~/.focusflow/backups. Restore with focusflow restore.', h('a.btn.sm', { href: '#/help/backups' }, 'How to restore'))),
      group('Keyboard', null, row('Shortcuts', '⌘K search · N new task · G then H/I/T/P/M to jump · ? help', h('a.btn.sm', { href: '#/help/shortcuts' }, 'All shortcuts'))));
  },
};
