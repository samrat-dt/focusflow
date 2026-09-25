// Pure helpers (no DOM): wording, time, parsing, filtering, grouping. Tested under node.

export const STAGES = ['backlog', 'next', 'in-progress', 'paused', 'review', 'done'];
export const STAGE_NAME = { backlog: 'Backlog', next: 'Up next', 'in-progress': 'In progress', paused: 'Paused', review: 'Ready for review', done: 'Done' };
export const PRIORITY_NAME = { high: 'High', medium: 'Medium', low: 'Low' };
const PRI_RANK = { high: 0, medium: 1, low: 2 };

export function friendlyError(raw) {
  const msg = String(raw?.message ?? raw ?? '').trim();
  const first = msg.split('\n').map(l => l.trim()).find(Boolean) ?? '';
  const rules = [
    [/requires a task ref/i, 'Focusflow could not tell which task to use. Please try again.'],
    [/failed to fetch|networkerror|load failed|econnrefused|not answering/i, 'Focusflow is not answering. Check that it is running, then try again.'],
    [/timed? ?out|etimedout/i, 'That took too long. It may still be running; check again in a moment.'],
    [/already running|is live/i, 'That task already has a live session.'],
    [/no such task|not found/i, 'That item no longer exists. Refresh and try again.'],
    [/database is locked|busy/i, 'Focusflow is busy right now. Try again in a few seconds.'],
    [/permission|eacces/i, 'Focusflow does not have permission to do that.'],
  ];
  for (const [re, text] of rules) if (re.test(msg)) return text;
  const clean = first.replace(/^(error|fatal):\s*/i, '').replace(/^focusflow:\s*/i, '');
  if (!clean) return 'Something went wrong. Please try again.';
  const s = clean.charAt(0).toUpperCase() + clean.slice(1);
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

export function humanTime(iso, now = new Date(), locale = 'en-US') {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const diff = now.getTime() - t;
  if (diff < 0) {
    const m = Math.round(-diff / 60000);
    if (m < 60) return `in ${Math.max(1, m)}m`;
    if (m < 1440) return `in ${Math.round(m / 60)}h`;
    return new Date(t).toLocaleDateString(locale, { month: 'short', day: 'numeric' });
  }
  const m = Math.floor(diff / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  if (m < 60 * 24) return `${Math.floor(m / 60)}h ago`;
  const d = new Date(t);
  const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((day(now) - day(d)) / 86400000);
  if (days === 1) return 'Yesterday';
  if (days < 7) return `${days}d ago`;
  return d.toLocaleDateString(locale, { month: 'short', day: 'numeric', ...(d.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}) });
}

export function duration(seconds) {
  if (!Number.isFinite(seconds) || seconds < 0) return '';
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
}

export function greeting(now = new Date()) {
  const h = now.getHours();
  return h < 5 ? 'Working late' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening';
}

// "Write the launch note #website !high" -> { name, project, priority }. Unknown #words stay in the name.
export function parseQuickAdd(text, projectSlugs = []) {
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

export function filterTasks(list, { project = '', priority = '', text = '', stage = '' } = {}) {
  const q = String(text).trim().toLowerCase();
  return list.filter(t =>
    (!project || (project === '__none__' ? !t.project : t.project === project)) &&
    (!priority || t.priority === priority) &&
    (!stage || t.status === stage) &&
    (!q || `${t.name} ${t.slug} ${(t.tags ?? []).join(' ')}`.toLowerCase().includes(q)));
}

export function sortTasks(list) {
  return [...list].sort((a, b) =>
    Number(!!b.headless_running || !!b.terminal_open) - Number(!!a.headless_running || !!a.terminal_open) ||
    (PRI_RANK[a.priority] ?? 9) - (PRI_RANK[b.priority] ?? 9) ||
    String(b.updated_at).localeCompare(String(a.updated_at)));
}

export function groupTasks(list, by) {
  const key = { stage: t => t.status, project: t => t.project ?? '', priority: t => t.priority }[by] ?? (() => '');
  const order = by === 'stage' ? STAGES : by === 'priority' ? ['high', 'medium', 'low'] : null;
  const groups = new Map();
  for (const t of list) { const k = key(t); if (!groups.has(k)) groups.set(k, []); groups.get(k).push(t); }
  const keys = [...groups.keys()].sort((a, b) => order ? order.indexOf(a) - order.indexOf(b) : (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)));
  return keys.map(k => ({ key: k, tasks: sortTasks(groups.get(k)) }));
}

// Dragging a card to another lane: which action that means. A refusal explains itself.
const DROPS = {
  backlog: { next: 'backlog', paused: 'backlog', 'in-progress': 'backlog', review: 'backlog' },
  next: { backlog: 'next', paused: 'next', 'in-progress': 'next', review: 'next' },
  paused: { next: 'pause', 'in-progress': 'pause', review: 'pause', backlog: 'pause' },
  review: { next: 'review', 'in-progress': 'review', paused: 'review' },
  done: { review: 'approve', 'in-progress': 'approve', paused: 'approve' },
  'in-progress': { paused: 'unpause', review: 'unpause', done: 'unpause', next: 'unpause', backlog: 'unpause' },
};
export function dropAction(task, target) {
  if (!task || task.archived || task.status === target) return { action: null, hint: null };
  if (task.kind === 'project_agent') return { action: null, hint: 'A project assistant stays where it is.' };
  const action = DROPS[target]?.[task.status] ?? null;
  return action ? { action, hint: null } : { action: null, hint: 'That move is not available.' };
}

// The one thing to do next with a task, for its main button.
export function primaryAction(t) {
  if (t.archived) return { action: 'unarchive', label: 'Restore' };
  if (t.status === 'review') return { action: 'approve', label: 'Approve' };
  if (t.status === 'done') return { action: 'unpause', label: 'Reopen' };
  if (t.status === 'paused') return { action: 'unpause', label: 'Resume' };
  if (t.status === 'backlog') return { action: 'next', label: 'Move to Up next' };
  return { action: 'message', label: t.status === 'next' ? 'Start' : 'Message' };
}

// A short, human description of what is happening right now.
export function liveLine(t, run) {
  if (t.headless_running) return run?.activity?.summary ? `Working: ${run.activity.summary}` : 'Working in the background';
  if (t.auto_run_status === 'dead') return 'The background run stopped unexpectedly';
  if (t.auto_run_status === 'timed_out') return 'Stopped at the project’s time limit';
  if (t.terminal_open) return t.working ? 'Working in a terminal' : 'Open in a terminal';
  return null;
}

export const fingerprint = v => JSON.stringify(v);

// A tiny, safe Markdown renderer to DOM-friendly blocks: headings, lists, quotes, code, paragraphs,
// tables, **bold**, *italic*, `code` and [links](https://…). Returns a tree of {tag, children}.
export function markdownBlocks(src) {
  const lines = String(src ?? '').replace(/\r/g, '').split('\n');
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      const code = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) code.push(lines[i++]);
      i++;
      out.push({ tag: 'pre', text: code.join('\n') });
      continue;
    }
    const st = /^<p class="stamp">(.*)<\/p>\s*$/.exec(line);
    if (st) { out.push({ tag: 'stamp', inline: st[1] }); i++; continue; }
    const hm = /^(#{1,3})\s+(.*)$/.exec(line);
    if (hm) { out.push({ tag: `h${hm[1].length}`, inline: hm[2] }); i++; continue; }
    if (/^(-{3,}|\*{3,})\s*$/.test(line)) { out.push({ tag: 'hr' }); i++; continue; }
    if (/^\s*[-*]\s+/.test(line) || /^\s*\d+\.\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line);
      const items = [];
      while (i < lines.length && (ordered ? /^\s*\d+\.\s+/ : /^\s*[-*]\s+/).test(lines[i])) items.push(lines[i++].replace(/^\s*([-*]|\d+\.)\s+/, ''));
      out.push({ tag: ordered ? 'ol' : 'ul', items });
      continue;
    }
    if (/^>\s?/.test(line)) {
      const q = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) q.push(lines[i++].replace(/^>\s?/, ''));
      out.push({ tag: 'blockquote', inline: q.join(' ') });
      continue;
    }
    if (/^\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\|[\s:|-]+\|\s*$/.test(lines[i + 1])) {
      const cells = l => l.trim().replace(/^\||\|$/g, '').split('|').map(c => c.trim());
      const head = cells(line);
      i += 2;
      const rows = [];
      while (i < lines.length && /^\|.*\|\s*$/.test(lines[i])) rows.push(cells(lines[i++]));
      out.push({ tag: 'table', head, rows });
      continue;
    }
    if (!line.trim()) { i++; continue; }
    const para = [];
    while (i < lines.length && lines[i].trim() && !/^(#{1,3}\s|```|>\s?|\s*[-*]\s+|\s*\d+\.\s+|\|)/.test(lines[i])) para.push(lines[i++]);
    if (!para.length) para.push(lines[i++]);
    out.push({ tag: 'p', inline: para.join(' ') });
  }
  return out;
}

// Inline Markdown -> [{t:'text'|'b'|'i'|'code'|'a', v, href?}].
export function inlineTokens(s) {
  const out = [];
  const re = /(\*\*([^*]+)\*\*|`([^`]+)`|\[([^\]]+)\]\((https?:\/\/[^\s)]+|#[^\s)]*)\)|\*([^*]+)\*)/g;
  let last = 0;
  let m;
  while ((m = re.exec(s))) {
    if (m.index > last) out.push({ t: 'text', v: s.slice(last, m.index) });
    if (m[2]) out.push({ t: 'b', v: m[2] });
    else if (m[3]) out.push({ t: 'code', v: m[3] });
    else if (m[4]) out.push({ t: 'a', v: m[4], href: m[5] });
    else if (m[6]) out.push({ t: 'i', v: m[6] });
    last = re.lastIndex;
  }
  if (last < s.length) out.push({ t: 'text', v: s.slice(last) });
  return out;
}

// ---- Activity map layout: deterministic, so nothing jumps between refreshes ----
// You in the centre; one cluster per project on a ring around you, each given room in proportion to its
// size; a project's assistant at the centre of its cluster, its tasks on a circle around it with their labels
// pointing outwards. The canvas grows to fit. Returns positions, label anchors, cluster labels and the size.
export function layoutActivity(nodes, { minWidth = 1060, minHeight = 560 } = {}) {
  const groups = new Map();
  for (const n of nodes) {
    if (n.id === 'you') continue;
    const key = n.kind === 'person' ? '@people' : n.project ?? '';
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(n);
  }
  const keys = [...groups.keys()].sort((a, b) => groups.get(b).length - groups.get(a).length || a.localeCompare(b));
  const clusters = keys.map(key => {
    const members = [...groups.get(key)].sort((a, b) => (a.kind === 'assistant' ? -1 : b.kind === 'assistant' ? 1 : a.id.localeCompare(b.id)));
    const hub = members[0]?.kind === 'assistant' ? members.shift() : null;
    const r = members.length <= 1 && !hub ? 0 : Math.min(190, 40 + members.length * 13);
    return { key, members, hub, r, room: r + (r === 0 ? 140 : members.length > 4 ? 190 : 120) }; // room: the cluster plus its (sideways) labels
  });
  const totalRoom = clusters.reduce((t, c) => t + c.room, 0);
  const biggest = Math.max(0, ...clusters.map(c => c.room));
  // A ring long enough that neighbouring clusters do not overlap, and clear of you in the middle.
  const R = clusters.length <= 1 ? (clusters[0]?.room ?? 0) + 60 : Math.max(240, (totalRoom * 2) / (2 * Math.PI) * 1.05, biggest + 90);
  // Side room for labels that point outwards, in the same units the labels will be drawn at.
  const base = Math.ceil(2 * (R + biggest) + 40);
  const width = Math.max(minWidth, Math.ceil(base + 2 * 150 * Math.max(1, base / minWidth)));
  const height = Math.max(minHeight, Math.ceil(2 * (R * 0.78 + biggest) + 40));
  const cx = width / 2;
  const cy = height / 2;
  const pos = new Map([['you', { x: cx, y: cy }]]);
  const anchor = new Map();
  const labels = [];
  let angle = clusters.length === 2 ? 0 : -Math.PI / 2;
  clusters.forEach((c, i) => {
    const span = (2 * Math.PI * c.room) / Math.max(1, totalRoom);
    const mid = clusters.length === 1 ? 0 : angle + (i === 0 && clusters.length !== 2 ? 0 : span / 2);
    if (!(i === 0 && clusters.length !== 2)) angle += span; else angle += span / 2;
    const gx = clusters.length === 1 ? cx + R * 0.7 : cx + R * Math.cos(mid);
    const gy = clusters.length === 1 ? cy : cy + R * 0.78 * Math.sin(mid);
    if (c.hub) { pos.set(c.hub.id, { x: gx, y: gy }); anchor.set(c.hub.id, 'below'); }
    c.members.forEach((m, j) => {
      const a = -Math.PI / 2 + (2 * Math.PI * j) / Math.max(1, c.members.length);
      pos.set(m.id, { x: gx + c.r * Math.cos(a), y: gy + c.r * Math.sin(a) });
      // The top of a cluster belongs to its heading: labels there go sideways, never above.
      anchor.set(m.id, c.r === 0 ? 'below' : Math.abs(Math.cos(a)) < 0.35 ? (Math.sin(a) < 0 ? 'right' : 'below') : Math.cos(a) > 0 ? 'right' : 'left');
    });
    labels.push({ key: c.key, x: gx, y: Math.max(16, gy - c.r - (c.r === 0 ? 26 : 30)) });
  });
  return { pos, anchor, labels, width, height };
}

// A curve from a to b that bows sideways, so two-way and parallel lines stay apart.
export function curvePath(a, b, bend = 0.16) {
  const mx = (a.x + b.x) / 2;
  const my = (a.y + b.y) / 2;
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  return `M${a.x.toFixed(1)},${a.y.toFixed(1)} Q${(mx - dy * bend).toFixed(1)},${(my + dx * bend).toFixed(1)} ${b.x.toFixed(1)},${b.y.toFixed(1)}`;
}
