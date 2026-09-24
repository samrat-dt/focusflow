import { test } from 'node:test';
import assert from 'node:assert/strict';
import { diffStates, STATE_FIELDS } from '../lib/task-state.js';
import { state } from './helpers.js';

test('STATE_FIELDS is exactly the spec section 5.4 list', () => {
  assert.deepEqual(STATE_FIELDS, [
    'slug', 'name', 'project', 'kind', 'status', 'priority', 'tags', 'due_date', 'waiting_on',
    'assignee', 'harness', 'has_session', 'session_started', 'session_last_resumed',
    'status_changed_at', 'created_at', 'updated_at', 'archived', 'auto_run_status',
    'notes_count', 'brief_sha256',
  ]);
  assert.deepEqual(Object.keys(state()), STATE_FIELDS);
});

test('identical states produce no changes', () => {
  assert.deepEqual(diffStates(state(), state()), {});
});

const cases = {
  name: 'Renamed',
  project: 'proj',
  kind: 'playbook_run',
  status: 'in-progress',
  priority: 'high',
  tags: ['ui'],
  due_date: '2026-10-01',
  waiting_on: 'design review',
  assignee: 'sam',
  harness: 'claude',
  has_session: true,
  session_started: '2026-09-02T09:00:00+05:30',
  session_last_resumed: '2026-09-03T09:00:00+05:30',
  status_changed_at: '2026-09-02T09:00:00+05:30',
  created_at: '2026-08-01T09:00:00+05:30',
  updated_at: '2026-09-05T09:00:00+05:30',
  archived: true,
  auto_run_status: 'running',
  notes_count: 3,
  brief_sha256: 'abc123',
};

for (const [field, value] of Object.entries(cases)) {
  test(`change in ${field} is reported as [old, new]`, () => {
    const prev = state();
    const next = state({ [field]: value });
    assert.deepEqual(diffStates(prev, next), { [field]: [prev[field], value] });
  });
}

test('tag order does not count as a change; membership does', () => {
  assert.deepEqual(diffStates(state({ tags: ['a', 'b'] }), state({ tags: ['b', 'a'] })), {});
  assert.deepEqual(diffStates(state({ tags: ['a'] }), state({ tags: ['a', 'b'] })), { tags: [['a'], ['a', 'b']] });
});

test('several fields changing at once are all reported', () => {
  const changes = diffStates(state(), state({ status: 'in-progress', has_session: true, harness: 'claude' }));
  assert.deepEqual(Object.keys(changes).sort(), ['harness', 'has_session', 'status'].sort());
});
