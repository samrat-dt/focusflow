// The Activity map's model: who talks to whom on the message bus. Pure: the server passes in bus rows
// (readBus) and the task views it already has.
//
// Addresses: a message from/to the human has no task slug (assignee "user"). Anything else with a task
// slug is that task's session. A project assistant is a task of kind project_agent.

const YOU = 'you';
const flat = s => String(s ?? '').replace(/\s*\(via focusflow:[\s\S]*\)\s*$/, '').replace(/\s+/g, ' ').trim();
const preview = s => { const t = flat(s); return t.length > 140 ? `${t.slice(0, 139)}…` : t; };

function endpoint(assignee, taskSlug) {
  if (taskSlug) return taskSlug;
  return assignee === 'user' || assignee === 'self' ? YOU : `@${assignee}`;
}

// A watcher is a task slug (focusflow), "<assignee>/<task>", or an assignee such as "user"; what it
// watches is a task, a project or an assignee.
function watcherNode(w, isTask) {
  const i = w.indexOf('/');
  if (i >= 0) return w.slice(i + 1);
  return isTask(w) ? w : endpoint(w, null);
}

function delivery(m, toYou) {
  if (m.mail === 'read' || m.read_at) return 'read';
  if (!toYou && m.delivered_at) return 'delivered';
  return 'sent';
}

export function buildActivity({ messages = [], watches = [], nudges = [], tasks = [], projects = [], now = new Date(), recentMs = 10 * 60000 }) {
  const bySlug = new Map(tasks.map(t => [t.slug, t]));
  const projectName = slug => projects.find(p => p.slug === slug)?.name ?? slug;
  const nodes = new Map();
  const node = id => {
    if (nodes.has(id)) return nodes.get(id);
    let n;
    if (id === YOU) n = { id, kind: 'you', name: 'You', project: null };
    else if (id.startsWith('@')) n = { id, kind: 'person', name: id.slice(1), project: null };
    else {
      const t = bySlug.get(id);
      const agent = t?.kind === 'project_agent';
      n = {
        id, kind: agent ? 'assistant' : 'task',
        name: agent && t.project ? `${projectName(t.project)} assistant` : t?.name ?? id,
        project: t?.project ?? null, projectName: t?.project ? projectName(t.project) : null, status: t?.status ?? 'unknown', archived: !!t?.archived,
        working: !!(t?.headless_running || (t?.terminal_open && t?.working)), open: !!t?.terminal_open, running: !!t?.headless_running,
        known: !!t,
      };
    }
    n.sent = 0; n.received = 0; n.nudges = 0;
    nodes.set(id, n);
    return n;
  };
  node(YOU);

  const edges = new Map(); // "from→to|kind" -> edge
  const edge = (from, to, kind) => {
    const key = `${from}→${to}|${kind}`;
    if (!edges.has(key)) edges.set(key, { id: key, from, to, kind, count: 0, urgent: 0, unread: 0, last_at: null, last: null });
    return edges.get(key);
  };
  const events = [];
  const broadcasts = new Map();

  for (const m of messages) {
    const from = endpoint(m.from_assignee, m.from_task_slug);
    const to = endpoint(m.to_assignee, m.to_task_slug);
    node(from).sent += 1;
    if (m.broadcast) {
      // one row per watcher: regroup into one event with every recipient
      const key = `${from}|${m.created_at}|${m.body}`;
      if (!broadcasts.has(key)) {
        const ev = { id: m.id, kind: 'broadcast', from, to: [], at: m.created_at, text: preview(m.body), urgent: !!m.urgent };
        broadcasts.set(key, ev);
        events.push(ev);
      }
      const ev = broadcasts.get(key);
      if (to !== from && !ev.to.includes(to)) ev.to.push(to);
      node(to).received += 1;
      const e = edge(from, to, 'broadcast');
      e.count += 1; e.last_at = m.created_at; e.last = preview(m.body);
      continue;
    }
    node(to).received += 1;
    const kind = from === YOU || to === YOU ? 'you' : 'agents';
    const e = edge(from, to, kind);
    e.count += 1;
    if (m.urgent) e.urgent += 1;
    const state = delivery(m, to === YOU);
    if (state !== 'read' && to === YOU) e.unread += 1;
    e.last_at = m.created_at;
    e.last = preview(m.body);
    events.push({ id: m.id, kind, from, to: [to], at: m.created_at, text: preview(m.body), urgent: !!m.urgent, state });
  }

  const watchEdges = [];
  for (const w of watches) {
    const from = watcherNode(w.watcher, slug => bySlug.has(slug));
    const to = endpoint(w.watched, bySlug.has(w.watched) ? w.watched : null);
    const target = bySlug.has(w.watched) ? w.watched : projects.some(p => p.slug === w.watched) ? `project:${w.watched}` : to;
    if (target.startsWith('project:')) continue; // project-wide watches show on the watcher as a note
    node(from); node(target);
    watchEdges.push({ id: `${from}⇢${target}`, from, to: target, kind: 'watch', since: w.created_at });
  }
  for (const n of nudges) if (nodes.has(n.task_slug) || bySlug.has(n.task_slug)) node(n.task_slug).nudges = n.attempts;

  const recent = at => at && now.getTime() - Date.parse(at) < recentMs;
  const edgeList = [...edges.values()].map(e => ({ ...e, recent: recent(e.last_at) }))
    .concat(watchEdges.map(w => ({ ...w, count: 1, recent: false })));
  // A pair that talks both ways is one conversation: mark it so the map can draw it once.
  for (const e of edgeList) e.both = edgeList.some(o => o.kind === e.kind && o.from === e.to && o.to === e.from);

  const list = [...nodes.values()];
  const agents = list.filter(n => n.kind === 'task' || n.kind === 'assistant');
  return {
    nodes: list,
    edges: edgeList,
    events: events.sort((a, b) => (a.at < b.at ? 1 : -1)).slice(0, 200),
    stats: {
      agents: agents.length,
      messages: events.filter(e => e.kind !== 'broadcast').length,
      between_agents: events.filter(e => e.kind === 'agents').length,
      broadcasts: events.filter(e => e.kind === 'broadcast').length,
      watches: watchEdges.length,
      unread_for_you: edgeList.filter(e => e.to === YOU).reduce((s, e) => s + e.unread, 0),
      live: agents.filter(n => n.working || n.running || n.open).length,
    },
  };
}
