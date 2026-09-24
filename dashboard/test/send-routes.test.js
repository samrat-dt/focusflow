import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { makeApp } from './harness.js';
import { REPLY_CUE, HEADLESS_CUE } from '../lib/send.js';
import { readEvents } from '../lib/event-log.js';

let h;
beforeEach(() => { h = null; });
afterEach(() => { h?.close(); });

const send = (slug, body, o) => h.post(`/api/tasks/${slug}/message`, body, o);
const messageCalls = () => h.calls.filter(c => c.args[0] === 'message');
const sentEvents = () => h.store.dump().messages.filter(m => m.direction === 'out');

test('queue sends `focusflow message user/<slug>` with the reply cue and records the focusflow id', async () => {
  h = await makeApp();
  const r = await send('going', { text: 'Please rebase', mode: 'queue' });
  assert.equal(r.status, 200);
  assert.deepEqual(r.json(), { ok: true, mode: 'queue', flow_id: 'ab12cd34' });
  assert.deepEqual(messageCalls().map(c => c.args), [['message', 'user/going', `Please rebase${REPLY_CUE}`]]);
  assert.equal(messageCalls()[0].opts.env.CLAUDE_CODE_SESSION_ID, undefined);
  const rows = sentEvents();
  assert.equal(rows.length, 1);
  assert.deepEqual([rows[0].flow_id, rows[0].slug, rows[0].body], ['ab12cd34', 'going', 'Please rebase']);
});

test('the thread shows what I typed, never the cue, and awaits a reply', async () => {
  h = await makeApp();
  await send('going', { text: 'Please rebase', mode: 'queue' });
  const t = (await h.get('/api/tasks/going/thread')).json();
  assert.deepEqual(t.messages.map(m => [m.direction, m.body]), [['out', 'Please rebase']]);
  assert.equal(t.awaiting_reply, true);
});

test('queue works for a live task, a backlog task and an in-progress task with an old session', async () => {
  h = await makeApp();
  for (const slug of ['running', 'todo', 'going']) assert.equal((await send(slug, { text: 'hi', mode: 'queue' })).status, 200, slug);
  assert.equal(messageCalls().length, 3);
});

test('a focusflow error is relayed unchanged (502), nothing is logged, nothing is retried', async () => {
  h = await makeApp({ focusflow: async () => { throw new Error('error: task "going" is done'); } });
  const r = await send('going', { text: 'hi', mode: 'queue' });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, 'error: task "going" is done');
  assert.equal(sentEvents().length, 0);
  assert.equal(messageCalls().length, 1);
});

test('if focusflow prints no id the message is still recorded, under a synthetic key', async () => {
  h = await makeApp({ focusflow: async () => ({ stdout: 'queued\n', stderr: '' }) });
  await send('going', { text: 'hi', mode: 'queue' });
  const rows = sentEvents();
  assert.equal(rows.length, 1);
  assert.match(rows[0].flow_id, /^out:/);
});

test('validation: text rules, mode, and closed tasks, all before focusflow is called', async () => {
  h = await makeApp();
  const bad = [
    [{ text: '', mode: 'queue' }, 400], [{ mode: 'queue' }, 400], [{ text: 5, mode: 'queue' }, 400],
    [{ text: 'x'.repeat(381), mode: 'queue' }, 400], [{ text: '--help', mode: 'queue' }, 400],
    [{ text: 'hi' }, 400], [{ text: 'hi', mode: 'carrier-pigeon' }, 400],
  ];
  for (const [body, status] of bad) assert.equal((await send('going', body)).status, status, JSON.stringify(body));
  assert.equal((await send('finished', { text: 'hi', mode: 'queue' })).status, 409);
  assert.equal((await send('shelved', { text: 'hi', mode: 'queue' })).status, 409);
  assert.equal(h.calls.filter(c => c.args[0] !== 'inbox').length, 0);
});

test('unknown and malformed slugs, bad token, wrong method, non-JSON body', async () => {
  h = await makeApp();
  assert.equal((await send('nope', { text: 'hi', mode: 'queue' })).status, 404);
  assert.equal((await send('..%2Fx', { text: 'hi', mode: 'queue' })).status, 400);
  assert.equal((await send('going', { text: 'hi', mode: 'queue' }, { token: 'wrong' })).status, 401);
  assert.equal((await h.get('/api/tasks/going/message')).status, 405);
  assert.equal((await send('going', undefined, { raw: 'text=hi', type: 'application/x-www-form-urlencoded' })).status, 415);
  assert.equal(messageCalls().length, 0);
});

test('two simultaneous sends run one at a time', async () => {
  let active = 0, peak = 0;
  h = await makeApp({ focusflow: async () => { active += 1; peak = Math.max(peak, active); await new Promise(r => setTimeout(r, 20)); active -= 1; return { stdout: '[ab12cd34]', stderr: '' }; } });
  await Promise.all([send('going', { text: 'one', mode: 'queue' }), send('todo', { text: 'two', mode: 'queue' })]);
  assert.equal(peak, 1);
});

// ---------------- headless ----------------

const doCalls = () => h.calls.filter(c => c.args[0] === 'do');
const sentLog = () => readEvents(h.config.dataDir).events.filter(e => e.kind === 'message.sent');

test('headless without confirm is refused and nothing runs', async () => {
  h = await makeApp();
  for (const confirm of [undefined, false, 'true', 1]) {
    const r = await send('going', { text: 'do it', mode: 'headless', ...(confirm === undefined ? {} : { confirm }) });
    assert.equal(r.status, 400, String(confirm));
    assert.match(r.json().error, /confirm/);
  }
  assert.equal(doCalls().length, 0);
  assert.equal(messageCalls().length, 0);
});

test('headless with confirm runs `focusflow do --auto` with the text and headless cue, and only that', async () => {
  h = await makeApp();
  const r = await send('todo', { text: 'Run the suite', mode: 'headless', confirm: true });
  assert.equal(r.status, 200);
  assert.deepEqual(r.json(), { ok: true, mode: 'headless' });
  assert.deepEqual(doCalls().map(c => c.args), [['do', 'todo', '--auto', '--with', `Run the suite${HEADLESS_CUE}`]]);
  assert.equal(doCalls()[0].opts.env.CLAUDE_CODE_SESSION_ID, undefined);
  assert.equal(messageCalls().length, 0, 'the text is never delivered a second way');
});

test('a headless send is recorded (mode, ok) and shows in the thread under a synthetic key', async () => {
  h = await makeApp();
  await send('todo', { text: 'Run the suite', mode: 'headless', confirm: true });
  const ev = sentLog();
  assert.equal(ev.length, 1);
  assert.deepEqual(ev[0].payload, { slug: 'todo', text: 'Run the suite', mode: 'headless', via: 'auto', ok: true });
  const t = (await h.get('/api/tasks/todo/thread')).json();
  assert.deepEqual(t.messages.map(m => [m.direction, m.body]), [['out', 'Run the suite']]);
  assert.match(t.messages[0].id, /^out:/);
  assert.equal(t.awaiting_reply, true);
});

test('headless is refused for done and archived tasks', async () => {
  h = await makeApp();
  assert.equal((await send('finished', { text: 'x', mode: 'headless', confirm: true })).status, 409);
  assert.equal((await send('shelved', { text: 'x', mode: 'headless', confirm: true })).status, 409);
  assert.equal(doCalls().length, 0);
});

test('a live task is downgraded to a queue send, and the response says so', async () => {
  h = await makeApp();
  const r = await send('running', { text: 'ping', mode: 'headless', confirm: true });
  assert.equal(r.status, 200);
  const j = r.json();
  assert.equal(j.mode, 'queue');
  assert.equal(j.downgraded, true);
  assert.match(j.note, /live/);
  assert.equal(doCalls().length, 0);
  assert.deepEqual(messageCalls().map(c => c.args), [['message', 'user/running', `ping${REPLY_CUE}`]]);
  assert.equal(sentLog()[0].payload.mode, 'queue');
});

test('a task that went live after the page loaded is caught by the re-check', async () => {
  h = await makeApp({ live: [] });
  h.live.add('going'); // becomes live before the send; refresh() inside the send picks it up
  const j = (await send('going', { text: 'ping', mode: 'headless', confirm: true })).json();
  assert.equal(j.downgraded, true);
});

test('a failed launch is relayed unchanged (502), logged with ok:false, and not shown in the thread', async () => {
  h = await makeApp({ focusflow: async () => { throw new Error('focusflow: cannot start headless run'); } });
  const r = await send('todo', { text: 'do it', mode: 'headless', confirm: true });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, 'focusflow: cannot start headless run');
  assert.deepEqual(sentLog()[0].payload, { slug: 'todo', text: 'do it', mode: 'headless', via: 'auto', ok: false, error: 'focusflow: cannot start headless run' });
  assert.deepEqual((await h.get('/api/tasks/todo/thread')).json().messages, []);
});

// Found in the live acceptance run: `focusflow do --auto` launches `claude --session-id <id>`, which
// Claude rejects ("Session ID ... is already in use") when the task already has a session. So a
// stopped task with a session continues via `claude --resume` directly instead (claude harness only
// — focusflow's CLI has no equivalent for any harness).
test('a stopped task with a claude session continues headlessly via claude --resume, not focusflow do --auto', async () => {
  h = await makeApp({ sessions: { going: { sessionId: 'sess-going', harness: 'claude' } } });
  const r = await send('going', { text: 'Run the suite', mode: 'headless', confirm: true });
  assert.equal(r.status, 200);
  assert.deepEqual(r.json(), { ok: true, mode: 'headless' });
  assert.equal(h.resumeCalls.length, 1);
  assert.equal(h.resumeCalls[0].sessionId, 'sess-going');
  assert.equal(h.resumeCalls[0].text, `Run the suite${HEADLESS_CUE}`);
  assert.equal(h.resumeCalls[0].cwd, '/work/x');
  assert.equal(doCalls().length, 0, 'focusflow do --auto must not run for a continuation');
});

test('the continuation is recorded with via: resume; a first-ever run is recorded with via: auto', async () => {
  h = await makeApp({ sessions: { going: { sessionId: 'sess-going', harness: 'claude' } } });
  await send('going', { text: 'x', mode: 'headless', confirm: true });
  await send('todo', { text: 'y', mode: 'headless', confirm: true });
  const events = sentLog();
  assert.equal(events.find(e => e.payload.slug === 'going').payload.via, 'resume');
  assert.equal(events.find(e => e.payload.slug === 'todo').payload.via, 'auto');
});

test('a stopped task whose session is not claude is still refused, with a harness-specific message', async () => {
  h = await makeApp();
  const r = await send('coding', { text: 'x', mode: 'headless', confirm: true });
  assert.equal(r.status, 409);
  assert.match(r.json().error, /codex/);
  assert.match(r.json().error, /Queue/);
  assert.equal(h.resumeCalls.length, 0);
  assert.equal(doCalls().length, 0);
});

test('a stopped task with no recorded harness is refused the same way', async () => {
  h = await makeApp();
  const r = await send('unknown-harness', { text: 'x', mode: 'headless', confirm: true });
  assert.equal(r.status, 409);
  assert.equal(h.resumeCalls.length, 0);
});

test('a resume launch failure is relayed (502) and logged with ok:false, via:resume', async () => {
  h = await makeApp({
    sessions: { going: { sessionId: 'sess-going', harness: 'claude' } },
    resume: async () => { throw new Error('cannot run claude: not found'); },
  });
  const r = await send('going', { text: 'x', mode: 'headless', confirm: true });
  assert.equal(r.status, 502);
  assert.equal(r.json().error, 'cannot run claude: not found');
  const ev = sentLog();
  assert.equal(ev[0].payload.ok, false);
  assert.equal(ev[0].payload.via, 'resume');
});

test('has_session but no session row found (readSession returns null) is refused, not crashed', async () => {
  h = await makeApp(); // no `sessions` entry for `going`
  const r = await send('going', { text: 'x', mode: 'headless', confirm: true });
  assert.equal(r.status, 409);
  assert.equal(h.resumeCalls.length, 0);
});

test('refresh() runs before the response for a continuation, so has_session/live are current immediately', async () => {
  h = await makeApp({
    live: [],
    sessions: { going: { sessionId: 'sess-going', harness: 'claude' } },
    resume: async () => { h.live.add('going'); return { pid: 1 }; }, // simulate focusflow noticing the new process
  });
  await send('going', { text: 'x', mode: 'headless', confirm: true });
  const t = (await h.get('/api/tasks/going/thread')).json();
  assert.equal(t.live, true, 'the thread route reflects live immediately, not on the next tick');
});

test('refresh() runs before the response for a first-ever headless run too', async () => {
  h = await makeApp({
    live: [],
    focusflow: async args => { if (args[0] === 'do') h.live.add('todo'); return { stdout: '', stderr: '' }; },
  });
  await send('todo', { text: 'x', mode: 'headless', confirm: true });
  const t = (await h.get('/api/tasks/todo/thread')).json();
  assert.equal(t.live, true);
});

test('a live task that has a session still downgrades to queue (the refusal only applies when it is not live)', async () => {
  h = await makeApp();
  const j = (await send('running', { text: 'ping', mode: 'headless', confirm: true })).json();
  assert.equal(j.mode, 'queue');
  assert.equal(j.downgraded, true);
});

test('the confirmation is still asked for first: no confirm gives the 400, not the session refusal', async () => {
  h = await makeApp();
  assert.equal((await send('going', { text: 'x', mode: 'headless' })).status, 400);
});

test('headless text is validated like queue text', async () => {
  h = await makeApp();
  assert.equal((await send('going', { text: '--help', mode: 'headless', confirm: true })).status, 400);
  assert.equal((await send('going', { text: 'x'.repeat(381), mode: 'headless', confirm: true })).status, 400);
  assert.equal(doCalls().length, 0);
});

test('the headless start puts the task ref BEFORE its flags (focusflow do <ref> --auto ...)', async () => {
  h = await makeApp({ live: [], tasks: undefined });
  await send('todo', { text: 'go', mode: 'headless', confirm: true });
  const a = doCalls()[0].args;
  assert.equal(a[0], 'do');
  assert.ok(!a[1].startsWith('-'), `the ref must come first, got ${JSON.stringify(a)}`);
  assert.deepEqual(a.slice(1, 3), ['todo', '--auto']);
});
