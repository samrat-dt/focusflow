import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { buildPlist, installAgent, uninstallAgent, agentPath, LABEL } from '../lib/launchd.js';
import { resolveFocusBin } from '../lib/focus-env.js';
import { tmpdir } from './helpers.js';

const opts = {
  label: LABEL, nodePath: '/opt/node/bin/node', serverPath: '/code/cc/server.js', workDir: '/code/cc',
  logPath: '/home/u/.focusflow/dashboard/server.log',
  env: { PATH: '/usr/bin:/bin', FOCUSFLOW_TERM: 'Terminal.app', FOCUSFLOW_BIN: '/opt/focusflow/bin/focusflow' },
};

test('the plist runs node on server.js at login, restarts it, and sets FOCUSFLOW_TERM and FOCUSFLOW_BIN', () => {
  const x = buildPlist(opts);
  assert.match(x, /<\?xml version="1\.0" encoding="UTF-8"\?>/);
  assert.match(x, /<key>Label<\/key>\s*<string>local\.focusflow\.dashboard<\/string>/);
  assert.match(x, /<key>RunAtLoad<\/key>\s*<true\/>/);
  assert.match(x, /<key>KeepAlive<\/key>\s*<true\/>/);
  assert.match(x, /<key>ThrottleInterval<\/key>\s*<integer>30<\/integer>/);
  assert.match(x, /<string>\/opt\/node\/bin\/node<\/string>\s*<string>\/code\/cc\/server\.js<\/string>/);
  assert.match(x, /<key>WorkingDirectory<\/key>\s*<string>\/code\/cc<\/string>/);
  assert.match(x, /<key>FOCUSFLOW_TERM<\/key>\s*<string>Terminal\.app<\/string>/);
  assert.match(x, /<key>FOCUSFLOW_BIN<\/key>\s*<string>\/opt\/focusflow\/bin\/focusflow<\/string>/);
  assert.match(x, /<key>StandardOutPath<\/key>\s*<string>\/home\/u\/\.focusflow\/dashboard\/server\.log<\/string>/);
});

test('values are XML-escaped', () => {
  const x = buildPlist({ ...opts, workDir: '/code/a&b <c>', env: { ...opts.env, X: `"q" & 'r'` } });
  assert.match(x, /\/code\/a&amp;b &lt;c&gt;/);
  assert.match(x, /&quot;q&quot; &amp; &apos;r&apos;/);
  assert.ok(!/a&b/.test(x));
});

test('agentPath is under ~/Library/LaunchAgents', () => {
  assert.equal(agentPath('/home/u'), '/home/u/Library/LaunchAgents/local.focusflow.dashboard.plist');
});

test('install writes the plist, unloads any old copy (errors ignored), then loads it', async () => {
  const home = tmpdir('ff-la-');
  const file = agentPath(home);
  const calls = [];
  const run = async (cmd, args) => { calls.push([cmd, ...args]); if (args[0] === 'bootout') throw new Error('not loaded'); return { stdout: '' }; };
  await installAgent({ plist: buildPlist(opts), file, label: LABEL, uid: 501, run });
  assert.equal(fs.readFileSync(file, 'utf8'), buildPlist(opts));
  assert.deepEqual(calls, [
    ['launchctl', 'bootout', `gui/501/${LABEL}`],
    ['launchctl', 'bootstrap', 'gui/501', file],
  ]);
});

test('a failing bootstrap is reported', async () => {
  const home = tmpdir('ff-la-');
  const run = async (cmd, args) => { if (args[0] === 'bootstrap') throw new Error('Bootstrap failed: 5'); return { stdout: '' }; };
  await assert.rejects(installAgent({ plist: 'x', file: agentPath(home), label: LABEL, uid: 501, run }), /Bootstrap failed/);
});

test('uninstall unloads and removes the plist; running it twice is fine', async () => {
  const home = tmpdir('ff-la-');
  const file = agentPath(home);
  const run = async () => ({ stdout: '' });
  await installAgent({ plist: 'x', file, label: LABEL, uid: 501, run });
  await uninstallAgent({ file, label: LABEL, uid: 501, run });
  assert.equal(fs.existsSync(file), false);
  await uninstallAgent({ file, label: LABEL, uid: 501, run });
});

test('resolveFocusBin finds an executable on PATH, passes absolute paths through, and returns null when missing', () => {
  const dir = tmpdir('ff-bin-');
  const bin = path.join(dir, 'focusflow');
  fs.writeFileSync(bin, '#!/bin/sh\n', { mode: 0o755 });
  assert.equal(resolveFocusBin('focusflow', { PATH: `/nonexistent:${dir}` }), bin);
  assert.equal(resolveFocusBin(bin, { PATH: '' }), bin);
  assert.equal(resolveFocusBin('focusflow', { PATH: '/nonexistent' }), null);
  assert.equal(resolveFocusBin('/nonexistent/focusflow', { PATH: dir }), null);
  fs.chmodSync(bin, 0o644);
  assert.equal(resolveFocusBin('focusflow', { PATH: dir }), null, 'not executable');
});
