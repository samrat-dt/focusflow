import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bannerFor, appleScriptQuote, osascriptArgs, createNotifier } from '../lib/notifier.js';
import { makeApp, focusRow } from './harness.js';

const arrival = (slug, body, o = {}) => ({ slug, name: `Name of ${slug}`, body, urgent: false, ...o });

test('one arrival: title is the task name, body the first 120 characters on one line', () => {
  const long = `first line\n\n  second   line ${'x'.repeat(200)}`;
  const b = bannerFor([arrival('a', long)]);
  assert.equal(b.title, 'Name of a');
  assert.equal(b.subtitle, null);
  assert.ok(b.body.startsWith('first line second line xxx'));
  assert.equal(b.body.length, 120);
  assert.ok(!b.body.includes('\n'));
});

test('an urgent message puts "urgent" in the subtitle', () => {
  assert.equal(bannerFor([arrival('a', 'help', { urgent: true })]).subtitle, 'urgent');
});

test('several arrivals collapse into one banner', () => {
  const b = bannerFor([arrival('a', 'one'), arrival('a', 'two'), arrival('b', 'three')]);
  assert.deepEqual(b, { title: 'Focusflow', subtitle: null, body: '3 new messages in 2 tasks' });
  assert.equal(bannerFor([arrival('a', 'one'), arrival('a', 'two')]).body, '2 new messages in 1 task');
  assert.equal(bannerFor([arrival('a', 'one'), arrival('b', 'x', { urgent: true })]).subtitle, 'urgent');
});

test('AppleScript quoting escapes backslashes and quotes and flattens control characters', () => {
  assert.equal(appleScriptQuote('plain'), '"plain"');
  assert.equal(appleScriptQuote('say "hi"'), '"say \\"hi\\""');
  assert.equal(appleScriptQuote('back\\slash'), '"back\\\\slash"');
  assert.equal(appleScriptQuote('a\nb\tc\u0000d'), '"a b c d"');
});

test('a hostile message body cannot break out of the string literal', () => {
  const evil = '" & (do shell script "touch /tmp/pwned") & "';
  const q = appleScriptQuote(evil);
  assert.equal(q, '"\\" & (do shell script \\"touch /tmp/pwned\\") & \\""');
  // every quote inside is escaped: removing the escaped ones leaves only the two outer quotes
  assert.equal(q.replace(/\\"/g, '').split('"').length - 1, 2);
  const evil2 = 'x\\" & (do shell script "id") & \\"';
  const q2 = appleScriptQuote(evil2);
  assert.equal(q2.replace(/\\\\/g, '').replace(/\\"/g, '').split('"').length - 1, 2);
});

test('osascript gets an argv array with one -e script', () => {
  assert.deepEqual(osascriptArgs({ title: 'T', subtitle: null, body: 'B' }), ['-e', 'display notification "B" with title "T"']);
  assert.deepEqual(osascriptArgs({ title: 'T', subtitle: 'urgent', body: 'B' }), ['-e', 'display notification "B" with title "T" subtitle "urgent"']);
});

test('the notifier runs osascript once per poll, however many arrivals', async () => {
  const runs = [];
  const n = createNotifier({ run: async argv => { runs.push(argv); } });
  await n.notify([arrival('a', 'one'), arrival('b', 'two')]);
  assert.equal(runs.length, 1);
  assert.equal(runs[0][0], '-e');
  assert.match(runs[0][1], /2 new messages in 2 tasks/);
});

test('disabled by the tunable: nothing runs', async () => {
  const runs = [];
  const n = createNotifier({ enabled: false, run: async argv => { runs.push(argv); } });
  await n.notify([arrival('a', 'one')]);
  assert.equal(runs.length, 0);
});

test('an empty arrival list runs nothing', async () => {
  const runs = [];
  await createNotifier({ run: async argv => { runs.push(argv); } }).notify([]);
  assert.equal(runs.length, 0);
});

test('a failing banner is remembered for the footer and never throws; the next success clears it', async () => {
  let fail = true;
  const n = createNotifier({ run: async () => { if (fail) throw new Error('osascript: not allowed to send notifications'); } });
  await n.notify([arrival('a', 'one')]);
  assert.equal(n.status().last_error, 'osascript: not allowed to send notifications');
  fail = false;
  await n.notify([arrival('a', 'two')]);
  assert.equal(n.status().last_error, null);
});

// --- through the whole app ---

test('the app notifies once per new unread arrival and its health shows a failing banner', async () => {
  const seen = [];
  const notifier = createNotifier({ run: async argv => { seen.push(argv[1]); throw new Error('banner blocked'); } });
  const h = await makeApp({ notifier });
  try {
    await h.app.pollInbox();
    h.inboxRows.rows = [focusRow('going', 'ping', [10, 0])];
    await h.app.pollInbox();
    await h.app.pollInbox();
    assert.equal(seen.length, 1);
    assert.match(seen[0], /"Going"/);
    assert.match(seen[0], /"ping"/);
    assert.equal((await h.get('/api/health')).json().notify_error, 'banner blocked');
  } finally { h.close(); }
});

test('the harness default (notifications off in the tunables) builds a silent notifier: no banner, no error', async () => {
  const h = await makeApp();
  try {
    await h.app.pollInbox();
    h.inboxRows.rows = [focusRow('going', 'ping', [10, 0])];
    await h.app.pollInbox();
    assert.equal((await h.get('/api/health')).json().notify_error, null);
  } finally { h.close(); }
});
