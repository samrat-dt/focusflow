import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { makeApp, focusRow } from './harness.js';
import { state, tmpdir } from './helpers.js';
import { gitSince, latestSummary } from '../lib/proof.js';

let h;
afterEach(() => { h?.close(); h = null; });
const T = (slug, status, extra = {}) => state({ slug, name: slug.toUpperCase(), status, has_session: true, harness: 'claude', ...extra });
const fakeFocus = rules => async args => {
  if (args[0] === 'remember') return { stdout: JSON.stringify({ id: 'r1', kind: 'rule', scope: args.includes('--global') ? 'global' : 'project', project: args[args.indexOf('--project') + 1], body: args[1] }), stderr: '' };
  if (args[0] === 'memories') return { stdout: JSON.stringify(rules ?? []), stderr: '' };
  return { stdout: '', stderr: '' };
};

test('request changes saves a house rule, then sends the feedback so work resumes in the background', async () => {
  h = await makeApp({ live: [], tasks: [T('rev', 'review', { project: 'web' })], focusflow: fakeFocus() });
  const r = await h.post('/api/tasks/rev/request-changes', { feedback: 'The table must work on mobile', rule: 'Every page must work on mobile' });
  assert.equal(r.status, 200);
  assert.equal(r.json().rule.kind, 'rule');
  const calls = h.calls.map(c => c.args).filter(a => a[0] !== 'inbox');
  assert.deepEqual(calls[0], ['remember', 'Every page must work on mobile', '--kind', 'rule', '--project', 'web', '--json']);
  const run = calls.find(a => a[0] === 'do');
  assert.equal(run[1], 'rev');
  assert.match(run[4], /^Changes requested: The table must work on mobile/);
});

test('a live task gets the feedback in its terminal and moves back to in progress; the rule can apply everywhere', async () => {
  h = await makeApp({ live: ['rev'], tasks: [T('rev', 'review', { project: 'web' })], focusflow: fakeFocus() });
  const r = await h.post('/api/tasks/rev/request-changes', { feedback: 'Use the brand font', rule: 'Always use the brand font', rule_scope: 'global' });
  assert.equal(r.status, 200);
  assert.equal(r.json().mode, 'queue');
  const calls = h.calls.map(c => c.args);
  assert.ok(calls.some(a => a[0] === 'remember' && a.includes('--global')));
  assert.ok(calls.some(a => a[0] === 'message' && a[2].startsWith('Changes requested: Use the brand font')));
  assert.ok(calls.some(a => a.join(' ') === 'update task rev --status in-progress'));
});

test('request changes validates and refuses the wrong stage', async () => {
  h = await makeApp({ live: [], tasks: [T('rev', 'review'), T('idea', 'backlog')], focusflow: fakeFocus() });
  assert.equal((await h.post('/api/tasks/idea/request-changes', { feedback: 'x' })).status, 409);
  assert.equal((await h.post('/api/tasks/rev/request-changes', { feedback: '' })).status, 400);
  assert.equal((await h.post('/api/tasks/rev/request-changes', { feedback: 'ok', rule: '-rf' })).status, 400);
  assert.equal((await h.post('/api/tasks/rev/request-changes', { feedback: 'x'.repeat(360) })).status, 400);
  assert.ok(!h.calls.some(c => ['remember', 'do', 'message'].includes(c.args[0])), 'nothing ran');
});

test('proof of work: the agent’s last message, the latest note, and the project’s rules', async () => {
  const rules = [{ kind: 'rule', scope: 'project', project: 'web', body: 'Tests for every change' }, { kind: 'rule', scope: 'project', project: 'other', body: 'nope' }, { kind: 'rule', scope: 'global', body: 'Be brief' }, { kind: 'decision', scope: 'project', project: 'web', body: 'x' }];
  h = await makeApp({ live: [], tasks: [T('rev', 'review', { project: 'web' })], focusflow: fakeFocus(rules), workDir: null });
  h.inboxRows.rows = [focusRow('rev', 'Done: pricing table is responsive (via focusflow: reply with focusflow message user "<answer>")')];
  await h.app.pollInbox();
  const dir = path.join(h.config.focusRoot, 'tasks', 'rev', 'updates');
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, '2026-09-25-done.md'), 'Made it responsive.');
  const p = (await h.get('/api/tasks/rev/proof')).json();
  assert.equal(p.summary.text, 'Done: pricing table is responsive');
  assert.equal(p.note.text, 'Made it responsive.');
  assert.deepEqual(p.rules.map(r => r.body), ['Tests for every change', 'Be brief']);
  assert.equal(p.git, null, 'no folder, no git section');
});

test('gitSince lists commits and changed files in a real repository, and nothing outside one', async () => {
  const repo = tmpdir('ff-git-');
  const git = (...a) => execFileSync('git', a, { cwd: repo, env: { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@x', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@x' } });
  git('init', '-q');
  fs.writeFileSync(path.join(repo, 'a.txt'), '1'); git('add', '.'); git('commit', '-qm', 'first');
  fs.writeFileSync(path.join(repo, 'b.txt'), '2'); git('add', '.'); git('commit', '-qm', 'second change');
  fs.writeFileSync(path.join(repo, 'c.txt'), 'dirty');
  const g = await gitSince({ workDir: repo, since: new Date(Date.now() - 3600000).toISOString() });
  assert.deepEqual(g.commits.map(c => c.subject), ['second change', 'first']);
  assert.ok(g.files.includes('b.txt') && g.files.includes('c.txt'));
  assert.equal(g.uncommitted, 1);
  assert.equal(await gitSince({ workDir: tmpdir('ff-nogit-'), since: null }), null);
  assert.deepEqual(latestSummary([{ direction: 'out', body: 'go' }]), null);
});

test('reply picks the route itself: background when nothing is open, the terminal when it is', async () => {
  h = await makeApp({ live: ['open'], tasks: [T('quiet', 'in-progress'), T('open', 'in-progress')], focusflow: fakeFocus() });
  assert.equal((await h.post('/api/tasks/quiet/reply', { text: 'Go ahead' })).json().mode, 'headless');
  assert.equal((await h.post('/api/tasks/open/reply', { text: 'Go ahead' })).json().mode, 'queue');
  assert.equal((await h.post('/api/tasks/open/reply', { text: '' })).status, 400);
});

test('while the Mac app is polling, the server does not send its own banners', async () => {
  const sent = [];
  const list = [T('bg', 'in-progress', { auto_run_status: 'running' })];
  h = await makeApp({ live: [], tasks: list, notifier: { notify: a => sent.push(a), status: () => ({}) } });
  const n = await h.get('/api/needs-you', { });
  assert.equal(typeof n.json().notify.enabled, 'boolean');
  // the app announces itself
  await new Promise((resolve, reject) => {
    import('node:http').then(({ default: http }) => {
      const r = http.request({ host: '127.0.0.1', port: h.port, path: '/api/needs-you', headers: { host: `127.0.0.1:${h.port}`, 'x-focusflow-token': 'c'.repeat(64), 'x-focusflow-client': 'mac-app' } }, res => { res.resume(); res.on('end', resolve); });
      r.on('error', reject); r.end();
    });
  });
  list[0].auto_run_status = 'finished';
  h.app.refresh();
  assert.equal(sent.length, 0, 'the app shows it natively instead');
});
