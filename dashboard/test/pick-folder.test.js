import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import { pickFolderScript, parsePick } from '../lib/pick-folder.js';
import { makeApp } from './harness.js';

let h;
afterEach(() => { h?.close(); });

test('the script opens the native chooser at a valid start folder, quoting safely', () => {
  const s = pickFolderScript({ prompt: 'Pick "a" \\ dir', start: os.tmpdir() });
  assert.equal(s[0], 'tell application "Finder" to activate');
  assert.match(s[1], /choose folder with prompt "Pick \\"a\\" \\\\ dir" default location POSIX file "/);
  assert.equal(s[2], 'return POSIX path of f');
  assert.ok(pickFolderScript({ start: '/definitely/not/here' })[1].includes(os.homedir()), 'a bad start falls back to home');
  assert.ok(pickFolderScript({ start: 'relative' })[1].includes(os.homedir()));
});

test('parsePick strips the trailing slash, recognises cancel, and reports real failures', () => {
  assert.deepEqual(parsePick({ stdout: '/Users/me/Work/\n' }), { path: '/Users/me/Work' });
  assert.deepEqual(parsePick({ stdout: '/\n' }), { path: '/' });
  assert.deepEqual(parsePick({ code: 1, stderr: 'execution error: User canceled. (-128)' }), { cancelled: true });
  assert.throws(() => parsePick({ code: 1, stderr: 'boom' }), /boom/);
  assert.throws(() => parsePick({ stdout: 'not a path' }), /no path/);
});

test('POST /api/pick-folder returns the chosen path, or cancelled, and serialises dialogs', async () => {
  let n = 0;
  let release;
  const gate = new Promise(r => { release = r; });
  h = await makeApp({ tasks: [], pickFolder: async opts => {
    n += 1;
    if (opts.start === '/slow') { await gate; return { path: '/slow/one' }; }
    return opts.start === '/cancel' ? { cancelled: true } : { path: '/Users/me/New Project' };
  } });
  const ok = await h.post('/api/pick-folder', { start: '/Users/me', prompt: 'Where?' });
  assert.deepEqual([ok.status, ok.json()], [200, { path: '/Users/me/New Project' }]);
  assert.deepEqual((await h.post('/api/pick-folder', { start: '/cancel' })).json(), { cancelled: true });

  const first = h.post('/api/pick-folder', { start: '/slow' });
  await new Promise(r => setTimeout(r, 30));
  const second = await h.post('/api/pick-folder', {});
  assert.equal(second.status, 409, 'only one dialog at a time');
  release();
  assert.equal((await first).json().path, '/slow/one');
  assert.equal(n, 3);
  assert.equal((await h.get('/api/pick-folder')).status, 405);
  assert.equal((await h.post('/api/pick-folder', {}, { token: 'x'.repeat(64) })).status, 401);
});

test('a failing dialog is a 502 with its message', async () => {
  h = await makeApp({ tasks: [], pickFolder: async () => { throw new Error('the folder dialog failed'); } });
  const r = await h.post('/api/pick-folder', {});
  assert.deepEqual([r.status, r.json().error], [502, 'the folder dialog failed']);
});
