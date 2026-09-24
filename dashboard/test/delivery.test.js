import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { makeApp, focusRow } from './harness.js';
import { at } from './helpers.js';

let h;
beforeEach(() => { h = null; });
afterEach(() => { h?.close(); });

const inboxTask = (slug, rows) => async args => {
  if (args[0] === 'inbox' && args[1] === '--task') return { stdout: JSON.stringify(rows[args[2]] ?? []), stderr: '' };
  if (args[0] === 'message') return { stdout: '[ab12cd34]\n', stderr: '' };
  return { stdout: '', stderr: '' };
};

test('a queued message is "sent" until focusflow reports it delivered, then "delivered"', async () => {
  const rows = {};
  h = await makeApp({ focusflow: inboxTask('going', rows) });
  await h.post('/api/tasks/going/message', { text: 'Please rebase', mode: 'queue' });

  let t = (await h.get('/api/tasks/going/thread')).json();
  assert.equal(t.delivery, 'sent');
  assert.equal(t.messages[0].delivered_at, null);

  // focusflow has not delivered it yet: still sent.
  await h.app.pollInbox();
  t = (await h.get('/api/tasks/going/thread')).json();
  assert.equal(t.delivery, 'sent');

  rows.going = [{ id: 'ab12cd34', delivered_at: '2026-09-22T06:31:00Z' }];
  await h.app.pollInbox();
  t = (await h.get('/api/tasks/going/thread')).json();
  assert.equal(t.delivery, 'delivered');
  assert.equal(t.messages[0].delivered_at, '2026-09-22T06:31:00.000Z');

  // Receipts are events, so a rebuild reproduces them.
  const before = h.store.hash();
  assert.ok(before);
  const receipts = h.store.dump().messages.filter(m => m.delivered_at);
  assert.equal(receipts.length, 1);
});

test('delivery polling only asks about undelivered queued messages and tolerates failures', async () => {
  h = await makeApp({ focusflow: async args => {
    if (args[0] === 'inbox' && args[1] === '--task') throw new Error('focusflow busy');
    if (args[0] === 'message') return { stdout: '[ab12cd34]\n', stderr: '' };
    return { stdout: '', stderr: '' };
  } });
  await h.post('/api/tasks/going/message', { text: 'hello', mode: 'queue' });
  const r = await h.app.pollInbox();
  assert.equal(r.error, null, 'a failing receipt lookup must not fail the poll');
  assert.equal((await h.get('/api/tasks/going/thread')).json().delivery, 'sent');

  // A reply ends "awaiting", so delivery no longer applies.
  h.inboxRows.rows.push(focusRow('going', 'done!', [12, 5]));
  await h.app.pollInbox();
  assert.equal((await h.get('/api/tasks/going/thread')).json().delivery, null);
});

test('headless runs have no bus message, hence no receipt to poll', async () => {
  h = await makeApp({ live: [], sessions: { going: { sessionId: 's', harness: 'claude' } } });
  await h.post('/api/tasks/going/message', { text: 'go', mode: 'headless', confirm: true });
  await h.app.pollInbox();
  assert.equal(h.calls.filter(c => c.args[1] === '--task').length, 0);
});
