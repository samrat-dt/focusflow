// Spec section 5.4: exactly what a snapshot records for each task.
export const STATE_FIELDS = [
  'slug', 'name', 'project', 'kind', 'status', 'priority', 'tags', 'due_date', 'waiting_on',
  'assignee', 'harness', 'has_session', 'session_started', 'session_last_resumed',
  'status_changed_at', 'created_at', 'updated_at', 'archived', 'auto_run_status',
  'notes_count', 'brief_sha256',
];

const canon = v => JSON.stringify(Array.isArray(v) ? [...v].sort() : (v ?? null));

// { field: [old, new] } for every field that differs. Tag order is not a change.
export function diffStates(prev, next) {
  const changes = {};
  for (const f of STATE_FIELDS) {
    if (canon(prev[f]) !== canon(next[f])) changes[f] = [prev[f] ?? null, next[f] ?? null];
  }
  return changes;
}
