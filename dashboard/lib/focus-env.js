import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

// Harness session identity: if present, focusflow treats the caller as that session
// ("Already open: ... this is the current session"), so a server started from
// inside a session would behave differently from one started by launchd.
const SESSION_VARS = ['CLAUDE_CODE_SESSION_ID', 'CODEX_THREAD_ID'];

// Environment for every focusflow call. launchd provides almost no PATH and no
// TERM_PROGRAM, so PATH gets the usual install dirs and FOCUSFLOW_TERM is explicit.
export function focusEnv({ focusBin, terminalApp, base = process.env }) {
  const env = { ...base };
  for (const v of SESSION_VARS) delete env[v];
  const dirs = [
    path.isAbsolute(focusBin) ? path.dirname(focusBin) : null,
    '/usr/local/bin',
    '/opt/homebrew/bin',
    path.join(os.homedir(), '.local', 'bin'),
    ...(base.PATH || '').split(':'),
    '/usr/bin',
    '/bin',
  ].filter(Boolean);
  env.PATH = [...new Set(dirs)].join(':');
  env.FOCUSFLOW_TERM = terminalApp;
  return env;
}

const isExecutable = f => {
  try {
    fs.accessSync(f, fs.constants.X_OK);
    return fs.statSync(f).isFile();
  } catch {
    return false;
  }
};

// Absolute path of the focusflow binary, or null. A launchd job needs the full path.
export function resolveFocusBin(focusBin, env = process.env) {
  if (focusBin.includes('/')) return isExecutable(focusBin) ? path.resolve(focusBin) : null;
  for (const dir of (env.PATH || '').split(':').filter(Boolean)) {
    const candidate = path.join(dir, focusBin);
    if (isExecutable(candidate)) return candidate;
  }
  return null;
}
