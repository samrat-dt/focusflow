import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { DatabaseSync } from 'node:sqlite';
import { readEvents } from './event-log.js';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS tasks (slug TEXT PRIMARY KEY, state TEXT NOT NULL, as_of TEXT NOT NULL, device TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots (date TEXT NOT NULL, device TEXT NOT NULL, recorded_at TEXT NOT NULL, tasks TEXT NOT NULL, PRIMARY KEY (date, device));
CREATE TABLE IF NOT EXISTS gaps (date TEXT NOT NULL, device TEXT NOT NULL, PRIMARY KEY (date, device));
CREATE TABLE IF NOT EXISTS captures (id TEXT PRIMARY KEY, event_time TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS opens (id TEXT PRIMARY KEY, event_time TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS instances (device TEXT PRIMARY KEY, event_time TEXT NOT NULL, payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS messages (device TEXT NOT NULL, flow_id TEXT NOT NULL, slug TEXT, direction TEXT NOT NULL, body TEXT NOT NULL, reply_to TEXT, urgent INTEGER NOT NULL DEFAULT 0, at TEXT NOT NULL, seen_read INTEGER NOT NULL DEFAULT 0, delivered_at TEXT, PRIMARY KEY (device, flow_id));
CREATE TABLE IF NOT EXISTS thread_read (device TEXT NOT NULL, slug TEXT NOT NULL, through TEXT NOT NULL, PRIMARY KEY (device, slug));
CREATE TABLE IF NOT EXISTS headless_runs (slug TEXT PRIMARY KEY, first_at TEXT NOT NULL);
`;

const messageRow = r => ({
  flow_id: r.flow_id, slug: r.slug, direction: r.direction, body: r.body, reply_to: r.reply_to,
  urgent: !!r.urgent, at: r.at, seen_read: !!r.seen_read, delivered_at: r.delivered_at ?? null,
});

// The SQLite cache. Everything in it is derived from events by apply(), so a
// rebuild (replaying the log through apply()) reproduces it exactly.
export class Store {
  constructor(dbPath = ':memory:') {
    if (dbPath !== ':memory:') fs.mkdirSync(path.dirname(dbPath), { recursive: true });
    this.db = new DatabaseSync(dbPath);
    this.db.exec(SCHEMA);
    // A cache file from before delivery receipts: add the column (the cache is disposable, but this
    // keeps an existing one usable without a rebuild).
    try { this.db.exec('ALTER TABLE messages ADD COLUMN delivered_at TEXT'); } catch { /* already there */ }
    this.q = {
      getTask: this.db.prepare('SELECT state, as_of, device FROM tasks WHERE slug = ?'),
      putTask: this.db.prepare('INSERT OR REPLACE INTO tasks (slug, state, as_of, device) VALUES (?, ?, ?, ?)'),
      putSnapshot: this.db.prepare('INSERT OR IGNORE INTO snapshots (date, device, recorded_at, tasks) VALUES (?, ?, ?, ?)'),
      putGap: this.db.prepare('INSERT OR IGNORE INTO gaps (date, device) VALUES (?, ?)'),
      putCapture: this.db.prepare('INSERT OR IGNORE INTO captures (id, event_time, payload) VALUES (?, ?, ?)'),
      putOpen: this.db.prepare('INSERT OR IGNORE INTO opens (id, event_time, payload) VALUES (?, ?, ?)'),
      putInstance: this.db.prepare('INSERT OR IGNORE INTO instances (device, event_time, payload) VALUES (?, ?, ?)'),
      putMessage: this.db.prepare('INSERT OR IGNORE INTO messages (device, flow_id, slug, direction, body, reply_to, urgent, at, seen_read) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)'),
      putDelivered: this.db.prepare('UPDATE messages SET delivered_at = ? WHERE device = ? AND flow_id = ? AND delivered_at IS NULL'),
      putRead: this.db.prepare('INSERT INTO thread_read (device, slug, through) VALUES (?, ?, ?) ON CONFLICT(device, slug) DO UPDATE SET through = MAX(through, excluded.through)'),
      putHeadlessRun: this.db.prepare('INSERT OR IGNORE INTO headless_runs (slug, first_at) VALUES (?, ?)'),
      bump: this.db.prepare("INSERT INTO meta (key, value) VALUES (?, 1) ON CONFLICT(key) DO UPDATE SET value = value + 1"),
    };
  }

  apply(e) {
    const p = e.payload ?? {};
    switch (e.kind) {
      case 'instance.created':
        this.q.putInstance.run(e.device, e.event_time, JSON.stringify(p));
        break;
      case 'task.appeared': {
        const cur = this.q.getTask.get(p.state.slug);
        if (cur && cur.device !== e.device && e.event_time < cur.as_of) break;
        this.q.putTask.run(p.state.slug, JSON.stringify(p.state), e.event_time, e.device);
        break;
      }
      case 'task.changed': {
        const cur = this.q.getTask.get(p.slug);
        if (!cur) { this.q.bump.run('anomalies'); break; }
        // Across devices the latest event_time wins; on one device, log order wins.
        if (cur.device !== e.device && e.event_time < cur.as_of) break;
        const next = JSON.parse(cur.state);
        for (const [field, [, to]] of Object.entries(p.changes)) next[field] = to;
        this.q.putTask.run(p.slug, JSON.stringify(next), e.event_time, e.device);
        break;
      }
      case 'snapshot.daily':
        this.q.putSnapshot.run(p.date, e.device, e.recorded_at, JSON.stringify(p.tasks));
        break;
      case 'snapshot.gap':
        for (const d of p.dates) this.q.putGap.run(d, p.device ?? e.device);
        break;
      case 'capture.created':
        this.q.putCapture.run(e.id, e.event_time, JSON.stringify(p));
        break;
      case 'open.requested':
        this.q.putOpen.run(e.id, e.event_time, JSON.stringify(p));
        break;
      case 'message.received':
        this.q.putMessage.run(e.device, p.flow_id, p.slug ?? null, 'in', p.body, p.reply_to ?? null, p.urgent ? 1 : 0, p.sent_at ?? e.event_time, p.seen_read ? 1 : 0);
        break;
      case 'message.sent':
        if (p.ok === false) break; // a failed launch stays in the log only
        this.q.putMessage.run(e.device, p.flow_id ?? `out:${e.id}`, p.slug, 'out', p.text, null, 0, e.event_time, 0);
        if (p.mode === 'headless') this.q.putHeadlessRun.run(p.slug, e.event_time);
        break;
      case 'message.delivered':
        this.q.putDelivered.run(p.delivered_at, e.device, p.flow_id);
        break;
      case 'message.read':
        this.q.putRead.run(e.device, p.slug, p.through);
        break;
      default:
        break; // unknown kinds from newer versions are kept in the log, ignored here
    }
  }

  getTask(slug) {
    const r = this.q.getTask.get(slug);
    return r ? JSON.parse(r.state) : null;
  }

  tasks() {
    return this.db.prepare('SELECT state FROM tasks ORDER BY slug').all().map(r => JSON.parse(r.state));
  }

  hasInstance(device) {
    return !!this.db.prepare('SELECT 1 AS x FROM instances WHERE device = ?').get(device);
  }

  snapshotDates(device) {
    return this.db.prepare('SELECT date FROM snapshots WHERE device = ? ORDER BY date').all(device).map(r => r.date);
  }

  gapCount(device) {
    return this.db.prepare('SELECT COUNT(*) AS n FROM gaps WHERE device = ?').get(device).n;
  }

  hasMessages(device) {
    return !!this.db.prepare('SELECT 1 AS x FROM messages WHERE device = ? LIMIT 1').get(device);
  }

  hasMessage(device, focusId) {
    return !!this.db.prepare('SELECT 1 AS x FROM messages WHERE device = ? AND flow_id = ?').get(device, focusId);
  }

  // Rows that belong to a thread (non-null slug), oldest first.
  messages(device) {
    return this.db.prepare('SELECT * FROM messages WHERE device = ? AND slug IS NOT NULL ORDER BY at, flow_id').all(device).map(messageRow);
  }

  thread(device, slug) {
    return this.db.prepare('SELECT * FROM messages WHERE device = ? AND slug = ? ORDER BY at, flow_id').all(device, slug).map(messageRow);
  }

  threadRead(device, slug) {
    const r = this.db.prepare('SELECT through FROM thread_read WHERE device = ? AND slug = ?').get(device, slug);
    return r ? r.through : null;
  }

  unreadIncoming(device, slug) {
    return this.db.prepare(`SELECT m.* FROM messages m LEFT JOIN thread_read r ON r.device = m.device AND r.slug = m.slug
      WHERE m.device = ? AND m.slug = ? AND m.direction = 'in' AND m.seen_read = 0 AND (r.through IS NULL OR m.at > r.through)
      ORDER BY m.at, m.flow_id`).all(device, slug).map(messageRow);
  }

  // Queued messages I sent whose delivery is not known yet (focusflow ids only, recent), by slug.
  undeliveredOutgoing(device, sinceIso) {
    return this.db.prepare(`SELECT slug, flow_id FROM messages WHERE device = ? AND direction = 'out' AND slug IS NOT NULL
      AND delivered_at IS NULL AND flow_id NOT LIKE 'out:%' AND at >= ? ORDER BY at`).all(device, sinceIso).map(r => ({ slug: r.slug, flow_id: r.flow_id }));
  }

  everHeadless(slug) {
    return !!this.db.prepare('SELECT 1 AS x FROM headless_runs WHERE slug = ?').get(slug);
  }

  unreadCounts(device) {
    const rows = this.db.prepare(`SELECT m.slug AS slug, COUNT(*) AS n FROM messages m LEFT JOIN thread_read r ON r.device = m.device AND r.slug = m.slug
      WHERE m.device = ? AND m.slug IS NOT NULL AND m.direction = 'in' AND m.seen_read = 0 AND (r.through IS NULL OR m.at > r.through)
      GROUP BY m.slug ORDER BY m.slug`).all(device);
    return new Map(rows.map(r => [r.slug, r.n]));
  }

  // Deterministic plain-object view of the whole cache, used for the rebuild check.
  dump() {
    const all = (sql, map) => this.db.prepare(sql).all().map(map);
    const anomalies = this.db.prepare("SELECT value FROM meta WHERE key = 'anomalies'").get();
    return {
      tasks: all('SELECT slug, state, as_of, device FROM tasks ORDER BY slug', r => ({ slug: r.slug, state: JSON.parse(r.state), as_of: r.as_of, device: r.device })),
      snapshots: all('SELECT date, device, recorded_at, tasks FROM snapshots ORDER BY date, device', r => ({ date: r.date, device: r.device, recorded_at: r.recorded_at, tasks: JSON.parse(r.tasks) })),
      gaps: all('SELECT date, device FROM gaps ORDER BY date, device', r => ({ date: r.date, device: r.device })),
      captures: all('SELECT id, event_time, payload FROM captures ORDER BY id', r => ({ id: r.id, event_time: r.event_time, payload: JSON.parse(r.payload) })),
      opens: all('SELECT id, event_time, payload FROM opens ORDER BY id', r => ({ id: r.id, event_time: r.event_time, payload: JSON.parse(r.payload) })),
      instances: all('SELECT device, event_time, payload FROM instances ORDER BY device', r => ({ device: r.device, event_time: r.event_time, payload: JSON.parse(r.payload) })),
      messages: all('SELECT device, flow_id, slug, direction, body, reply_to, urgent, at, seen_read, delivered_at FROM messages ORDER BY device, flow_id', r => ({ ...r })),
      thread_read: all('SELECT device, slug, through FROM thread_read ORDER BY device, slug', r => ({ ...r })),
      headless_runs: all('SELECT slug, first_at FROM headless_runs ORDER BY slug', r => ({ slug: r.slug, first_at: r.first_at })),
      anomalies: anomalies ? anomalies.value : 0,
    };
  }

  hash() {
    return crypto.createHash('sha256').update(JSON.stringify(this.dump())).digest('hex');
  }

  close() { this.db.close(); }
}

export function openStore(dbPath) { return new Store(dbPath); }

// Delete the cache and replay every event through apply().
export function rebuild({ dbPath, dataDir }) {
  for (const suffix of ['', '-wal', '-shm', '-journal']) fs.rmSync(dbPath + suffix, { force: true });
  const { events, malformed } = readEvents(dataDir);
  const store = new Store(dbPath);
  store.db.exec('BEGIN');
  try {
    for (const e of events) store.apply(e);
    store.db.exec('COMMIT');
  } catch (err) {
    store.db.exec('ROLLBACK');
    store.close();
    throw err;
  }
  return { store, eventCount: events.length, malformed };
}
