import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { runSetup, agentSpec, SetupError } from '../lib/setup.js';
import { loadConfig } from '../lib/config.js';
import { readEvents } from '../lib/event-log.js';
import { state, tmpdir } from './helpers.js';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function env() {
  const home = path.join(tmpdir('ff-setup-'), '.focusflow/dashboard');
  const repoRoot = tmpdir('ff-setup-repo-');
  const tasks = [state({ slug: 'a', name: 'A' }), state({ slug: 'b', name: 'B' })];
  return { home, repoRoot, readTasks: () => ({ tasks, live: new Set() }), focusVersion: () => 'v9.9.9-test', now: () => new Date(2026, 8, 22, 10, 0) };
}

test('setup creates config, data dir and the first snapshot, and reports each step', async () => {
  const o = env();
  const r = await runSetup(o);
  assert.match(r.device, /^dev-[0-9a-f]{8}$/);
  assert.equal(r.tokenCreated, true);
  assert.equal(r.focusVersion, 'v9.9.9-test');
  assert.equal(r.dataDir, path.join(o.home, 'data'));
  const kinds = readEvents(r.dataDir).events.map(e => e.kind);
  assert.deepEqual(kinds, ['instance.created', 'task.appeared', 'task.appeared', 'snapshot.daily']);
  assert.equal(r.tasks, 2);
  assert.match(r.url, /^http:\/\/127\.0\.0\.1:4177\/$/);
});

test('setup is idempotent: same device and token, no new events', async () => {
  const o = env();
  const a = await runSetup(o);
  const before = readEvents(a.dataDir).events.length;
  const b = await runSetup(o);
  assert.equal(b.device, a.device);
  assert.equal(b.tokenCreated, false);
  assert.equal(readEvents(a.dataDir).events.length, before);
});

test('setup fails clearly when focusflow cannot be read, and creates no data', async () => {
  const o = env();
  o.readTasks = () => { throw new Error('cannot run focusflow (focusflow): not found'); };
  await assert.rejects(runSetup(o), e => e instanceof SetupError && /focusflow/.test(e.message) && /FOCUSFLOW_BIN/.test(e.message));
  assert.equal(fs.existsSync(path.join(o.repoRoot, 'data')), false);
});

// ---- replicability: same code, different data per machine ----

function sourceFiles() {
  const out = [path.join(ROOT, 'server.js'), path.join(ROOT, 'package.json')];
  const walk = dir => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (/\.(js|css|html|json|md|py)$/.test(e.name)) out.push(p); // text only; fonts and licences are not code
    }
  };
  for (const d of ['lib', 'bin', 'public']) walk(path.join(ROOT, d));
  return out;
}

test('nothing in the code is hard-coded to this machine or user', () => {
  const user = os.userInfo().username;
  for (const f of sourceFiles()) {
    const text = fs.readFileSync(f, 'utf8');
    assert.ok(!/\/Users\//.test(text), `${path.relative(ROOT, f)} mentions /Users/`);
    assert.ok(!text.includes(`/${user}/`) && !new RegExp(`\\b${user}\\b`, 'i').test(text), `${path.relative(ROOT, f)} mentions the local user name`);
  }
});

test('agentSpec gives the login item an absolute focusflow path, FOCUSFLOW_TERM, and only the env overrides that are set', () => {
  const bin = tmpdir('ff-agent-bin-');
  fs.writeFileSync(path.join(bin, 'myflow'), '#!/bin/sh\n', { mode: 0o755 });
  const home = path.join(tmpdir('ff-agent-'), '.focusflow/dashboard');
  const repoRoot = tmpdir('ff-agent-repo-');
  const config = { ...loadConfig({ home, repoRoot }), focusBin: 'myflow', terminalApp: 'Warp' };
  const spec = agentSpec({ config, repoRoot, nodePath: '/n/node', base: { PATH: bin } });
  assert.equal(spec.env.FOCUSFLOW_BIN, path.join(bin, 'myflow'));
  assert.equal(spec.env.FOCUSFLOW_TERM, 'Warp');
  assert.equal(spec.nodePath, '/n/node');
  assert.equal(spec.serverPath, path.join(repoRoot, 'server.js'));
  assert.equal(spec.logPath, path.join(home, 'server.log'));
  assert.ok(!('FOCUSFLOW_DASHBOARD_HOME' in spec.env) && !('FOCUSFLOW_ROOT' in spec.env));
  const withEnv = agentSpec({ config, repoRoot, base: { PATH: bin, FOCUSFLOW_DASHBOARD_HOME: '/x/cc', FOCUSFLOW_ROOT: '/x/focusflow' } });
  assert.equal(withEnv.env.FOCUSFLOW_DASHBOARD_HOME, '/x/cc');
  assert.equal(withEnv.env.FOCUSFLOW_ROOT, '/x/focusflow');
});

test('agentSpec refuses to write a login item that could not find focusflow', () => {
  const home = path.join(tmpdir('ff-agent-'), '.focusflow/dashboard');
  const repoRoot = tmpdir('ff-agent-repo-');
  const config = { ...loadConfig({ home, repoRoot }), focusBin: 'definitely-not-installed-focusflow' };
  assert.throws(() => agentSpec({ config, repoRoot, base: { PATH: '/nonexistent' } }), e => e instanceof SetupError && /FOCUSFLOW_BIN/.test(e.message));
});
