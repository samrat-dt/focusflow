import { makeEvent, appendEvent } from './event-log.js';
import { diffStates } from './task-state.js';

const pad = n => String(n).padStart(2, '0');

export function localDate(d) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// Local dates strictly between a and b (both YYYY-MM-DD).
function datesBetween(a, b) {
  const [y, m, d] = a.split('-').map(Number);
  const out = [];
  for (let i = 1; ; i++) {
    const next = localDate(new Date(y, m - 1, d + i));
    if (next >= b) return out;
    out.push(next);
  }
}

// Append to the log first (the log is the truth), then update the cache.
function recorder({ store, dataDir, device, now }) {
  const events = [];
  const record = (kind, subject, payload, eventTime) => {
    const e = makeEvent({ device, kind, subject, payload, recordedAt: now.toISOString(), eventTime });
    appendEvent(dataDir, e);
    store.apply(e);
    events.push(e);
  };
  return { events, record };
}

export function ensureInstance({ store, dataDir, device, now = new Date() }) {
  const { events, record } = recorder({ store, dataDir, device, now });
  if (!store.hasInstance(device)) record('instance.created', 'instance', { device });
  return events;
}

// An updated_at we can trust as "when it happened": valid and not in the future.
function happenedAt(updatedAt, now) {
  const t = Date.parse(updatedAt);
  return Number.isNaN(t) || t > now.getTime() ? undefined : new Date(t).toISOString();
}

export function tick({ store, dataDir, device, readTasks, now = new Date() }) {
  let reading;
  try {
    reading = readTasks();
  } catch (err) {
    return { events: [], skipped: true, error: err.message };
  }
  const { events, record } = recorder({ store, dataDir, device, now });
  const current = [...reading.tasks].sort((a, b) => (a.slug < b.slug ? -1 : a.slug > b.slug ? 1 : 0));
  const known = new Map(store.tasks().map(t => [t.slug, t]));

  for (const next of current) {
    const prev = known.get(next.slug);
    if (!prev) {
      record('task.appeared', `task:${next.slug}`, { state: next });
      continue;
    }
    const changes = diffStates(prev, next);
    if (Object.keys(changes).length === 0) continue;
    const eventTime = changes.updated_at ? happenedAt(next.updated_at, now) : undefined;
    record('task.changed', `task:${next.slug}`, { slug: next.slug, changes }, eventTime);
  }

  const today = localDate(now);
  const dates = store.snapshotDates(device);
  if (!dates.includes(today)) {
    const last = dates.at(-1);
    if (last && last < today) {
      const missing = datesBetween(last, today);
      if (missing.length) record('snapshot.gap', `snapshot:${device}`, { device, dates: missing });
    }
    record('snapshot.daily', `snapshot:${today}`, { date: today, tasks: current });
  }
  return { events };
}
