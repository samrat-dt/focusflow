import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';

export const DEFAULTS = {
  tunables: {
    port: 4177, tick_minutes: 5, stale_days: 5, due_soon_days: 3,
    inbox_poll_seconds: 5, notifications: true,
    working_window_seconds: 15, nudge_stall_seconds: 30,
  },
};

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function readJson(file) {
  try {
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch (err) {
    throw new Error(`cannot read ${file}: ${err.message}`);
  }
}

// Per-machine config lives outside the repo (never in git). Idempotent: creates
// what is missing, never overwrites what exists.
export function loadConfig({
  home = process.env.FOCUSFLOW_DASHBOARD_HOME || path.join(process.env.FOCUSFLOW_ROOT || path.join(os.homedir(), '.focusflow'), 'dashboard'),
  repoRoot = REPO_ROOT,
} = {}) {
  fs.mkdirSync(home, { recursive: true, mode: 0o700 });

  const instanceFile = path.join(home, 'instance.json');
  const created = { instance: !fs.existsSync(instanceFile), token: false };
  if (created.instance) {
    const fresh = {
      name: 'focusflow-dashboard',
      device: `dev-${crypto.randomBytes(4).toString('hex')}`,
      data_dir: null,
      cache_db: null,
      terminal_app: 'Terminal.app',
      tunables: { ...DEFAULTS.tunables },
    };
    fs.writeFileSync(instanceFile, JSON.stringify(fresh, null, 2) + '\n', { flag: 'wx' });
  }
  const instance = readJson(instanceFile);

  const tokenFile = path.join(home, 'token');
  if (!fs.existsSync(tokenFile)) {
    created.token = true;
    fs.writeFileSync(tokenFile, crypto.randomBytes(32).toString('hex') + '\n', { flag: 'wx', mode: 0o600 });
  }
  const token = fs.readFileSync(tokenFile, 'utf8').trim();
  if (!token) throw new Error(`${tokenFile} is empty`);

  return {
    home,
    created,
    name: instance.name,
    device: instance.device,
    token,
    dataDir: instance.data_dir || path.join(home, 'data'),
    cacheDb: instance.cache_db || path.join(home, 'cache', 'store.db'),
    terminalApp: instance.terminal_app || 'Terminal.app',
    focusBin: process.env.FOCUSFLOW_BIN || 'focusflow',
    claudeBin: process.env.CLAUDE_BIN || 'claude',
    focusRoot: process.env.FOCUSFLOW_ROOT || path.join(os.homedir(), '.focusflow'),
    focusDbName: process.env.FOCUSFLOW_DB_NAME || 'focusflow.db',
    tunables: { ...DEFAULTS.tunables, ...(instance.tunables || {}) },
  };
}
