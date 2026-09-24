import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';

// claude's own transcript path (undocumented, reverse-engineered from directory names, not a public
// API — see the design spec's spike finding 2): the work dir with every '/' replaced by '-', under
// ~/.claude/projects, named <session-id>.jsonl.
export function transcriptPath({ workDir, sessionId, home = os.homedir() }) {
  return path.join(home, '.claude', 'projects', workDir.replace(/\//g, '-'), `${sessionId}.jsonl`);
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
