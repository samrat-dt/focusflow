import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

// claude's own transcript path (undocumented, reverse-engineered from directory names, not a public
// API — see the design spec's spike finding 2): the work dir with every '/' replaced by '-', under
// ~/.claude/projects, named <session-id>.jsonl.
export function transcriptPath({ workDir, sessionId, home = os.homedir() }) {
  return path.join(home, '.claude', 'projects', workDir.replace(/[/._]/g, '-'), `${sessionId}.jsonl`);
}

// A live session is "working" when its transcript was touched inside the last windowMs. Only
// message/tool-call boundaries append to it, so a long blocking tool call reads as idle in between
// (spike finding 2) — a coarse, not exact, signal. Never throws: a missing or unreadable file just
// means "don't know," which is idle, not an error.
export function isWorking({ workDir, sessionId, now, windowMs, home }) {
  try {
    const mtime = fs.statSync(transcriptPath({ workDir, sessionId, home })).mtimeMs;
    return (now.getTime() - mtime) <= windowMs;
  } catch {
    return false;
  }
}

const clip = (s, n) => { const t = String(s ?? '').replace(/\s+/g, ' ').trim(); return t.length > n ? `${t.slice(0, n - 1)}…` : t; };
const base = p => String(p ?? '').split('/').filter(Boolean).at(-1) ?? '';

// A short human phrase for one assistant content block: what the session is doing right now.
export function describeBlock(b) {
  if (b?.type === 'text' && b.text?.trim()) return { kind: 'text', summary: clip(b.text, 160) };
  if (b?.type === 'tool_use') {
    const i = b.input ?? {};
    const name = b.name ?? 'tool';
    let summary;
    if (name === 'Bash') summary = `Running: ${clip(i.command ?? i.description, 90)}`;
    else if (/^(Edit|Write|MultiEdit|NotebookEdit)$/.test(name)) summary = `Editing ${base(i.file_path ?? i.notebook_path)}`;
    else if (name === 'Read') summary = `Reading ${base(i.file_path)}`;
    else if (/^(Grep|Glob)$/.test(name)) summary = `Searching: ${clip(i.pattern, 60)}`;
    else if (/^(Task|Agent)$/.test(name)) summary = `Delegating: ${clip(i.description ?? i.prompt, 80)}`;
    else if (name === 'TodoWrite') summary = 'Updating its plan';
    else summary = `Using ${name}`;
    return { kind: 'tool', summary };
  }
  return null;
}

// What a session did last, read from the tail of its transcript. Returns null when unknown. Never throws.
export function lastActivity({ workDir, sessionId, home, tailBytes = 256 * 1024 }) {
  let fd;
  try {
    const file = transcriptPath({ workDir, sessionId, home });
    fd = fs.openSync(file, 'r');
    const size = fs.fstatSync(fd).size;
    const len = Math.min(size, tailBytes);
    const buf = Buffer.alloc(len);
    fs.readSync(fd, buf, 0, len, size - len);
    const lines = buf.toString('utf8').split('\n');
    if (size > len) lines.shift(); // the first line may be cut in half
    for (let i = lines.length - 1; i >= 0; i -= 1) {
      let rec;
      try { rec = JSON.parse(lines[i]); } catch { continue; }
      const msg = rec?.message;
      if (rec?.type !== 'assistant' || !Array.isArray(msg?.content)) continue;
      for (let j = msg.content.length - 1; j >= 0; j -= 1) {
        const d = describeBlock(msg.content[j]);
        if (d) return { ...d, at: rec.timestamp ?? null };
      }
    }
    return null;
  } catch {
    return null;
  } finally {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* nothing to do */ }
  }
}
