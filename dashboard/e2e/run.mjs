// End-to-end check of the app's core journeys in headless Chrome, against a throwaway sandbox:
// its own focusflow binary, data, HOME and port, and a fake `claude` so nothing real ever runs.
// Usage: npm run e2e   (needs Go and Google Chrome; macOS)
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { journeys } from './journeys.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const dashboard = path.resolve(here, '..');
const repo = path.resolve(dashboard, '..');
const sb = fs.mkdtempSync(path.join(os.tmpdir(), 'ff-e2e-'));
const dirs = Object.fromEntries(['bin', 'home', 'root', 'work'].map(d => [d, path.join(sb, d)]));
for (const d of Object.values(dirs)) fs.mkdirSync(d, { recursive: true });
const port = 4190 + Math.floor(Math.random() * 50);

const env = { ...process.env, PATH: `${dirs.bin}:${process.env.PATH}`, HOME: dirs.home, FOCUSFLOW_ROOT: dirs.root,
  FOCUSFLOW_DASHBOARD_HOME: path.join(dirs.root, 'dashboard'), FOCUSFLOW_BIN: path.join(dirs.bin, 'focusflow'), CLAUDE_BIN: path.join(dirs.bin, 'claude') };
for (const k of ['FOCUSFLOW_TASK', 'FOCUSFLOW_PROJECT', 'CLAUDE_CODE_SESSION_ID', 'CODEX_THREAD_ID']) delete env[k];

execFileSync('go', ['build', '-o', env.FOCUSFLOW_BIN, '.'], { cwd: repo, stdio: 'inherit' });
fs.writeFileSync(env.CLAUDE_BIN, '#!/bin/sh\n# stand-in for claude: works briefly, touches nothing\nsleep 3\nexit 0\n', { mode: 0o755 });
const ff = (...args) => execFileSync(env.FOCUSFLOW_BIN, args, { cwd: dirs.work, env, stdio: 'pipe' }).toString();

ff('init');
ff('add', 'project', 'Website', '--slug', 'website', '--work-dir', dirs.work, '--priority', 'high');
ff('add', 'project', 'Ops', '--slug', 'ops', '--work-dir', dirs.work);
for (const [name, slug, project, pri] of [['Draft the launch note', 'launch', 'website', 'high'], ['Fix pricing table', 'pricing', 'website', 'medium'],
  ['Rotate API keys', 'keys', 'ops', 'high'], ['Write onboarding email', 'onboard', 'website', 'low'], ['Audit backups', 'backups', 'ops', 'medium'], ['Old idea', 'idea', 'website', 'low'], ['Write the FAQ', 'faq', 'website', 'medium']]) {
  ff('add', 'task', name, '--slug', slug, '--project', project, '--priority', pri);
}
for (const s of ['launch', 'pricing', 'keys']) ff('next', s);
ff('update', 'task', 'pricing', '--status', 'in-progress');
ff('update', 'task', 'onboard', '--status', 'in-progress');
ff('review', 'onboard');
ff('update', 'task', 'faq', '--status', 'in-progress');
ff('review', 'faq');
ff('update', 'task', 'backups', '--status', 'in-progress', '--due-date', '2026-09-01');
execFileSync(env.FOCUSFLOW_BIN, ['message', 'user', 'Should the pricing table show annual or monthly first?'], { cwd: dirs.work, env: { ...env, FOCUSFLOW_TASK: 'pricing' } });
execFileSync(env.FOCUSFLOW_BIN, ['message', 'user', 'Blocked: need the new vault token.', '--urgent'], { cwd: dirs.work, env: { ...env, FOCUSFLOW_TASK: 'keys' } });

// The dashboard's own config, pointed at this sandbox's port with banners off.
execFileSync(process.execPath, ['--no-warnings', '-e', "import('./lib/config.js').then(m => m.loadConfig())"], { cwd: dashboard, env });
const instFile = path.join(env.FOCUSFLOW_DASHBOARD_HOME, 'instance.json');
const inst = JSON.parse(fs.readFileSync(instFile, 'utf8'));
inst.tunables = { ...inst.tunables, port, notifications: false };
fs.writeFileSync(instFile, JSON.stringify(inst));
const server = spawn(process.execPath, ['--no-warnings', 'server.js'], { cwd: dashboard, env, stdio: 'ignore' });
const BASE = `http://127.0.0.1:${port}`;
for (let i = 0; i < 50; i++) { try { await fetch(BASE); break; } catch { await new Promise(r => setTimeout(r, 200)); } }
const TOKEN = fs.readFileSync(path.join(env.FOCUSFLOW_DASHBOARD_HOME, 'token'), 'utf8').trim();

let failed = true;
try {
  const { results, errors } = await journeys({ BASE, TOKEN, shots: process.env.E2E_SHOTS });
  console.log(results.join('\n'));
  if (errors.length) console.log(`console errors:\n${errors.join('\n')}`);
  failed = results.some(r => r.startsWith('FAIL')) || errors.length > 0;
} finally {
  server.kill();
  fs.rmSync(sb, { recursive: true, force: true });
}
console.log(failed ? '\nE2E FAILED' : '\nE2E PASSED');
process.exit(failed ? 1 : 0);
