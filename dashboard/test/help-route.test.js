import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { makeApp, TOKEN } from './harness.js';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

function embedded(html) {
  const m = /<script type="application\/json" id="help-data">([\s\S]*?)<\/script>/.exec(html);
  assert.ok(m, 'the page has no help-data block');
  return JSON.parse(m[1]);
}

test('the page embeds the recovery steps with this server\'s real folder and port, no token needed', async () => {
  const h = await makeApp();
  try {
    const res = await h.get('/', { token: null });
    assert.equal(res.status, 200);
    const help = embedded(res.body);
    assert.ok(help.html.token.includes('focusflow dashboard open'));
    assert.ok(help.html.lost.includes('focusflow dashboard start'));
    assert.ok(help.html.lost.includes(`cd ${ROOT} &amp;&amp; npm start`), 'the fallback names the real project folder');
    assert.ok(help.html.lost.includes(`127.0.0.1:${h.port}`), 'the reload address is the port this server is on');
    assert.match(help.html.lost, /stopped answering/);
    assert.match(help.html.token, /access key/);
  } finally { h.close(); }
});

test('the embedded steps never contain the token (the page is served without one)', async () => {
  const h = await makeApp();
  try {
    const res = await h.get('/', { token: null });
    assert.ok(!res.body.includes(TOKEN));
  } finally { h.close(); }
});

test('a "</script>" in the folder name cannot break out of the embedded data block', async () => {
  const h = await makeApp();
  try {
    const res = await h.get('/', { token: null });
    assert.equal((res.body.match(/<\/script>/g) || []).length, (res.body.match(/<script\b/g) || []).length);
  } finally { h.close(); }
});

test('help.js and help.css are served as static files', async () => {
  const h = await makeApp();
  try {
    const js = await h.get('/help.js', { token: null });
    assert.equal(js.status, 200);
    const css = await h.get('/help.css', { token: null });
    assert.equal(css.status, 200);
    assert.match(css.body, /\.help-steps/);
  } finally { h.close(); }
});
