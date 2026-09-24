'use strict';

// Pure helpers for the page (no DOM), so they can be tested under node.
const Logic = (() => {
  const PRIORITIES = ['high', 'medium', 'low'];

  // Raw CLI/network text becomes a sentence a person can act on. The original stays available
  // (callers put it in a tooltip); it is just not the headline any more.
  function friendlyError(raw) {
    const msg = String(raw ?? '').trim();
    const first = msg.split('\n').map(l => l.trim()).find(Boolean) ?? '';
    const rules = [
      [/requires a task ref/i, 'Focusflow could not tell which task to use. Please try again.'],
      [/failed to fetch|networkerror|load failed|econnrefused/i, 'Focusflow is not answering. Check that it is running, then try again.'],
      [/timed? ?out|etimedout/i, 'That took too long. It may still be running; check again in a moment.'],
      [/session .*(live|running)|already running|is live/i, 'That task already has an open session.'],
      [/no such task|not found/i, 'That item no longer exists. Refresh and try again.'],
      [/database is locked|busy/i, 'Focusflow is busy right now. Try again in a few seconds.'],
      [/permission|eacces/i, 'Focusflow does not have permission to do that.'],
    ];
    for (const [re, text] of rules) if (re.test(msg)) return text;
    const clean = first.replace(/^(error|fatal):\s*/i, '').replace(/^focusflow:\s*/i, '');
    if (!clean) return 'Something went wrong. Please try again.';
    const sentence = clean.charAt(0).toUpperCase() + clean.slice(1);
    return /[.!?]$/.test(sentence) ? sentence : `${sentence}.`;
  }

  // "3m ago" up to a day, then "Yesterday 4:12 PM", then "Sep 20".
  function humanTime(iso, now = new Date(), locale = 'en-US') {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '';
    const diff = Math.max(0, now.getTime() - t);
    const m = Math.floor(diff / 60000);
    if (m < 1) return 'just now';
    if (m < 60) return `${m}m ago`;
    if (m < 60 * 24) return `${Math.floor(m / 60)}h ago`;
    const d = new Date(t);
    const startOfDay = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const days = Math.round((startOfDay(now) - startOfDay(d)) / 86400000);
    if (days === 1) return `Yesterday ${d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })}`;
    if (days < 7) return `${days}d ago`;
    return d.toLocaleDateString(locale, { month: 'short', day: 'numeric' });
  }

  function duration(seconds) {
    if (!Number.isFinite(seconds) || seconds < 0) return '';
    const s = Math.round(seconds);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`;
    return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
  }

  // "Write the launch note #website !high" -> name, project (a slug from the known list), priority.
  // An unknown #tag stays in the name: nothing is silently dropped.
  function parseQuickAdd(text, projectSlugs = []) {
    let name = String(text ?? '');
    let project = null;
    let priority = null;
    const known = new Set(projectSlugs);
    name = name.replace(/(^|\s)#([a-z0-9][a-z0-9-]*)(?=\s|$)/gi, (all, sp, slug) => {
      if (project === null && known.has(slug.toLowerCase())) { project = slug.toLowerCase(); return sp; }
      return all;
    });
    name = name.replace(/(^|\s)!(high|medium|low)(?=\s|$)/gi, (all, sp, p) => {
      if (priority === null) { priority = p.toLowerCase(); return sp; }
      return all;
    });
    return { name: name.replace(/\s+/g, ' ').trim(), project, priority };
  }

  function filterTasks(list, { project = '', priority = '', text = '' } = {}) {
    const q = String(text).trim().toLowerCase();
    return list.filter(t =>
      (!project || (project === '__none__' ? !t.project : t.project === project)) &&
      (!priority || t.priority === priority) &&
      (!q || `${t.name} ${t.slug} ${(t.tags ?? []).join(' ')}`.toLowerCase().includes(q)));
  }

  // Dragging a card to another lane: which action does that mean? `null` explains itself via `hint`.
  const DROPS = {
    next: { backlog: 'next', paused: 'next', 'in-progress': 'next', review: 'next' },
    paused: { next: 'pause', 'in-progress': 'pause', review: 'pause' },
    review: { next: 'review', 'in-progress': 'review', paused: 'review' },
    done: { review: 'approve' },
    'in-progress': { paused: 'unpause', review: 'unpause', done: 'unpause' },
  };
  function dropAction(task, target) {
    if (!task || task.archived || task.status === target) return { action: null, hint: null };
    if (task.kind === 'project_agent') return { action: null, hint: 'A project coordinator cannot be moved.' };
    const action = DROPS[target]?.[task.status] ?? null;
    if (action) return { action, hint: null };
    if (target === 'in-progress') return { action: null, hint: 'Click "Open terminal" to start work on it.' };
    if (target === 'done') return { action: null, hint: 'Only work in Review can be approved.' };
    return { action: null, hint: 'That move is not available.' };
  }

  const isEditing = el => !!el && ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName);

  // A cheap, stable change detector for "only redraw when the data changed".
  const fingerprint = v => JSON.stringify(v);

  return { PRIORITIES, friendlyError, humanTime, duration, parseQuickAdd, filterTasks, dropAction, isEditing, fingerprint };
})();

if (typeof module !== 'undefined') module.exports = Logic;
