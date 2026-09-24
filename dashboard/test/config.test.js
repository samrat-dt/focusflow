import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { loadConfig, DEFAULTS } from '../lib/config.js';
import { tmpdir } from './helpers.js';

test('first load creates the per-machine home with instance.json and a mode-600 token', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  const c = loadConfig({ home, repoRoot: repo });
  assert.match(c.device, /^dev-[0-9a-f]{8}$/);
  assert.match(c.token, /^[0-9a-f]{64}$/);
  assert.equal(fs.statSync(path.join(home, 'token')).mode & 0o777, 0o600);
  assert.equal(c.dataDir, path.join(home, 'data'), 'the event log lives with the rest of the dashboard state');
  assert.equal(c.cacheDb, path.join(home, 'cache', 'store.db'));
  assert.deepEqual(c.tunables, DEFAULTS.tunables);
  assert.ok(fs.existsSync(path.join(home, 'instance.json')));
});

test('loading again is idempotent: same device and token', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  const a = loadConfig({ home, repoRoot: repo });
  const b = loadConfig({ home, repoRoot: repo });
  assert.equal(a.device, b.device);
  assert.equal(a.token, b.token);
});

test('instance.json overrides tunables and data_dir; unknown tunables keep defaults', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  loadConfig({ home, repoRoot: repo });
  const f = path.join(home, 'instance.json');
  const j = JSON.parse(fs.readFileSync(f, 'utf8'));
  j.data_dir = '/somewhere/else';
  j.tunables = { port: 5001, stale_days: 9 };
  fs.writeFileSync(f, JSON.stringify(j));
  const c = loadConfig({ home, repoRoot: repo });
  assert.equal(c.dataDir, '/somewhere/else');
  assert.equal(c.tunables.port, 5001);
  assert.equal(c.tunables.stale_days, 9);
  assert.equal(c.tunables.tick_minutes, DEFAULTS.tunables.tick_minutes);
});

test('a corrupt instance.json is an error, not silently replaced', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  loadConfig({ home, repoRoot: repo });
  fs.writeFileSync(path.join(home, 'instance.json'), '{nope');
  assert.throws(() => loadConfig({ home, repoRoot: repo }), /instance\.json/);
});

test('cache_db in instance.json overrides where the disposable cache lives', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  loadConfig({ home, repoRoot: repo });
  const f = path.join(home, 'instance.json');
  const j = JSON.parse(fs.readFileSync(f, 'utf8'));
  j.cache_db = '/elsewhere/store.db';
  fs.writeFileSync(f, JSON.stringify(j));
  assert.equal(loadConfig({ home, repoRoot: repo }).cacheDb, '/elsewhere/store.db');
});

test('working_window_seconds and nudge_stall_seconds have defaults', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  const c = loadConfig({ home, repoRoot: repo });
  assert.equal(c.tunables.working_window_seconds, 15);
  assert.equal(c.tunables.nudge_stall_seconds, 30);
});

test('claudeBin defaults to "claude" and honors CLAUDE_BIN', () => {
  const home = path.join(tmpdir(), '.focusflow/dashboard');
  const repo = tmpdir('ff-repo-');
  const prev = process.env.CLAUDE_BIN;
  delete process.env.CLAUDE_BIN;
  try {
    assert.equal(loadConfig({ home, repoRoot: repo }).claudeBin, 'claude');
    process.env.CLAUDE_BIN = '/opt/bin/claude';
    assert.equal(loadConfig({ home: path.join(tmpdir(), '.focusflow/dashboard'), repoRoot: repo }).claudeBin, '/opt/bin/claude');
  } finally {
    if (prev === undefined) delete process.env.CLAUDE_BIN; else process.env.CLAUDE_BIN = prev;
  }
});
