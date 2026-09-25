import { launch } from './cdp.mjs';

// The core journeys, driven in a real (headless) Chrome against a sandboxed server. See run.mjs.
export async function journeys({ BASE, TOKEN, shots }) {
const api = async (p, method = 'GET', body) => (await fetch(BASE + p, { method, headers: { 'x-focusflow-token': TOKEN, 'content-type': 'application/json', host: new URL(BASE).host }, body: body && JSON.stringify(body) })).json();
const results = [];
const step = async (name, fn) => { try { await fn(); results.push(`PASS ${name}`); } catch (err) { results.push(`FAIL ${name}: ${err.message.split('\n').slice(-1)[0]}`); } };
const b = await launch();
const $ = sel => `document.querySelector(${JSON.stringify(sel)})`;
const clickText = (sel, text) => `(() => { const el = [...document.querySelectorAll(${JSON.stringify(sel)})].find(n => n.textContent.trim().includes(${JSON.stringify(text)})); if (!el) throw new Error('no ' + ${JSON.stringify(text)}); el.click(); return true; })()`;
const inRow = (rowText, btnText) => `(() => { const row = [...document.querySelectorAll('.attn')].find(r => r.textContent.includes(${JSON.stringify(rowText)})); if (!row) throw new Error('no row ' + ${JSON.stringify(rowText)}); const b = [...row.querySelectorAll('button')].find(x => x.textContent.trim().includes(${JSON.stringify(btnText)})); if (!b) throw new Error('no button ' + ${JSON.stringify(btnText)}); b.click(); return true; })()`;
const typeInto = (sel, text) => `(() => { const el = ${$(sel)}; el.focus(); el.value = ${JSON.stringify(text)}; el.dispatchEvent(new Event('input', { bubbles: true })); return true; })()`;
const key = (sel, k, extra = {}) => `(() => { const el = ${sel ? $(sel) : 'document.activeElement || document.body'}; el.dispatchEvent(new KeyboardEvent('keydown', { key: ${JSON.stringify(k)}, bubbles: true, ...${JSON.stringify(extra)} })); return true; })()`;

await b.goto(`${BASE}/#token=${TOKEN}`);
await step('Home loads with Needs you items', async () => { await b.until(`document.querySelectorAll('.attn').length >= 2`); });

await step('Approve shows Undo, and Undo cancels the approval', async () => {
  await b.evaluate(inRow('Write onboarding email', 'Approve'));
  await b.until(`[...document.querySelectorAll('.toast')].some(t => t.textContent.includes('Approved'))`);
  await b.evaluate(clickText('.toast .btn', 'Undo'));
  await b.wait(7500);
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'onboard');
  if (t.status !== 'review') throw new Error(`status became ${t.status}`);
  await b.until(`[...document.querySelectorAll('.attn-title')].some(n => n.textContent.includes('Write onboarding email'))`);
});

await step('Approve without Undo really approves after the window', async () => {
  await b.evaluate(inRow('Write onboarding email', 'Approve'));
  await b.wait(8000);
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'onboard');
  if (t.status !== 'done') throw new Error(`status is ${t.status}`);
});

await step('Snooze hides an item and it can be brought back', async () => {
  const before = await b.evaluate(`document.querySelectorAll('.attn').length`);
  await b.evaluate(`(() => { const row = [...document.querySelectorAll('.attn')].find(r => r.textContent.includes('Audit backups')); [...row.querySelectorAll('button')].find(x => x.textContent.trim() === 'Later').click(); return true; })()`);
  await b.until(`!!document.querySelector('.menu-pop')`);
  await b.evaluate(clickText('.menu-pop button', 'In 1 hour'));
  await b.until(`document.querySelectorAll('.attn').length === ${before - 1}`);
  await b.evaluate(clickText('.btn', 'Show'));
  await b.evaluate(clickText('.btn', 'Bring back'));
  await b.until(`document.querySelectorAll('.attn').length === ${before}`);
});

await step('Reply inline sends, starts background work, and is remembered', async () => {
  await b.evaluate(`(() => { const row = [...document.querySelectorAll('.attn')].find(r => r.textContent.includes('Rotate API keys')); [...row.querySelectorAll('button')].find(x => x.textContent.trim() === 'Reply').click(); return true; })()`);
  await b.until(`!!document.querySelector('.reply-box textarea')`);
  await b.evaluate(typeInto('.reply-box textarea', 'The token is in 1Password under Vault'));
  await b.evaluate(clickText('.reply-box .btn', 'Send'));
  await b.until(`[...document.querySelectorAll('.toast')].some(t => t.textContent.includes('Sent'))`, 15000);
  const mem = (await api('/api/memories?task=keys')).memories;
  if (!mem.some(m => m.kind === 'answer' && m.body.includes('1Password') && m.question?.includes('vault'))) throw new Error(JSON.stringify(mem));
});

await step('A background run that ends cleanly is not reported as a failure', async () => {
  await b.wait(6000); // the fake claude works for 3s, then the status is re-read on the next refresh
  await fetch(BASE + '/api/refresh', { method: 'POST', headers: { 'x-focusflow-token': TOKEN, host: new URL(BASE).host } });
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'keys');
  if (t.auto_run_status !== 'finished') throw new Error(`auto_run_status is ${t.auto_run_status}`);
  const n = await api('/api/needs-you');
  if (n.items.some(i => i.kind === 'failed')) throw new Error('a clean run shows as failed on Home');
});

await step('Request changes shows proof of work, sends the feedback back and saves a house rule', async () => {
  await b.evaluate(`location.hash = '#/task/faq'; true`);
  await b.until(`document.body.textContent.includes('Ready for your review')`);
  await b.evaluate(clickText('.btn', 'Request changes'));
  await b.until(`!!document.querySelector('.dialog textarea')`);
  await b.evaluate(typeInto('.dialog textarea', 'Answers must link to the docs'));
  await b.evaluate(`(() => { const c = document.querySelector('#as-rule'); c.checked = true; c.dispatchEvent(new Event('change')); return true; })()`);
  await b.evaluate(clickText('.dialog .btn', 'Send back'));
  await b.until(`[...document.querySelectorAll('.toast')].some(t => t.textContent.includes('house rule'))`, 15000);
  const mem = (await api('/api/memories?project=website')).memories;
  if (!mem.some(m => m.kind === 'rule' && m.body === 'Answers must link to the docs')) throw new Error(JSON.stringify(mem));
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'faq');
  if (t.status !== 'in-progress') throw new Error(`stage is ${t.status}`);
});

await step('N opens New task; Enter adds it to Up next with #project !priority', async () => {
  await b.evaluate(`document.activeElement?.blur(); true`);
  await b.evaluate(key(null, 'n'));
  await b.until(`!!document.querySelector('.dialog input')`);
  await b.evaluate(typeInto('.dialog input', 'Write release notes #website !high'));
  await b.evaluate(key('.dialog input', 'Enter'));
  await b.wait(2500);
  const t = (await api('/api/tasks')).tasks.find(x => x.name === 'Write release notes');
  if (!t || t.project !== 'website' || t.priority !== 'high' || t.status !== 'next') throw new Error(JSON.stringify(t));
});

await step('Task page: rename, priority and tags save', async () => {
  await b.evaluate(`location.hash = '#/task/launch'; true`);
  await b.until(`!!document.querySelector('.title-edit')`);
  await b.evaluate(`(() => { const t = document.querySelector('.title-edit'); t.value = 'Draft the launch announcement'; t.dispatchEvent(new Event('change')); return true; })()`);
  await b.wait(2000);
  await b.evaluate(clickText('.prop-btn', 'High'));
  await b.until(`!!document.querySelector('.menu-pop')`);
  await b.evaluate(clickText('.menu-pop button', 'Low'));
  await b.wait(2000);
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'launch');
  if (t.name !== 'Draft the launch announcement' || t.priority !== 'low') throw new Error(JSON.stringify([t.name, t.priority]));
});

await step('Task page: Memory tab lists and teaches memory', async () => {
  await b.evaluate(`location.hash = '#/task/pricing?tab=memory'; true`);
  await b.until(`!!document.querySelector('[data-key="remember"]')`);
  await b.evaluate(typeInto('[data-key="remember"]', 'Prices always include VAT'));
  await b.evaluate(clickText('.btn', 'Remember'));
  await b.until(`document.body.textContent.includes('Prices always include VAT')`);
});

await step('Board drag and drop moves a task to Paused', async () => {
  await b.evaluate(`location.hash = '#/tasks?view=board'; true`);
  await b.until(`!!document.querySelector('.card[data-slug="pricing"]')`);
  await b.evaluate(`(() => {
    const card = document.querySelector('.card[data-slug="pricing"]');
    const lane = document.querySelector('.lane[data-status="paused"]');
    const dt = new DataTransfer();
    card.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer: dt }));
    lane.dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, dataTransfer: dt }));
    lane.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }));
    return true; })()`);
  await b.wait(2500);
  const t = (await api('/api/tasks')).tasks.find(x => x.slug === 'pricing');
  if (t.status !== 'paused') throw new Error(t.status);
});

await step('Command palette finds a task and opens it', async () => {
  await b.evaluate(key(null, 'k', { metaKey: true }));
  await b.until(`!!document.querySelector('.palette input')`);
  await b.evaluate(typeInto('.palette input', 'pricing'));
  await b.wait(300);
  await b.evaluate(key('.palette input', 'ArrowDown'));
  await b.evaluate(`(() => { const it = [...document.querySelectorAll('.palette-item')].find(n => n.textContent.includes('Fix pricing table')); it.click(); return true; })()`);
  await b.until(`location.hash === '#/task/pricing'`);
});

await step('Settings saves quiet hours', async () => {
  await b.evaluate(`location.hash = '#/settings'; true`);
  await b.until(`document.querySelectorAll('input[type=time]').length === 2`);
  await b.evaluate(`(() => { const [f, t] = document.querySelectorAll('input[type=time]'); f.value = '23:00'; t.value = '07:30'; t.dispatchEvent(new Event('change')); return true; })()`);
  await b.wait(1500);
  const s = (await api('/api/settings')).settings;
  if (s.quiet_hours !== '23:00-07:30') throw new Error(s.quiet_hours);
});

await step('The quick-capture window adds a task with shorthand', async () => {
  await b.goto(`${BASE}/?q=1#token=${TOKEN}&route=%2Fquick`);
  await b.until(`!!document.querySelector('[data-key="quick"]')`);
  await b.evaluate(typeInto('[data-key="quick"]', 'Check the invoice totals #ops'));
  await b.evaluate(key('[data-key="quick"]', 'Enter'));
  await b.until(`document.body.textContent.includes('Added')`, 8000);
  const t = (await api('/api/tasks')).tasks.find(x => x.name === 'Check the invoice totals');
  if (!t || t.project !== 'ops' || t.status !== 'next') throw new Error(JSON.stringify(t));
  await b.goto(`${BASE}/?q=2#token=${TOKEN}`);
});

await step('Every page renders without errors', async () => {
  for (const r of ['/home', '/inbox', '/inbox/keys', '/tasks?view=list', '/projects', '/project/website?tab=assistant', '/project/website?tab=brief', '/memory', '/archive', '/settings', '/about', '/about/vision', '/help/troubleshooting', '/task/nope']) {
    await b.evaluate(`location.hash = '#${r}'; true`);
    await b.wait(700);
    const txt = await b.evaluate(`document.querySelector('main').textContent.length`);
    if (txt < 20) throw new Error(`${r} is empty`);
  }
});

if (shots) await b.shot(`${shots}/e2e-last.png`);
const errors = [...new Set(b.errors)].filter(e => !e.includes('/api/tasks/nope')); // that one is the deliberate missing-task check
b.close();
return { results, errors };
}
