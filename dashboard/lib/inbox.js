import { SLUG } from './slug.js';

const LIST_TIMEOUT_MS = 15000;
// focusflow ids are 8 hex characters; accept a little more, but never something that could be a flag.
const ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;

// One focusflow inbox row -> what we keep, or null if it is unusable. Times are
// normalized to UTC ISO so plain string comparison orders them.
export function parseRow(row) {
  if (row === null || typeof row !== 'object' || Array.isArray(row)) return null;
  if (typeof row.id !== 'string' || !ID.test(row.id)) return null;
  if (typeof row.body !== 'string') return null;
  const ms = Date.parse(row.created_at);
  if (Number.isNaN(ms)) return null;
  const from = row.from?.task_slug;
  return {
    flow_id: row.id,
    slug: typeof from === 'string' && SLUG.test(from) ? from : null,
    body: row.body,
    reply_to: typeof row.reply_to === 'string' && ID.test(row.reply_to) ? row.reply_to : null,
    urgent: row.urgent === true,
    sent_at: new Date(ms).toISOString(),
    flow_read: row.mail === 'read',
  };
}

function parseList(stdout) {
  const text = String(stdout ?? '').trim();
  if (text === '' || text === 'null') return [];
  let v;
  try { v = JSON.parse(text); } catch { throw new Error('focusflow inbox returned output that is not JSON'); }
  if (!Array.isArray(v)) throw new Error('focusflow inbox did not return a list');
  return v;
}

const byTime = (a, b) =>
  a.sent_at < b.sent_at ? -1 : a.sent_at > b.sent_at ? 1 : a.flow_id < b.flow_id ? -1 : a.flow_id > b.flow_id ? 1 : 0;

// Read-only: lists the user queue and imports rows it has not seen. It never
// pops, so focusflow's unread/read labels stay whatever the human or agents made them.
export function createPoller({ config, store, runFocusCmd, record, notify, isLocked = () => false, now = () => new Date() }) {
  const st = { lastPollAt: null, lastError: null, malformed: 0, firstDone: false, polling: false };

  // Delivery receipts: for messages I queued to a task, ask focusflow whether that task's session has
  // seen them yet (`delivered_at`, stamped when a hook or an inbox listing surfaces the message).
  // Best effort — a failure just leaves the message "sent".
  async function pollDeliveries() {
    const since = new Date(now().getTime() - 24 * 3600 * 1000).toISOString();
    const pending = store.undeliveredOutgoing(config.device, since);
    const slugs = [...new Set(pending.map(p => p.slug))].slice(0, 10);
    for (const slug of slugs) {
      let rows;
      try { rows = parseList((await runFocusCmd(['inbox', '--task', slug, '--all', '--json'], LIST_TIMEOUT_MS)).stdout); } catch { continue; }
      const want = new Set(pending.filter(p => p.slug === slug).map(p => p.flow_id));
      for (const r of rows) {
        if (typeof r?.id === 'string' && want.has(r.id) && typeof r.delivered_at === 'string' && !Number.isNaN(Date.parse(r.delivered_at))) {
          record('message.delivered', `task:${slug}`, { flow_id: r.id, slug, delivered_at: new Date(Date.parse(r.delivered_at)).toISOString() });
        }
      }
    }
  }

  async function pollOnce() {
    // A pull may be rewriting data/, and a slow focusflow call must not stack polls.
    if (st.polling || isLocked()) return { skipped: true, error: null, imported: 0, notified: 0 };
    st.polling = true;
    try {
      let rows;
      try {
        const r = await runFocusCmd(['inbox', '--all', '--as', 'user', '--json'], LIST_TIMEOUT_MS);
        rows = parseList(r.stdout);
      } catch (err) {
        st.lastError = err.message;
        return { skipped: true, error: err.message, imported: 0, notified: 0 };
      }
      st.lastError = null;
      st.lastPollAt = now();

      const backlog = !st.firstDone && !store.hasMessages(config.device);
      st.firstDone = true;

      let malformed = 0;
      const seen = new Set();
      const fresh = [];
      for (const raw of rows) {
        const m = parseRow(raw);
        if (!m) { malformed += 1; continue; }
        if (seen.has(m.flow_id) || store.hasMessage(config.device, m.flow_id)) continue;
        seen.add(m.flow_id);
        fresh.push(m);
      }
      st.malformed = malformed;
      fresh.sort(byTime);

      const arrivals = [];
      for (const m of fresh) {
        const seen_read = backlog || m.flow_read;
        record('message.received', m.slug ? `task:${m.slug}` : 'inbox', {
          flow_id: m.flow_id, slug: m.slug, body: m.body, reply_to: m.reply_to,
          urgent: m.urgent, sent_at: m.sent_at, seen_read,
        }, m.sent_at);
        if (!seen_read && m.slug) {
          arrivals.push({ slug: m.slug, name: store.getTask(m.slug)?.name ?? m.slug, body: m.body, urgent: m.urgent });
        }
      }
      if (arrivals.length) await notify(arrivals);
      await pollDeliveries();
      return { skipped: false, error: null, imported: fresh.length, notified: arrivals.length };
    } finally {
      st.polling = false;
    }
  }

  const status = () => ({
    last_inbox_poll_at: st.lastPollAt ? st.lastPollAt.toISOString() : null,
    last_inbox_error: st.lastError,
    inbox_malformed_count: st.malformed,
  });

  return { pollOnce, status };
}

const flat = s => s.replace(/\s+/g, ' ').trim();
const preview = s => { const t = flat(s); return t.length > 100 ? `${t.slice(0, 99)}…` : t; };
const later = (a, b) => (a < b ? 1 : a > b ? -1 : 0);

// The Inbox list: one row per task that has any message, newest activity first.
export function buildInbox({ store, device, live }) {
  const unread = store.unreadCounts(device);
  const lastBySlug = new Map();
  for (const m of store.messages(device)) lastBySlug.set(m.slug, m); // oldest first, so the last write is the newest
  const threads = [];
  for (const [slug, last] of lastBySlug) {
    const t = store.getTask(slug);
    if (!t) continue;
    threads.push({
      slug, name: t.name, unread: unread.get(slug) ?? 0,
      last: { direction: last.direction, preview: preview(last.body), at: last.at },
      live: live.has(slug), auto_run_status: t.auto_run_status ?? null, status: t.status,
    });
  }
  threads.sort((a, b) => later(a.last.at, b.last.at) || (a.slug < b.slug ? -1 : 1));
  return { unread_total: threads.reduce((n, t) => n + t.unread, 0), threads };
}

// One thread, oldest first. `awaiting_reply`: my latest message has no incoming message after it.
// `stalled`: a live task's `awaiting_reply` that has gone unanswered past `stallMs` — a visible,
// not-silent nudge-stall warning (there is no real delivery ack; see the design spec §7). The
// defaults mean an existing caller that passes none of the new options always gets `stalled: false`.
export function buildThread({ store, device, slug, live = false, now = new Date(), stallMs = Infinity }) {
  const through = store.threadRead(device, slug) ?? '';
  const rows = store.thread(device, slug);
  const messages = rows.map(m => ({
    id: m.flow_id, direction: m.direction, body: m.body, at: m.at, reply_to: m.reply_to,
    delivered_at: m.direction === 'out' ? m.delivered_at : undefined,
    unread: m.direction === 'in' && !m.seen_read && m.at > through,
  }));
  const lastOut = [...rows].reverse().find(m => m.direction === 'out');
  const awaiting = !!lastOut && !rows.some(m => m.direction === 'in' && m.at > lastOut.at);
  const stalled = awaiting && live && !!lastOut && (now.getTime() - Date.parse(lastOut.at)) >= stallMs;
  // "sent" until the session has actually surfaced it, then "delivered" — a real receipt, not a timer.
  const delivery = !awaiting || !lastOut ? null : lastOut.delivered_at ? 'delivered' : 'sent';
  return { messages, awaiting_reply: awaiting, stalled, delivery };
}
