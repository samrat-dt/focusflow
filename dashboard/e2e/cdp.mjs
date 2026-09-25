// Minimal Chrome DevTools Protocol driver (Node's built-in WebSocket; no dependencies).
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import net from 'node:net';

const freePort = () => new Promise(resolve => { const s = net.createServer(); s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => resolve(p)); }); });

export async function launch({ port, width = 1400, height = 900 } = {}) {
  port = port ?? await freePort(); // never collide with a Chrome left over from an earlier run
  const prof = fs.mkdtempSync(path.join(os.tmpdir(), 'cdp-'));
  const chrome = spawn('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', [
    '--headless=new', '--disable-gpu', '--no-first-run', `--user-data-dir=${prof}`, `--remote-debugging-port=${port}`, `--window-size=${width},${height}`, 'about:blank',
  ], { stdio: 'ignore' });
  let info;
  for (let i = 0; i < 50 && !info; i++) {
    await new Promise(r => setTimeout(r, 200));
    try { info = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json(); } catch { /* not yet */ }
  }
  if (!info) { chrome.kill('SIGKILL'); throw new Error(`Chrome did not start its debugging port (${port})`); }
  const page = info.find(t => t.type === 'page');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise(r => ws.addEventListener('open', r));
  let id = 0;
  const waiting = new Map();
  const errors = [];
  ws.addEventListener('message', e => {
    const m = JSON.parse(e.data);
    if (m.id && waiting.has(m.id)) { waiting.get(m.id)(m); waiting.delete(m.id); }
    if (m.method === 'Runtime.exceptionThrown') errors.push(m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text);
    if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') errors.push(m.params.args.map(a => a.value ?? a.description).join(' '));
    if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error') errors.push(`${m.params.entry.text} ${m.params.entry.url ?? ''}`);
  });
  const send = (method, params = {}) => new Promise(r => { const i = ++id; waiting.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
  await send('Runtime.enable'); await send('Page.enable'); await send('Log.enable');
  const evaluate = async expr => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.result.exceptionDetails) throw new Error(`${expr}\n→ ${r.result.exceptionDetails.exception?.description ?? r.result.exceptionDetails.text}`);
    return r.result.result.value;
  };
  return {
    send, evaluate, errors,
    goto: async url => { await send('Page.navigate', { url }); await new Promise(r => setTimeout(r, 1500)); },
    wait: ms => new Promise(r => setTimeout(r, ms)),
    until: async (expr, ms = 8000) => { const end = Date.now() + ms; while (Date.now() < end) { if (await evaluate(expr)) return true; await new Promise(r => setTimeout(r, 150)); } throw new Error(`timed out waiting for: ${expr}`); },
    shot: async file => { const r = await send('Page.captureScreenshot', { format: 'png' }); fs.writeFileSync(file, Buffer.from(r.result.data, 'base64')); },
    close: () => { ws.close(); chrome.kill('SIGKILL'); fs.rmSync(prof, { recursive: true, force: true }); },
  };
}
