// The attention engine's pure parts: how much an item deserves the human now (score + plain reasons),
// what the human has snoozed or dismissed, what their own habits say (learned bias), and when to keep
// quiet (quiet hours). The server feeds these from the event log; nothing here does I/O.

export const KIND_WEIGHT = { question: 60, failed: 55, review: 50, stalled: 45, overdue: 40, waiting: 20 };
const PRIORITY_BONUS = { high: 10, medium: 4, low: 0 };

const hoursBetween = (fromIso, now) => {
  const t = Date.parse(fromIso);
  return Number.isFinite(t) ? Math.max(0, (now.getTime() - t) / 3600000) : 0;
};

export function waitedPhrase(hours) {
  if (hours < 1) return `Waiting ${Math.max(1, Math.round(hours * 60))}m`;
  if (hours < 48) return `Waiting ${Math.floor(hours)}h`;
  return `Waiting ${Math.floor(hours / 24)}d`;
}

// score = kind weight + urgency + priority + age + blocking + learned bias, with the reasons a person
// would give for each part that applies.
export function scoreItem(item, { priority, now, blocking = 0, bias = 0 }) {
  const reasons = [];
  let score = KIND_WEIGHT[item.kind] ?? 0;
  if (item.urgent) { score += 30; reasons.push('Marked urgent'); }
  score += PRIORITY_BONUS[priority] ?? 0;
  if (priority === 'high') reasons.push('High priority');
  const hours = hoursBetween(item.at, now);
  score += Math.min(20, Math.round(hours * 2));
  if (item.at && hours >= 0.25) reasons.push(item.kind === 'overdue' ? waitedPhrase(hours).replace('Waiting', 'Overdue') : waitedPhrase(hours));
  if (blocking > 0) { score += Math.min(20, blocking * 10); reasons.push(`Blocks ${blocking} other task${blocking === 1 ? '' : 's'}`); }
  if (bias) {
    score += bias;
    reasons.push(bias > 0 ? 'You usually handle these quickly' : 'You usually leave these for later');
  }
  return { score, reasons };
}

// Snoozes and dismissals are keyed by item (kind:slug) and pinned to its fingerprint (its timestamp):
// when something new happens (a new question, a new failure) the fingerprint changes and it comes back.
export function isSuppressed(item, rule, now) {
  if (!rule || rule.fp !== item.fp) return false;
  if (rule.dismissed) return true;
  return rule.until == null || Date.parse(rule.until) > now.getTime();
}

const median = xs => {
  if (!xs.length) return null;
  const s = [...xs].sort((a, b) => a - b);
  const m = Math.floor(s.length / 2);
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
};

// From past "acted" records ({kind, project, waited_seconds}): items of a kind the human always handles
// faster than their usual pace get up to +10; ones they always leave get down to -10. A project-specific
// pattern wins when there is enough of it. Silent until there is enough history to mean anything.
export function learnedBias(acts) {
  const overall = median(acts.map(a => a.waited_seconds));
  if (acts.length < 5 || !overall) return () => 0;
  const by = key => {
    const m = new Map();
    for (const a of acts) { const k = key(a); if (!m.has(k)) m.set(k, []); m.get(k).push(a.waited_seconds); }
    return m;
  };
  const byKind = by(a => a.kind);
  const byKindProject = by(a => `${a.kind}|${a.project ?? ''}`);
  const toBias = xs => {
    if (!xs || xs.length < 3) return null;
    const med = median(xs);
    return Math.max(-10, Math.min(10, Math.round(10 * (overall - med) / overall)));
  };
  return (kind, project) => toBias(byKindProject.get(`${kind}|${project ?? ''}`)) ?? toBias(byKind.get(kind)) ?? 0;
}

// "22:00-08:00" (wraps midnight) or "13:00-14:00". Anything else means no quiet hours.
export function parseQuietHours(spec) {
  const m = /^(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})$/.exec(String(spec ?? '').trim());
  if (!m) return null;
  const start = Number(m[1]) * 60 + Number(m[2]);
  const end = Number(m[3]) * 60 + Number(m[4]);
  if (start > 1439 || end > 1439 || start === end) return null;
  return { start, end };
}

export function inQuietHours(date, spec) {
  const q = typeof spec === 'string' ? parseQuietHours(spec) : spec;
  if (!q) return false;
  const mins = date.getHours() * 60 + date.getMinutes();
  return q.start < q.end ? mins >= q.start && mins < q.end : mins >= q.start || mins < q.end;
}

// Wraps a notifier: urgent arrivals always go through; the rest wait out quiet hours and are then sent
// as one summary. `flush()` is called on every tick.
export function createGate({ notify, quietHours, now = () => new Date() }) {
  let held = [];
  const quiet = () => inQuietHours(now(), typeof quietHours === 'function' ? quietHours() : quietHours);
  return {
    notify(arrivals) {
      const urgent = arrivals.filter(a => a.urgent);
      const rest = arrivals.filter(a => !a.urgent);
      if (urgent.length) notify(urgent);
      if (!rest.length) return;
      if (quiet()) held.push(...rest);
      else notify(rest);
    },
    flush() {
      if (!held.length || quiet()) return 0;
      const n = held.length;
      const tasks = new Set(held.map(a => a.slug)).size;
      notify(n === 1 ? held : [{ slug: held[0].slug, name: 'Focusflow', urgent: false, body: `While things were quiet: ${n} updates in ${tasks} task${tasks === 1 ? '' : 's'}.` }]);
      held = [];
      return n;
    },
    held: () => held.length,
  };
}
