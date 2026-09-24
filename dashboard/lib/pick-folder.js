import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';

// The native macOS folder chooser (with its own "New Folder" button), opened from the dashboard so a
// project's working directory can be picked or created without typing a path. The dialog appears on the
// user's screen because the server runs in their login session.
const esc = s => s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');

export function pickFolderScript({ prompt = 'Choose a working directory', start = os.homedir() } = {}) {
  let dir = start;
  try { if (!path.isAbsolute(dir) || !fs.statSync(dir).isDirectory()) dir = os.homedir(); } catch { dir = os.homedir(); }
  return [
    'tell application "Finder" to activate',
    `set f to choose folder with prompt "${esc(prompt)}" default location POSIX file "${esc(dir)}"`,
    'return POSIX path of f',
  ];
}

// osascript reports a cancelled dialog as error -128; everything else is a real failure.
export function parsePick({ stdout = '', stderr = '', code = 0 }) {
  if (code !== 0) {
    if (/-128/.test(stderr)) return { cancelled: true };
    throw new Error(stderr.trim() || 'the folder dialog failed');
  }
  const p = stdout.trim().replace(/(.)\/$/, '$1'); // "<dir>/" -> "<dir>" (the root "/" stays)
  if (!p.startsWith('/')) throw new Error('the folder dialog returned no path');
  return { path: p };
}

export function runPickFolder(opts = {}, { timeoutMs = 10 * 60 * 1000 } = {}) {
  const lines = pickFolderScript(opts);
  const args = lines.flatMap(l => ['-e', l]);
  return new Promise((resolve, reject) => {
    execFile('osascript', args, { timeout: timeoutMs }, (err, stdout, stderr) => {
      try { resolve(parsePick({ stdout, stderr: String(stderr || err?.message || ''), code: err ? (err.code ?? 1) : 0 })); } catch (e) { reject(e); }
    });
  });
}
