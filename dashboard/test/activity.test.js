import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { transcriptPath, isWorking } from '../lib/activity.js';
import { tmpdir } from './helpers.js';

test('transcriptPath replaces every / in work_dir with -, under <home>/.claude/projects', () => {
  const p = transcriptPath({ workDir: '/Users/sam/Documents/random/focusflow', sessionId: 'abc-123', home: '/home/sam' });
  assert.equal(p, '/home/sam/.claude/projects/-Users-sam-Documents-random-focusflow/abc-123.jsonl');
});

test('isWorking is true when the transcript was touched within the window', () => {
  const home = tmpdir('ff-activity-');
  const dir = path.join(home, '.claude', 'projects', '-work-x');
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, 'sess-1.jsonl');
  fs.writeFileSync(file, '{}');
  const now = new Date();
  fs.utimesSync(file, now, now);
  assert.equal(isWorking({ workDir: '/work/x', sessionId: 'sess-1', now, windowMs: 15000, home }), true);
});

test('isWorking is false when the transcript is older than the window', () => {
  const home = tmpdir('ff-activity-');
  const dir = path.join(home, '.claude', 'projects', '-work-x');
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, 'sess-1.jsonl');
  fs.writeFileSync(file, '{}');
  const old = new Date(Date.now() - 60000);
  fs.utimesSync(file, old, old);
  assert.equal(isWorking({ workDir: '/work/x', sessionId: 'sess-1', now: new Date(), windowMs: 15000, home }), false);
});

test('isWorking is false, not thrown, when the transcript file does not exist', () => {
  const home = tmpdir('ff-activity-');
  assert.equal(isWorking({ workDir: '/nope', sessionId: 'nope', now: new Date(), windowMs: 15000, home }), false);
});
