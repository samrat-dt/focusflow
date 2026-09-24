import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { loadConfig } from './config.js';
import { readTasks as readFocusTasks } from './focus-reader.js';
import { focusEnv, resolveFocusBin } from './focus-env.js';
import { rebuild } from './store.js';
import { ensureInstance, tick } from './snapshotter.js';
import { LABEL } from './launchd.js';

export class SetupError extends Error {}

function defaultFocusVersion(config) {
  const r = spawnSync(config.focusBin, ['--version'], { encoding: 'utf8', env: focusEnv({ focusBin: config.focusBin, terminalApp: config.terminalApp }) });
  return r.error || r.status !== 0 ? 'unknown' : r.stdout.trim();
}

// One-time and idempotent: checks focusflow, creates per-machine config and the data
// dir, records the first snapshot, and reports sync state. It never pushes and
// never creates a remote; the login item is a separate, confirmed step.
export async function runSetup({ home, repoRoot, readTasks, focusVersion, now = () => new Date() } = {}) {
  const config = loadConfig({ home, repoRoot });
  const read = readTasks ?? (() => readFocusTasks({ focusBin: config.focusBin, focusRoot: config.focusRoot, dbName: config.focusDbName }));

  try {
    read();
  } catch (err) {
    throw new SetupError(`focusflow is not usable: ${err.message}. Install focusflow, put it on PATH, or set FOCUSFLOW_BIN to its full path.`);
  }

  const { store } = rebuild({ dbPath: config.cacheDb, dataDir: config.dataDir });
  try {
    const before = store.dump().snapshots.length;
    const instance = ensureInstance({ store, dataDir: config.dataDir, device: config.device, now: now() });
    const t = tick({ store, dataDir: config.dataDir, device: config.device, readTasks: read, now: now() });
    if (t.skipped) throw new SetupError(`first reading failed: ${t.error}`);
    return {
      device: config.device,
      home: config.home,
      tokenCreated: config.created.token,
      instanceCreated: config.created.instance,
      focusVersion: (focusVersion ?? (() => defaultFocusVersion(config)))(),
      dataDir: config.dataDir,
      tasks: store.tasks().length,
      events: instance.length + t.events.length,
      newSnapshot: store.dump().snapshots.length > before,
      url: `http://127.0.0.1:${config.tunables.port}/`,
      config,
    };
  } finally {
    store.close();
  }
}

// What the login item needs from this machine's config.
export function agentSpec({ config, repoRoot, nodePath = process.execPath, base = process.env }) {
  const env = focusEnv({ focusBin: config.focusBin, terminalApp: config.terminalApp, base });
  const focusBin = resolveFocusBin(config.focusBin, env);
  if (!focusBin) throw new SetupError(`cannot find the focusflow binary (${config.focusBin}) to put in the login item; set FOCUSFLOW_BIN to its full path`);
  return {
    label: LABEL,
    nodePath,
    serverPath: path.join(repoRoot, 'server.js'),
    workDir: repoRoot,
    logPath: path.join(config.home, 'server.log'),
    env: {
      PATH: env.PATH,
      FOCUSFLOW_TERM: config.terminalApp,
      FOCUSFLOW_BIN: focusBin,
      ...(base.FOCUSFLOW_DASHBOARD_HOME ? { FOCUSFLOW_DASHBOARD_HOME: base.FOCUSFLOW_DASHBOARD_HOME } : {}),
      ...(base.FOCUSFLOW_ROOT ? { FOCUSFLOW_ROOT: base.FOCUSFLOW_ROOT } : {}),
      ...(base.FOCUSFLOW_DB_NAME ? { FOCUSFLOW_DB_NAME: base.FOCUSFLOW_DB_NAME } : {}),
    },
  };
}
