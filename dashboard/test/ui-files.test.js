import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const pub = f => fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'public', f), 'utf8');

test('every $("id") in app.js exists in index.html, and ids are unique', () => {
  const html = pub('index.html');
  const js = pub('app.js') + pub('help.js');
  const used = new Set([...js.matchAll(/\$\('([a-z0-9-]+)'\)/g)].map(m => m[1]));
  const defined = [...html.matchAll(/\sid="([^"]+)"/g)].map(m => m[1]);
  assert.equal(new Set(defined).size, defined.length, 'duplicate ids in index.html');
  for (const id of used) assert.ok(defined.includes(id), `#${id} is used in app.js but missing from index.html`);
});

test('the page has an Inbox toggle with an unread badge next to Capture, and an inbox container', () => {
  const html = pub('index.html');
  assert.match(html, /id="inbox-btn"/);
  assert.match(html, /id="inbox-badge"/);
  assert.match(html, /id="inbox"/);
  assert.ok(html.indexOf('id="inbox-btn"') < html.indexOf('id="capture-btn"'));
});

test('the page loads no inline script or style (the CSP forbids them); the only inline block is inert JSON data', () => {
  const html = pub('index.html');
  assert.ok(!/<script(?![^>]*\ssrc=)(?![^>]*type="application\/json")/.test(html));
  assert.ok(!/<style/.test(html));
  assert.ok(!/\son[a-z]+=/.test(html));
});

test('the drawer has a persistent chat section above a rebuildable details container', () => {
  const html = pub('index.html');
  for (const id of ['chat-state', 'chat-thread', 'chat-note', 'chat-composer', 'chat-text', 'chat-count', 'chat-send', 'chat-queue', 'chat-headless', 'chat-confirm', 'chat-confirm-text', 'chat-confirm-go', 'chat-confirm-cancel', 'chat-error', 'drawer-details']) {
    assert.match(html, new RegExp(`id="${id}"`), id);
  }
  assert.ok(html.indexOf('id="chat-thread"') < html.indexOf('id="drawer-details"'));
  assert.match(html, /id="chat-text"[^>]*maxlength="380"/);
});

test('fillDrawer rebuilds only the details, so a half-typed message survives the 30s reload', () => {
  const js = pub('app.js');
  assert.match(js, /\$\('drawer-details'\)\.replaceChildren/);
  assert.ok(!/\$\('drawer-body'\)\.replaceChildren/.test(js));
});

test('the client mirrors the server text rules', () => {
  const js = pub('app.js');
  assert.match(js, /380/);
  assert.match(js, /startsWith\('-'\)/);
});

test('the header has a sound toggle, off by default', () => {
  const html = pub('index.html');
  assert.match(html, /id="sound-toggle"/);
  assert.ok(!/id="sound-toggle"[^>]*checked/.test(html));
});

test('the tab title and the sound follow the unread count', () => {
  const js = pub('app.js');
  assert.match(js, /document\.title = /);
  assert.match(js, /\(\$\{n\}\) Focusflow/);
  assert.match(js, /AudioContext/);
  assert.match(js, /focusflow-sound/);
});

test('the capture dialog can start a new project inline, hidden until "+ New project" is picked', () => {
  const html = pub('index.html');
  const js = pub('app.js');
  for (const id of ['cap-newproject', 'cap-np-name', 'cap-np-dir', 'cap-np-priority']) assert.match(html, new RegExp(`id="${id}"`), id);
  assert.match(html, /id="cap-newproject"[^>]*\shidden/);
  assert.ok(html.indexOf('id="cap-project"') < html.indexOf('id="cap-newproject"'));
  assert.ok(html.indexOf('id="cap-newproject"') < html.indexOf('id="cap-note"'));
  assert.match(js, /new_project:/);
  assert.match(js, /default_work_dir/);
  assert.match(js, /\+ New project/);
});

test('a capture that created the project but not the task selects that project for the retry', () => {
  const js = pub('app.js');
  assert.match(js, /err\.data/);
  assert.match(js, /data\.project|\.project\b/);
});

test('Send is headless for a stopped claude task (continuation); other harnesses get a hint instead', () => {
  const html = pub('index.html');
  const js = pub('app.js');
  assert.match(html, /id="chat-hint"/);
  assert.match(js, /headlessAllowed = !t\.has_session \|\| t\.harness === 'claude'/);
  assert.match(js, /function canSendHeadless\(t\)/);
  assert.match(js, /if \(canSendHeadless\(thread\)\) askHeadless\(\); else sendChat\('queue'\)/);
  assert.match(js, /\$\('chat-queue'\)\.hidden = [^;\n]*headlessAllowed/);
  assert.match(js, /\$\('chat-hint'\)\.hidden = [^;\n]*headlessAllowed/);
});

test('the drawer state line and board marker distinguish a working live session from an idle one', () => {
  const js = pub('app.js');
  assert.match(js, /t\.live \? \(t\.working \? 'Working' : 'Idle'\)/);
  assert.match(js, /s\.working/);
});

test('the confirmation is skipped once a task has ever run headlessly', () => {
  const js = pub('app.js');
  assert.match(js, /thread\?\.ever_headless/);
});
