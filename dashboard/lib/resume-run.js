import fs from 'node:fs';
import path from 'node:path';
import { spawn as nodeSpawn } from 'node:child_process';

// Continues an existing claude session headlessly. Detached and never awaited to completion: the
// turn this starts can run far longer than any reasonable HTTP timeout (the design spec's spike saw
// a 13s turn for three sentences and one `sleep 10`). Only the spawn itself is awaited — a failure
// after that point is invisible here, the same limitation focusflow's own auto-run status already has.
export function runResume({ claudeBin, sessionId, text, cwd, env, logFile, spawnFn = nodeSpawn }) {
  return new Promise((resolve, reject) => {
    fs.mkdirSync(path.dirname(logFile), { recursive: true });
    const out = fs.openSync(logFile, 'a');
    const child = spawnFn(claudeBin, ['--resume', sessionId, '-p', text, '--dangerously-skip-permissions'], {
      cwd, env, detached: true, stdio: ['ignore', out, out],
    });
    child.once('error', err => {
      fs.closeSync(out);
      reject(err.code === 'ENOENT' ? new Error(`cannot run ${claudeBin}: not found`) : err);
    });
    child.once('spawn', () => {
      child.unref();
      fs.closeSync(out);
      resolve({ pid: child.pid });
    });
  });
}
