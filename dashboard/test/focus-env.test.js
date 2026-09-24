import { test } from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import { focusEnv } from '../lib/focus-env.js';

test('focusEnv drops harness session identity so focusflow behaves the same whoever started the server', () => {
  const env = focusEnv({ focusBin: 'focusflow', terminalApp: 'Terminal.app', base: { PATH: '/usr/bin', CLAUDE_CODE_SESSION_ID: 'abc', CODEX_THREAD_ID: 'def', HOME: '/h', KEEP: '1' } });
  assert.equal(env.CLAUDE_CODE_SESSION_ID, undefined);
  assert.equal(env.CODEX_THREAD_ID, undefined);
  assert.equal(env.KEEP, '1');
  assert.equal(env.HOME, '/h');
});

test('focusEnv sets FOCUSFLOW_TERM from config', () => {
  assert.equal(focusEnv({ focusBin: 'focusflow', terminalApp: 'Warp', base: {} }).FOCUSFLOW_TERM, 'Warp');
});

test('focusEnv puts focusflow\'s own directory and the usual install dirs on PATH, keeping the original', () => {
  const env = focusEnv({ focusBin: '/opt/x/bin/focusflow', terminalApp: 'Terminal.app', base: { PATH: '/custom/bin:/usr/bin' } });
  const parts = env.PATH.split(':');
  assert.equal(parts[0], '/opt/x/bin');
  for (const p of ['/usr/local/bin', '/opt/homebrew/bin', path.join(os.homedir(), '.local', 'bin'), '/custom/bin', '/usr/bin', '/bin']) assert.ok(parts.includes(p), p);
  assert.equal(new Set(parts).size, parts.length, 'no duplicates');
});

test('focusEnv does not add a directory for a bare command name', () => {
  const env = focusEnv({ focusBin: 'focusflow', terminalApp: 'Terminal.app', base: { PATH: '/usr/bin' } });
  assert.ok(!env.PATH.split(':').includes('.'));
});

test('focusEnv does not mutate the base environment', () => {
  const base = { PATH: '/usr/bin', CLAUDE_CODE_SESSION_ID: 'abc' };
  focusEnv({ focusBin: 'focusflow', terminalApp: 'Terminal.app', base });
  assert.equal(base.CLAUDE_CODE_SESSION_ID, 'abc');
  assert.equal(base.PATH, '/usr/bin');
});
