import fs from 'node:fs';
import path from 'node:path';
import { validateName, CaptureError } from './capture.js';

// Turning an edit from the app into safe focusflow arguments, and writing briefs and notes.
// Nothing here trusts the request: every value is validated, and nothing may start with "-" (focusflow
// would read it as a flag).

export class EditError extends Error {}

const PRIORITIES = ['high', 'medium', 'low'];
const TAG = /^[a-z0-9][a-z0-9_-]{0,39}$/i;
const DUE = /^(\d{4}-\d{2}-\d{2}|today|tomorrow|monday|tuesday|wednesday|thursday|friday|saturday|sunday|\d{1,3}d)$/i;
export const MAX_BRIEF = 50000;
export const MAX_NOTE_TEXT = 10000;

function cleanName(v) {
  try { return validateName(v); } catch (err) { throw new EditError(err instanceof CaptureError ? err.message : 'bad name'); }
}

function cleanShort(v, what, max = 200) {
  if (typeof v !== 'string') throw new EditError(`${what} must be text`);
  const t = v.trim();
  if (!t) throw new EditError(`${what} must not be blank`);
  if (t.startsWith('-')) throw new EditError(`${what} must not start with "-"`);
  if (t.length > max) throw new EditError(`${what} must be at most ${max} characters`);
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(t)) throw new EditError(`${what} must be one line`);
  return t;
}

// `current`: the task as the app knows it (for tag differences). Returns [] when nothing changes.
export function taskEditArgs(slug, body, current = {}) {
  const args = [];
  if (body.name !== undefined && body.name !== current.name) args.push('--name', cleanName(body.name));
  if (body.priority !== undefined && body.priority !== current.priority) {
    if (!PRIORITIES.includes(body.priority)) throw new EditError('priority must be high, medium or low');
    args.push('--priority', body.priority);
  }
  if (body.due !== undefined) {
    if (body.due === null || body.due === '') { if (current.due_date) args.push('--clear-due'); }
    else {
      if (typeof body.due !== 'string' || !DUE.test(body.due.trim())) throw new EditError('due must be a date like 2026-10-01, today, tomorrow, a weekday or 3d');
      args.push('--due-date', body.due.trim());
    }
  }
  if (body.waiting !== undefined) {
    if (body.waiting === null || body.waiting === '') { if (current.waiting_on) args.push('--clear-waiting'); }
    else args.push('--waiting', cleanShort(body.waiting, 'waiting on'));
  }
  if (body.tags !== undefined) {
    if (!Array.isArray(body.tags) || body.tags.length > 20) throw new EditError('tags must be a list of at most 20');
    const want = [...new Set(body.tags.map(t => String(t).trim().replace(/^#/, '').toLowerCase()).filter(Boolean))];
    for (const t of want) if (!TAG.test(t)) throw new EditError(`"${t}" is not a valid tag (letters, digits, - and _)`);
    const have = new Set(current.tags ?? []);
    for (const t of want) if (!have.has(t)) args.push('--tag', t);
    for (const t of have) if (!want.includes(t)) args.push('--remove-tag', t);
  }
  return args.length ? ['update', 'task', slug, ...args] : [];
}

export function projectEditArgs(slug, body, current = {}) {
  const args = [];
  if (body.name !== undefined && body.name !== current.name) args.push('--name', cleanName(body.name));
  if (body.priority !== undefined && body.priority !== current.priority) {
    if (!PRIORITIES.includes(body.priority)) throw new EditError('priority must be high, medium or low');
    args.push('--priority', body.priority);
  }
  return args.length ? ['update', 'project', slug, ...args] : [];
}

function atomicWrite(file, text) {
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const tmp = `${file}.tmp-${process.pid}-${Date.now()}`;
  fs.writeFileSync(tmp, text, { mode: 0o600 });
  fs.renameSync(tmp, file);
}

// kind: 'tasks' | 'projects'. The slug was validated by the caller; the path is re-checked anyway.
export function writeBrief(focusRoot, kind, slug, text) {
  if (typeof text !== 'string') throw new EditError('brief must be text');
  if (text.length > MAX_BRIEF) throw new EditError(`the brief must be at most ${MAX_BRIEF} characters`);
  const dir = path.join(focusRoot, kind, slug);
  if (!fs.existsSync(dir)) throw new EditError('no such item on disk');
  const file = path.join(dir, 'brief.md');
  atomicWrite(file, text.endsWith('\n') ? text : `${text}\n`);
  return file;
}

// A progress note from the app: updates/YYYY-MM-DD-note-HHMMSS.md, never overwriting one that exists.
export function writeNote(focusRoot, slug, text, now = new Date()) {
  if (typeof text !== 'string' || !text.trim()) throw new EditError('write something first');
  if (text.length > MAX_NOTE_TEXT) throw new EditError(`a note must be at most ${MAX_NOTE_TEXT} characters`);
  const dir = path.join(focusRoot, 'tasks', slug);
  if (!fs.existsSync(dir)) throw new EditError('no such task on disk');
  const pad = n => String(n).padStart(2, '0');
  const stamp = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}-note-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  let file = path.join(dir, 'updates', `${stamp}.md`);
  for (let i = 2; fs.existsSync(file); i++) file = path.join(dir, 'updates', `${stamp}-${i}.md`);
  atomicWrite(file, `${text.trim()}\n`);
  return path.basename(file);
}

// Settings the app may change, with their rules. Everything else in instance.json is left alone.
export const SETTINGS = {
  notifications: { type: 'boolean' },
  quiet_hours: { type: 'quiet' },
  stall_minutes: { type: 'int', min: 5, max: 240 },
  stale_days: { type: 'int', min: 1, max: 60 },
  due_soon_days: { type: 'int', min: 0, max: 30 },
  terminal_app: { type: 'enum', values: ['iTerm2', 'Terminal.app'] },
};

export function validateSettings(body) {
  const out = {};
  for (const [k, v] of Object.entries(body ?? {})) {
    const rule = SETTINGS[k];
    if (!rule) throw new EditError(`unknown setting "${k}"`);
    if (rule.type === 'boolean') { if (typeof v !== 'boolean') throw new EditError(`${k} must be true or false`); out[k] = v; }
    if (rule.type === 'int') {
      if (!Number.isInteger(v) || v < rule.min || v > rule.max) throw new EditError(`${k} must be a whole number from ${rule.min} to ${rule.max}`);
      out[k] = v;
    }
    if (rule.type === 'enum') { if (!rule.values.includes(v)) throw new EditError(`${k} must be one of ${rule.values.join(', ')}`); out[k] = v; }
    if (rule.type === 'quiet') {
      if (v === null || v === '') out[k] = null;
      else if (typeof v === 'string' && /^\d{1,2}:\d{2}-\d{1,2}:\d{2}$/.test(v)) out[k] = v;
      else throw new EditError('quiet hours look like 22:00-08:00, or empty for none');
    }
  }
  return out;
}

// Merge validated settings into instance.json (terminal_app lives at the top level, the rest under tunables).
export function saveSettings(home, changes) {
  const file = path.join(home, 'instance.json');
  const inst = JSON.parse(fs.readFileSync(file, 'utf8'));
  inst.tunables = inst.tunables ?? {};
  for (const [k, v] of Object.entries(changes)) {
    if (k === 'terminal_app') inst.terminal_app = v;
    else inst.tunables[k] = v;
  }
  atomicWrite(file, JSON.stringify(inst, null, 2) + '\n');
}
