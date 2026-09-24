// "Needs you": everything that is waiting on the human, in one ordered list. Pure: the server passes
// in task views and message rows.

import { scoreItem, isSuppressed } from './attention.js';

const KIND_ORDER = ['failed', 'question', 'review', 'stalled', 'overdue', 'waiting'];
const isWork = t => (t.kind ?? 'regular') === 'regular';
const flat = s => String(s ?? '').replace(/\s+/g, ' ').trim();
const preview = s => { const t = flat(s); return t.length > 110 ? `${t.slice(0, 109)}…` : t; };
const minutes = ms => Math.max(1, Math.round(ms / 60000));

// Everything waiting on the human, ranked by the attention engine.
// `tasks`: server views (with signals). `messages`: store.messages(device) rows, oldest first.
// `rules`: Map key -> {fp, until, dismissed} (snoozes). `bias(kind, project)`: learned bias.
// `runActivity`: Map slug -> ISO of a running task's latest step; quiet longer than `stallMs` = stalled.
export function buildNeeds({ tasks, messages = [], now = new Date(), rules = new Map(), bias = () => 0, runActivity = new Map(), stallMs = 15 * 60000 }) {
  const bySlug = new Map(tasks.map(t => [t.slug, t]));
  const raw = [];
  const push = (t, extra) => raw.push({ slug: t.slug, name: t.name, project: t.project ?? null, agent: t.kind === 'project_agent', priority: t.priority, ...extra });

  for (const t of tasks) {
    if (t.archived || t.status === 'done') continue;
    if (isWork(t) && t.status === 'review') push(t, { kind: 'review', detail: 'Ready for your approval', at: t.status_changed_at || t.updated_at });
    if (isWork(t) && t.auto_run_status === 'dead') push(t, { kind: 'failed', detail: 'A background run stopped unexpectedly', at: t.updated_at });
    if (t.auto_run_status === 'running') {
      const last = runActivity.get(t.slug);
      const quietMs = last ? now.getTime() - Date.parse(last) : NaN;
      if (Number.isFinite(quietMs) && quietMs >= stallMs) push(t, { kind: 'stalled', detail: `Working in the background, but no progress for ${minutes(quietMs)}m`, at: last });
    }
    if (isWork(t) && t.signals?.due === 'overdue') push(t, { kind: 'overdue', detail: `Overdue since ${t.due_date}`, at: t.due_date });
    if (isWork(t) && t.waiting_on && ['in-progress', 'next', 'paused'].includes(t.status)) push(t, { kind: 'waiting', detail: `Waiting on ${t.waiting_on}`, at: t.updated_at });
  }

  // A question is a thread whose latest message is from the agent (nothing from me after it).
  const last = new Map();
  for (const m of messages) last.set(m.slug, m);
  for (const [slug, m] of last) {
    const t = bySlug.get(slug);
    if (!t || t.archived || t.status === 'done' || m.direction !== 'in') continue;
    push(t, { kind: 'question', detail: preview(m.body), at: m.at, urgent: !!m.urgent, unread: !m.seen_read });
  }

  // How many other tasks say they are waiting on this one.
  const blockingOf = t => tasks.filter(o => o.slug !== t.slug && !o.archived && o.status !== 'done' && o.waiting_on &&
    new RegExp(`\\b(${[t.slug, t.name].map(x => String(x).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})\\b`, 'i').test(o.waiting_on)).length;

  const visible = [];
  const hidden = [];
  for (const i of raw) {
    i.key = `${i.kind}:${i.slug}`;
    i.fp = String(i.at ?? '');
    const { score, reasons } = scoreItem(i, { priority: i.priority, now, blocking: blockingOf(bySlug.get(i.slug)), bias: bias(i.kind, i.project) });
    i.score = score;
    i.reasons = reasons;
    delete i.priority;
    if (isSuppressed(i, rules.get(i.key), now)) {
      const r = rules.get(i.key);
      hidden.push({ ...i, snoozed_until: r.dismissed ? null : r.until ?? null, dismissed: !!r.dismissed });
    } else visible.push(i);
  }
  const order = (a, b) => b.score - a.score || KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind) || String(b.at ?? '').localeCompare(String(a.at ?? '')) || (a.slug < b.slug ? -1 : 1);
  visible.sort(order);
  hidden.sort(order);

  const counts = Object.fromEntries(KIND_ORDER.map(k => [k, visible.filter(i => i.kind === k).length]));
  return { items: visible, counts, total: visible.length, badge: counts.review + counts.question + counts.failed + counts.stalled, hidden };
}

// "Since you were away": what changed after `since` (ISO), from task state and the message log.
export function buildSince({ tasks, messages = [], since }) {
  const after = v => v && String(v) > since;
  const active = tasks.filter(t => !t.archived && (t.kind ?? 'regular') === 'regular');
  const pick = (list, f) => list.map(f).sort((a, b) => (a.at < b.at ? 1 : -1));
  const entry = t => ({ slug: t.slug, name: t.name, at: t.status_changed_at });
  const reviews = pick(active.filter(t => t.status === 'review' && after(t.status_changed_at)), entry);
  const done = pick(active.filter(t => t.status === 'done' && after(t.status_changed_at)), entry);
  const failed = pick(active.filter(t => t.auto_run_status === 'dead' && after(t.updated_at)), t => ({ slug: t.slug, name: t.name, at: t.updated_at }));
  const names = new Map(tasks.map(t => [t.slug, t.name]));
  const replies = new Map();
  for (const m of messages) {
    if (m.direction !== 'in' || !after(m.at) || !names.has(m.slug)) continue;
    const r = replies.get(m.slug) ?? { slug: m.slug, name: names.get(m.slug), count: 0, at: m.at };
    r.count += 1; if (m.at > r.at) r.at = m.at;
    replies.set(m.slug, r);
  }
  const out = { reviews, done, failed, replies: [...replies.values()].sort((a, b) => (a.at < b.at ? 1 : -1)) };
  out.total = out.reviews.length + out.done.length + out.failed.length + out.replies.length;
  return out;
}
