import fs from 'node:fs';
import path from 'node:path';
import { uuidv7 } from './uuidv7.js';

export function makeEvent({ device, kind, subject, payload = {}, recordedAt, eventTime }) {
  const recorded_at = recordedAt ?? new Date().toISOString();
  return {
    v: 1,
    id: uuidv7(),
    recorded_at,
    event_time: eventTime ?? recorded_at,
    device,
    kind,
    subject,
    payload,
  };
}

export function eventFile(dataDir, event) {
  return path.join(dataDir, 'events', event.recorded_at.slice(0, 7), `${event.device}.jsonl`);
}

// Append-only. O_APPEND write of a single line; if a previous crash left a
// partial last line, start on a fresh line so the new event is not glued to it.
export function appendEvent(dataDir, event) {
  const file = eventFile(dataDir, event);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const line = JSON.stringify(event) + '\n';
  const fd = fs.openSync(file, 'a+');
  try {
    const { size } = fs.fstatSync(fd);
    let prefix = '';
    if (size > 0) {
      const last = Buffer.alloc(1);
      fs.readSync(fd, last, 0, 1, size - 1);
      if (last[0] !== 0x0a) prefix = '\n';
    }
    fs.writeSync(fd, prefix + line);
  } finally {
    fs.closeSync(fd);
  }
  return file;
}

function isEvent(o) {
  return o !== null && typeof o === 'object' && !Array.isArray(o) &&
    o.v === 1 && typeof o.id === 'string' && typeof o.kind === 'string' &&
    typeof o.recorded_at === 'string';
}

function jsonlFiles(dataDir) {
  const root = path.join(dataDir, 'events');
  if (!fs.existsSync(root)) return [];
  const files = [];
  for (const month of fs.readdirSync(root).sort()) {
    const dir = path.join(root, month);
    if (!fs.statSync(dir).isDirectory()) continue;
    for (const f of fs.readdirSync(dir).sort()) {
      if (f.endsWith('.jsonl')) files.push(path.join(dir, f));
    }
  }
  return files;
}

// Never writes. Malformed lines (including a truncated last line) are skipped
// and reported with file and 1-based line number.
export function readEvents(dataDir) {
  const events = [];
  const malformed = [];
  for (const file of jsonlFiles(dataDir)) {
    const lines = fs.readFileSync(file, 'utf8').split('\n');
    lines.forEach((text, i) => {
      if (text === '') return;
      try {
        const o = JSON.parse(text);
        if (isEvent(o)) events.push(o);
        else malformed.push({ file, line: i + 1 });
      } catch {
        malformed.push({ file, line: i + 1 });
      }
    });
  }
  events.sort((a, b) =>
    a.recorded_at < b.recorded_at ? -1 : a.recorded_at > b.recorded_at ? 1 :
    a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  return { events, malformed };
}
