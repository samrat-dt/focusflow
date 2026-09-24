import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { EventEmitter } from 'node:events';
import { runResume } from '../lib/resume-run.js';
import { tmpdir } from './helpers.js';

function fakeSpawn({ failWith } = {}) {
  const calls = [];
  const spawnFn = (cmd, args, opts) => {
    const child = new EventEmitter();
    child.pid = 4242;
    child.unref = () => { child.unrefed = true; };
    calls.push({ cmd, args, opts, child });
    queueMicrotask(() => {
      if (failWith) child.emit('error', failWith);
      else child.emit('spawn');
    });
    return child;
  };
  return { spawnFn, calls };
}

test('spawns claude --resume with the session id, text and skip-permissions, detached, cwd set', async () => {
  const { spawnFn, calls } = fakeSpawn();
  const logFile = path.join(tmpdir('ff-resume-'), 'sub', 'log.txt');
  const r = await runResume({
    claudeBin: 'claude', sessionId: 'sess-1', text: 'hello world', cwd: '/work/x',
    env: { PATH: '/usr/bin' }, logFile, spawnFn,
  });
  assert.equal(r.pid, 4242);
  assert.equal(calls.length, 1);
  const c = calls[0];
  assert.equal(c.cmd, 'claude');
  assert.deepEqual(c.args, ['--resume', 'sess-1', '-p', 'hello world', '--dangerously-skip-permissions']);
  assert.equal(c.opts.cwd, '/work/x');
  assert.deepEqual(c.opts.env, { PATH: '/usr/bin' });
  assert.equal(c.opts.detached, true);
  assert.equal(c.opts.stdio[0], 'ignore');
  assert.ok(c.child.unrefed, 'the child must be unref()d so it does not keep this process alive');
  assert.ok(fs.existsSync(logFile), 'the log file (and its parent dir) is created');
});

test('a missing claude binary rejects with a clear message and never resolves', async () => {
  const err = Object.assign(new Error('spawn claude ENOENT'), { code: 'ENOENT' });
  const { spawnFn } = fakeSpawn({ failWith: err });
  const logFile = path.join(tmpdir('ff-resume-'), 'log.txt');
  await assert.rejects(
    runResume({ claudeBin: 'claude', sessionId: 's', text: 't', cwd: '/x', env: {}, logFile, spawnFn }),
    /cannot run claude: not found/,
  );
});

test('any other spawn error keeps its own message', async () => {
  const err = new Error('EACCES: permission denied');
  const { spawnFn } = fakeSpawn({ failWith: err });
  const logFile = path.join(tmpdir('ff-resume-'), 'log.txt');
  await assert.rejects(
    runResume({ claudeBin: 'claude', sessionId: 's', text: 't', cwd: '/x', env: {}, logFile, spawnFn }),
    /EACCES: permission denied/,
  );
});
