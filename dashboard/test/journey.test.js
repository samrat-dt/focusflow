import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { makeApp } from './harness.js';
import { state } from './helpers.js';

let h;
afterEach(() => { h?.close(); });

const T = (slug, status, extra = {}) => state({ slug, name: slug.toUpperCase(), status, has_session: true, harness: 'claude', project: 'resume', ...extra });

// A stateful stand-in for focusflow: commands change the task list the way the real CLI would.
function fakeFocus(tasks, log, detachedLog) {
  const find = slug => tasks.find(t => t.slug === slug);
  return async args => {
    log.push(args);
    const [cmd] = args;
    if (cmd === 'project' && args[1] === 'agent') {
      if (!find('resume-agent')) tasks.push(T('resume-agent', 'backlog', { kind: 'project_agent', has_session: false }));
      return { stdout: 'resume-agent\n', stderr: '' };
    }
    if (cmd === 'add' && args[1] === 'task') {
      const slug = args[args.indexOf('--slug') + 1];
      tasks.push(T(slug, 'backlog', { has_session: false, name: args[2] }));
      return { stdout: `Created task "${slug}"\n`, stderr: '' };
    }
    if (cmd === 'next') find(args[1]).status = 'next';
    if (cmd === 'review') find(args[1]).status = 'review';
    if (cmd === 'pause') find(args[1]).status = 'paused';
    if (cmd === 'update' && args[1] === 'task') find(args[2]).status = args[args.indexOf('--status') + 1];
    if (cmd === 'message') return { stdout: '[ab12cd34]\n', stderr: '' };
    return { stdout: 'ok\n', stderr: '' };
  };
}

test('the morning walk-through, end to end, without ever opening a terminal by itself', async () => {
  const tasks = [
    T('overnight', 'review'), T('busy', 'in-progress'), T('queued', 'next'), T('idea', 'backlog', { has_session: false }),
    T('old', 'paused'),
  ];
  const calls = []; const detached = [];
  const projects = [{ slug: 'resume', name: 'Resume', priority: 'high', work_dir: '/tmp', archived: false, agent_task_slug: null }];
  h = await makeApp({
    tasks, live: ['busy'], projectsFull: projects, detachedCalls: detached,
    focusflow: fakeFocus(tasks, calls, detached),
    sessions: { 'resume-agent': { sessionId: 'sess-coord', harness: 'claude' } },
  });
  const lane = async status => (await h.get('/api/tasks')).json().tasks.filter(t => t.status === status && t.kind === 'regular').map(t => t.slug).sort();
  const act = (slug, action) => h.post(`/api/tasks/${slug}/action`, { action });

  // 1. Board: the review lane holds what finished overnight; the terminal marker is independent of the stage.
  assert.deepEqual(await lane('review'), ['overnight']);
  const busy = (await h.get('/api/tasks')).json().tasks.find(t => t.slug === 'busy');
  assert.deepEqual([busy.status, busy.terminal_open], ['in-progress', true]);
  assert.equal((await act('overnight', 'approve')).status, 200);
  assert.deepEqual(detached.map(d => d.args), [['done', 'overnight']], 'approve closes out through `focusflow done`');

  // 2. A project-level message reaches the coordinator; fan-out is opt-in.
  const m = await h.post('/api/projects/resume/message', { text: 'make B do the thing', mode: 'queue', fanout: true });
  assert.equal(m.status, 200);
  assert.deepEqual([m.json().agent, m.json().fanned_out], ['resume-agent', true]);

  // 3. A draft subtask lands in the backlog and starts nothing.
  const before = calls.length;
  await act('idea', 'next');           // promote from the backlog to the literal queue
  assert.deepEqual(await lane('next'), ['idea', 'queued']);
  assert.ok(calls.length > before);

  // 4. End of day: pause what is not worth leaving running; send finished work to review.
  await act('busy', 'pause');
  await act('queued', 'unpause');
  await act('queued', 'review');
  assert.deepEqual(await lane('paused'), ['busy', 'old']);
  // (`done` is detached in the real app and flips the status itself; the stand-in only records it.)
  assert.ok((await lane('review')).includes('queued'));

  // 5. Overview and archive tell the same story from the project level.
  projects[0].agent_task_slug = 'resume-agent'; // what `focusflow project agent` records in the real database
  const o = (await h.get('/api/projects/overview')).json();
  assert.equal(o.projects[0].slug, 'resume');
  assert.ok(o.projects[0].agent, 'the coordinator exists once the project was messaged');
  assert.equal((await act('idea', 'archive')).status, 200);

  // The whole walk-through never asked for a terminal.
  assert.ok(!calls.some(a => a[0] === 'do'), 'no step of the journey may open a terminal');
});
