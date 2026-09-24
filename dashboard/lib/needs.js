// "Needs you": everything that is waiting on the human, in one ordered list. Pure: the server passes
// in task views and message rows.

const KIND_ORDER = ['failed', 'question', 'review', 'overdue', 'waiting'];
const isWork = t => (t.kind ?? 'regular') === 'regular';
const flat = s => String(s ?? '').replace(/\s+/g, ' ').trim();
const preview = s => { const t = flat(s); return t.length > 110 ? `${t.slice(0, 109)}…` : t; };

// `tasks`: server views (with signals). `messages`: store.messages(device) rows, oldest first.
export function buildNeeds({ tasks, messages = [] }) {
  const bySlug = new Map(tasks.map(t => [t.slug, t]));
  const items = [];

  for (const t of tasks) {
    if (t.archived || t.status === 'done') continue;
    const base = { slug: t.slug, name: t.name, project: t.project ?? null, agent: t.kind === 'project_agent' };
    if (isWork(t) && t.status === 'review') {
      items.push({ ...base, kind: 'review', detail: 'Ready for your approval', at: t.status_changed_at || t.updated_at });
    }
    if (isWork(t) && t.auto_run_status === 'dead') {
      items.push({ ...base, kind: 'failed', detail: 'A headless run stopped unexpectedly', at: t.updated_at });
    }
    if (isWork(t) && t.signals?.due === 'overdue') {
      items.push({ ...base, kind: 'overdue', detail: `Overdue since ${t.due_date}`, at: t.due_date });
    }
    if (isWork(t) && t.waiting_on && ['in-progress', 'next', 'paused'].includes(t.status)) {
      items.push({ ...base, kind: 'waiting', detail: `Waiting on ${t.waiting_on}`, at: t.updated_at });
    }
  }

  // A question is a thread whose latest message is from the agent (nothing from me after it).
  const last = new Map();
  for (const m of messages) last.set(m.slug, m);
  for (const [slug, m] of last) {
    const t = bySlug.get(slug);
    if (!t || t.archived || t.status === 'done') continue;
    if (m.direction !== 'in') continue;
    items.push({
      slug, name: t.name, project: t.project ?? null, agent: t.kind === 'project_agent',
      kind: 'question', detail: preview(m.body), at: m.at, urgent: !!m.urgent, unread: !m.seen_read,
    });
  }

  const rank = i => (i.urgent ? -1 : 0) * 10 + KIND_ORDER.indexOf(i.kind);
  items.sort((a, b) => rank(a) - rank(b) || String(b.at ?? '').localeCompare(String(a.at ?? '')) || (a.slug < b.slug ? -1 : 1));

  const counts = Object.fromEntries(KIND_ORDER.map(k => [k, items.filter(i => i.kind === k).length]));
  return { items, counts, total: items.length, badge: counts.review + counts.question + counts.failed };
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
