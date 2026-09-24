const DAY = 24 * 60 * 60 * 1000;

function localMidnight(d) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

function dueState(dueDate, now, soonDays) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dueDate ?? '');
  if (!m) return null;
  const due = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])).getTime();
  const days = Math.round((due - localMidnight(now)) / DAY);
  if (days < 0) return 'overdue';
  return days <= soonDays ? 'soon' : null;
}

function isStale(task, now, staleDays) {
  if (task.status !== 'in-progress' || task.archived) return false;
  const last = Math.max(Date.parse(task.updated_at) || 0, Date.parse(task.session_last_resumed) || 0);
  return last > 0 && (now.getTime() - last) / DAY >= staleDays;
}

// The board markers: ! stale, W waiting, * live, D due soon or overdue.
export function signalsFor(task, { live, working, now, tunables }) {
  const open = task.status !== 'done' && !task.archived;
  return {
    stale: isStale(task, now, tunables.stale_days),
    waiting: !!task.waiting_on,
    live: !!live,
    working: !!live && !!working,
    due: open ? dueState(task.due_date, now, tunables.due_soon_days) : null,
  };
}
