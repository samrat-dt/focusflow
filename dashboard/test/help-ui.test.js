import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';
import { helpContent, appFragments } from '../lib/help.js';

const PUBLIC = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'public');
const read = f => fs.readFileSync(path.join(PUBLIC, f), 'utf8');
const DIR = '/Users/someone/code/focusflow';
const TOKEN = 'k'.repeat(64);

// Just enough DOM to run public/help.js. The element ids come from the real index.html,
// so a renamed or missing id fails here.
class El {
  constructor(id = null, hidden = false) { this.id = id; this.hidden = hidden; this.innerHTML = ''; this.textContent = ''; this.children = []; this.attrs = {}; this.listeners = {}; this.className = ''; this.focused = false; }
  append(...kids) { this.children.push(...kids); }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  click() { for (const fn of this.listeners.click || []) fn({ target: this }); }
  focus() { this.focused = true; }
}

function page({ withSteps = true, fetchImpl } = {}) {
  const html = read('ui/index.html');
  const els = {};
  for (const m of html.matchAll(/<[^>]*\sid="([^"]+)"[^>]*>/g)) els[m[1]] = new El(m[1], /\shidden(\s|>|=)/.test(m[0]));
  els['help-data'].textContent = withSteps ? JSON.stringify({ html: appFragments(helpContent({ projectDir: DIR, port: 4177 })) }) : '';
  const docListeners = {};
  const winListeners = {};
  const timers = [];
  const copied = [];
  const ctx = {
    document: {
      getElementById: id => { assert.ok(els[id], `#${id} is not in ui/index.html`); return els[id]; },
      createElement: () => new El(),
      addEventListener: (t, fn) => { (docListeners[t] ||= []).push(fn); },
    },
    window: { addEventListener: (t, fn) => { (winListeners[t] ||= []).push(fn); } },
    navigator: { clipboard: { writeText: async t => { copied.push(t); } } },
    fetch: fetchImpl ?? (async () => ({ status: 200, ok: true, json: async () => ({}) })),
    setTimeout: fn => { timers.push(fn); return timers.length; },
  };
  vm.createContext(ctx);
  const Help = vm.runInContext(`${read('help.js')}\nHelp;`, ctx);
  return {
    Help, els, copied, timers, ctx,
    // a click on a Copy button, as the browser delivers it to the document
    clickCopy: button => { const e = { target: { closest: sel => (sel === '[data-copy]' ? button : null) } }; for (const fn of docListeners.click || []) fn(e); },
    storage: key => { for (const fn of winListeners.storage || []) fn({ key }); },
  };
}

const respond = status => async () => ({ status, ok: status >= 200 && status < 300, json: async () => ({}) });

test('no token: the full-screen panel shows, says what happened, lists the steps and the real command; nothing is sent', async () => {
  let calls = 0;
  const p = page({ fetchImpl: async () => { calls++; return { status: 200 }; } });
  await assert.rejects(p.Help.request('/api/tasks', { token: null }), e => e.code === 'no-token' && e.helped === true);
  assert.equal(calls, 0, 'no request should go out without a token');
  const panel = p.els['token-panel'];
  assert.equal(panel.hidden, false);
  const body = p.els['token-panel-body'].innerHTML;
  assert.match(body, /Chrome forgot the access token/);
  assert.match(body, /does not have its access token/);
  assert.match(body, /<ol class="help-steps">/);
  assert.equal((body.match(/<li>/g) || []).length, 3);
  assert.match(body, /Open Terminal/);
  assert.ok(body.includes(`cd ${DIR} &amp;&amp; npm run url -- --open`), 'the command names the real folder');
  assert.match(body, /class="help-copy"/);
  assert.match(body, /Focusflow\.app/, 'the one-click launcher is offered');
});

test('a rejected token (401) shows the same panel', async () => {
  const p = page({ fetchImpl: respond(401) });
  await assert.rejects(p.Help.request('/api/tasks', { token: TOKEN }), e => e.code === 'bad-token' && e.helped === true);
  assert.equal(p.els['token-panel'].hidden, false);
  assert.match(p.els['token-panel-body'].innerHTML, /access token/);
});

test('the panel has a "check again" button that hands control back to the app', async () => {
  const p = page();
  let retried = 0;
  p.Help.init({ onRetry: () => { retried++; } });
  await assert.rejects(p.Help.request('/api/x', { token: null }));
  const button = p.els['token-panel-body'].children[0].children[0];
  assert.match(button.textContent, /check again/);
  assert.equal(button.focused, true);
  button.click();
  assert.equal(retried, 1);
});

test('the panel clears itself when a request is accepted, and when another tab stores a token', async () => {
  let status = 401;
  const p = page({ fetchImpl: async () => ({ status, ok: status === 200 }) });
  let retried = 0;
  p.Help.init({ onRetry: () => { retried++; } });
  await assert.rejects(p.Help.request('/api/x', { token: TOKEN }));
  assert.equal(p.Help.isPanelUp(), true);
  p.storage('something-else');
  assert.equal(retried, 0);
  p.storage('focusflow-token');
  assert.equal(retried, 1, 'a token stored by another tab should trigger a fresh look');
  status = 200;
  await p.Help.request('/api/x', { token: TOKEN });
  assert.equal(p.els['token-panel'].hidden, true);
  assert.equal(p.Help.isPanelUp(), false);
});

test('showing the panel twice does not add a second button', async () => {
  const p = page();
  await assert.rejects(p.Help.request('/a', { token: null }));
  await assert.rejects(p.Help.request('/b', { token: null }));
  assert.equal(p.els['token-panel-body'].children.length, 1);
});

test('the server not answering: a visible banner with the start-it-again steps', async () => {
  const p = page({ fetchImpl: async () => { throw new TypeError('Failed to fetch'); } });
  assert.equal(p.els['lost-banner'].hidden, true);
  await assert.rejects(p.Help.request('/api/tasks', { token: TOKEN }), e => e.code === 'offline' && e.helped === true);
  assert.equal(p.els['lost-banner'].hidden, false);
  assert.equal(p.Help.isLost(), true);
  const body = p.els['lost-banner-body'].innerHTML;
  assert.match(body, /stopped answering/);
  assert.match(body, /<ol class="help-steps">/);
  assert.ok(body.includes('launchctl kickstart -k gui/$(id -u)/local.focusflow.dashboard'));
  assert.match(body, /Reload 127\.0\.0\.1:4177/);
  assert.match(body, /class="help-copy"/);
  assert.equal(p.els['token-panel'].hidden, true, 'a dead server is not a token problem');
});

test('the banner clears itself when the server answers again, and the app is told to refresh once', async () => {
  let up = false;
  const p = page({ fetchImpl: async () => { if (!up) throw new TypeError('Failed to fetch'); return { status: 200, ok: true }; } });
  let recovered = 0;
  p.Help.init({ onRecovered: () => { recovered++; } });
  await assert.rejects(p.Help.request('/api/tasks', { token: TOKEN }));
  await assert.rejects(p.Help.request('/api/inbox', { token: TOKEN }));
  assert.equal(p.els['lost-banner'].hidden, false);
  assert.equal(recovered, 0);
  up = true;
  await p.Help.request('/api/inbox', { token: TOKEN });
  assert.equal(p.els['lost-banner'].hidden, true);
  assert.equal(recovered, 1);
  await p.Help.request('/api/tasks', { token: TOKEN });
  assert.equal(recovered, 1, 'only the transition counts');
});

test('a server that answers with an error is still "up": no banner', async () => {
  const p = page({ fetchImpl: respond(500) });
  await p.Help.request('/api/tasks', { token: TOKEN });
  assert.equal(p.els['lost-banner'].hidden, true);
});

test('the request carries the token and passes method, headers and body through', async () => {
  let seen;
  const p = page({ fetchImpl: async (u, o) => { seen = [u, o]; return { status: 200, ok: true }; } });
  await p.Help.request('/api/capture', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{"a":1}', token: TOKEN });
  assert.equal(seen[0], '/api/capture');
  assert.equal(seen[1].method, 'POST');
  assert.equal(seen[1].headers['x-focusflow-token'], TOKEN);
  assert.equal(seen[1].headers['content-type'], 'application/json');
  assert.equal(seen[1].body, '{"a":1}');
});

test('Copy puts exactly the command on the clipboard and confirms, then reverts', async () => {
  const p = page();
  await assert.rejects(p.Help.request('/a', { token: null }));
  const cmd = `cd ${DIR} && npm run url -- --open`;
  const button = { textContent: 'Copy', getAttribute: k => (k === 'data-copy' ? cmd : null), parentNode: { querySelector: () => ({}) } };
  p.clickCopy(button);
  await new Promise(r => setImmediate(r));
  assert.deepEqual(p.copied, [cmd]);
  assert.equal(button.textContent, 'Copied');
  p.timers.forEach(fn => fn());
  assert.equal(button.textContent, 'Copy');
});

test('if the clipboard is refused, the button says to press Command-C instead of pretending', async () => {
  const p = page();
  p.ctx.navigator.clipboard.writeText = async () => { throw new Error('denied'); };
  const button = { textContent: 'Copy', getAttribute: () => 'cmd', parentNode: { querySelector: () => ({}) } };
  p.clickCopy(button);
  await new Promise(r => setImmediate(r));
  assert.equal(button.textContent, 'Press Command-C');
});

test('if the embedded steps are missing, the panel still explains where the help file is', async () => {
  const p = page({ withSteps: false });
  await assert.rejects(p.Help.request('/a', { token: null }));
  assert.equal(p.els['token-panel'].hidden, false);
  assert.match(p.els['token-panel-body'].textContent, /Help file/);
});

test('the app sends every request through Help.request, and help loads first with its styles', () => {
  const api = read('ui/js/api.js');
  assert.match(api, /helpApi\(\)\.request\(/);
  const fetches = [...api.matchAll(/fetch\(/g)].length;
  assert.equal(fetches, 1, 'the only direct fetch is the page-unload beacon');
  assert.match(api, /keepalive: true/);
  const main = read('ui/js/main.js');
  assert.match(main, /helpApi\(\)\.init\(\{ onRecovered: \(\) => load\(\)/);
  const html = read('ui/index.html');
  assert.ok(html.indexOf('src="/help.js"') < html.indexOf('src="/ui/js/main.js"'), 'help.js must load before the app');
  assert.match(html, /href="\/help\.css"/);
  for (const id of ['help-data', 'token-panel', 'token-panel-body', 'lost-banner', 'lost-banner-body']) assert.match(html, new RegExp(`id="${id}"`), id);
  assert.ok(!/<script>(?!\s*<\/script>)|<style>|\sstyle="|\son[a-z]+="/i.test(html), 'no inline script, style or handlers (the CSP forbids them)');
});
