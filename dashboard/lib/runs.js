import fs from 'node:fs';
import path from 'node:path';

export function pidAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try { process.kill(pid, 0); return true; } catch (err) { return err.code === 'EPERM'; }
}

// eslint-disable-next-line no-control-regex
const ANSI = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;
export const stripAnsi = s => String(s).replace(ANSI, '');

// The last `maxLines` lines of a text log (reading at most `maxBytes` from the end), ANSI-stripped.
// Only files under `allowedDir` are read: the path comes from a database row, not from the request, but
// it still must never turn this into an arbitrary file reader.
export function tailLog(file, { allowedDir, maxLines = 40, maxBytes = 16 * 1024 } = {}) {
  try {
    if (!file || !allowedDir) return null;
    const real = fs.realpathSync(file);
    const root = fs.realpathSync(allowedDir);
    if (real !== root && !real.startsWith(root + path.sep)) return null;
    const fd = fs.openSync(real, 'r');
    try {
      const size = fs.fstatSync(fd).size;
      const len = Math.min(size, maxBytes);
      const buf = Buffer.alloc(len);
      fs.readSync(fd, buf, 0, len, size - len);
      let lines = stripAnsi(buf.toString('utf8')).split('\n');
      if (size > len) lines.shift();
      lines = lines.map(l => l.replace(/\s+$/, '')).filter((l, i, a) => l !== '' || (i > 0 && a[i - 1] !== ''));
      return lines.slice(-maxLines).join('\n').trim();
    } finally { fs.closeSync(fd); }
  } catch {
    return null;
  }
}
