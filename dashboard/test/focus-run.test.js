import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { runFocus } from '../lib/focus-run.js';
import { tmpdir } from './helpers.js';

function stub(body) {
  const f = path.join(tmpdir('ff-run-'), 'focusflow-stub');
  fs.writeFileSync(f, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
  return f;
}

test('resolves with stdout on success', async () => {
  const bin = stub('echo "opened: $1 $2"');
  const r = await runFocus(['do', 'my-task'], { focusBin: bin, env: process.env, timeoutMs: 5000 });
  assert.equal(r.stdout.trim(), 'opened: do my-task');
});

test('rejects with focusflow\'s stderr text, unchanged, on failure', async () => {
  const bin = stub('echo "error: project \\"nope\\" not found" >&2; exit 1');
  await assert.rejects(runFocus(['x'], { focusBin: bin, env: process.env, timeoutMs: 5000 }), e => {
    assert.equal(e.message, 'error: project "nope" not found');
    return true;
  });
});

test('multi-line errors are kept whole', async () => {
  const bin = stub('printf "line one\\nline two\\n" >&2; exit 2');
  await assert.rejects(runFocus(['x'], { focusBin: bin, env: process.env, timeoutMs: 5000 }), /line one\nline two/);
});

test('arguments go as argv, never through a shell', async () => {
  const bin = stub('printf "%s|" "$@"');
  const evil = 'a; echo pwned $(id)';
  const r = await runFocus(['add', evil], { focusBin: bin, env: process.env, timeoutMs: 5000 });
  assert.equal(r.stdout, `add|${evil}|`);
});

test('times out', async () => {
  const bin = stub('sleep 5');
  await assert.rejects(runFocus(['x'], { focusBin: bin, env: process.env, timeoutMs: 200 }), /timed out/);
});

test('a missing binary gives a clear error', async () => {
  await assert.rejects(runFocus(['x'], { focusBin: '/nonexistent/focusflow', env: process.env, timeoutMs: 1000 }), /cannot run focusflow/);
});

test('the given environment is what focusflow sees', async () => {
  const bin = stub('echo "$FOCUSFLOW_TERM"');
  const r = await runFocus([], { focusBin: bin, env: { PATH: process.env.PATH, FOCUSFLOW_TERM: 'Warp' }, timeoutMs: 5000 });
  assert.equal(r.stdout.trim(), 'Warp');
});
