import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { notify } from '../state.js';
import { go, setQuery, onRoute } from '../router.js';
import { empty } from '../ui.js';
import { humanTime, layoutActivity, curvePath, STAGE_NAME } from '../logic.js';

// Who talks to whom: you, your agents, and agents with each other. A map, and a feed.
let data = null;
let loadedFor = null;
let mounted = false;
let selected = null; // { type: 'node' | 'edge', id }
onRoute(r => { if (r.name !== 'activity') { mounted = false; selected = null; } });

async function fetchData(since) {
  try { data = await api(`/api/activity?since=${since}`); loadedFor = since; } catch { data = data ?? { nodes: [], edges: [], events: [], stats: {} }; }
  notify();
}

const SVG = 'http://www.w3.org/2000/svg';
function s(tag, attrs = {}, ...kids) {
  const el = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null || v === false) continue;
    // Colours are CSS variables: set them through the style object, which every engine resolves.
    if ((k === 'fill' || k === 'stroke') && String(v).includes('var(')) el.style.setProperty(k, String(v));
    else el.setAttribute(k, String(v));
  }
  for (const k of kids.flat()) if (k) el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  return el;
}

const EDGE_STYLE = {
  you: { stroke: 'var(--muted)', dash: null, label: 'You and an agent' },
  agents: { stroke: 'var(--accent)', dash: null, label: 'Agent to agent' },
  broadcast: { stroke: 'var(--review)', dash: '2 5', label: 'Broadcast' },
  watch: { stroke: 'var(--next)', dash: '7 5', label: 'Watching' },
};
const STATUS_COLOR = { backlog: 'var(--backlog)', next: 'var(--next)', 'in-progress': 'var(--progress)', paused: 'var(--paused)', review: 'var(--review)', done: 'var(--done)' };
const trunc = (t, n) => (t.length > n ? `${t.slice(0, n - 1)}…` : t);
const reduceMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;

function nameOf(id) { return data.nodes.find(n => n.id === id)?.name ?? id; }
function involves(ev, id) { return ev.from === id || ev.to.includes(id); }

function map() {
  const { pos, labels, anchor, width: W, height: H } = layoutActivity(data.nodes);
  // The map is scaled to fit its box; grow sizes with the canvas so text and dots look the same size.
  const k = Math.max(1, W / 1060);
  const fs = px => (px * k).toFixed(1);
  const svg = s('svg', { viewBox: `0 0 ${W} ${H}`, class: 'amap', role: 'img', 'aria-label': 'Map of who talks to whom. The feed beside it lists the same messages.' });
  const defs = s('defs');
  for (const [k, st] of Object.entries(EDGE_STYLE)) {
    defs.append(s('marker', { id: `arrow-${k}`, viewBox: '0 0 10 10', refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: 'auto-start-reverse' }, s('path', { d: 'M0,1 L9,5 L0,9 z', fill: st.stroke })));
  }
  svg.append(defs);
  const sel = selected;
  const focusNode = sel?.type === 'node' ? sel.id : null;
  const focusEdge = sel?.type === 'edge' ? sel.id : null;

  for (const l of labels) {
    const title = l.key === '' ? 'No project' : l.key === '@people' ? 'People' : (data.nodes.find(n => n.project === l.key && n.kind === 'assistant')?.name.replace(/ assistant$/, '') ?? data.nodes.find(n => n.project === l.key)?.projectName ?? l.key);
    svg.append(s('text', { x: l.x, y: l.y - (k - 1) * 12, class: 'amap-cluster', 'text-anchor': 'middle', 'font-size': fs(11.5), 'stroke-width': (4 * k).toFixed(1) }, title));
  }

  // lines first, nodes on top
  const drawn = new Set();
  for (const e of data.edges) {
    const a = pos.get(e.from);
    const b = pos.get(e.to);
    if (!a || !b || e.from === e.to) continue;
    const pair = [e.from, e.to].sort().join('|') + e.kind;
    if (e.both && drawn.has(pair)) continue;
    drawn.add(pair);
    const st = EDGE_STYLE[e.kind];
    const dim = (focusNode && e.from !== focusNode && e.to !== focusNode) || (focusEdge && focusEdge !== e.id);
    const d = curvePath(a, b, { you: 0.14, agents: 0.14, broadcast: 0.32, watch: -0.24 }[e.kind]); // each kind bows its own way, so shared pairs stay apart
    const width = k * (e.kind === 'watch' ? 1.5 : 1.5 + Math.min(4.5, Math.log2(e.count + 1) * 1.3));
    const g = s('g', { class: `amap-edge${dim ? ' dim' : ''}`, tabindex: 0, role: 'button', 'aria-label': `${st.label}: ${nameOf(e.from)} ${e.both ? 'and' : 'to'} ${nameOf(e.to)}, ${e.count} message${e.count === 1 ? '' : 's'}` });
    const path = s('path', { d, fill: 'none', stroke: st.stroke, 'stroke-width': width, 'stroke-dasharray': st.dash, 'stroke-linecap': 'round', 'marker-end': `url(#arrow-${e.kind})`, 'marker-start': e.both ? `url(#arrow-${e.kind})` : null, id: `p-${btoa(unescape(encodeURIComponent(e.id))).replace(/[^a-z0-9]/gi, '')}` });
    g.append(s('path', { d, fill: 'none', stroke: 'transparent', 'stroke-width': 16 * k }), path);
    if (e.recent && !reduceMotion()) {
      const dot = s('circle', { r: 4 * k, fill: st.stroke });
      dot.append(s('animateMotion', { dur: '2.4s', repeatCount: 'indefinite', path: d }));
      g.append(dot);
    }
    g.append(s('title', {}, `${st.label} · ${e.count} · last ${humanTime(e.last_at)}${e.last ? `: ${e.last}` : ''}`));
    const pick = () => { selected = focusEdge === e.id ? null : { type: 'edge', id: e.id }; notify(); };
    g.addEventListener('click', pick);
    g.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); pick(); } });
    svg.append(g);
  }

  for (const n of data.nodes) {
    const p = pos.get(n.id);
    if (!p) continue;
    const r = k * (n.kind === 'you' ? 26 : n.kind === 'assistant' ? 18 : 13);
    const dim = (focusNode && focusNode !== n.id && !data.edges.some(e => (e.from === focusNode && e.to === n.id) || (e.to === focusNode && e.from === n.id)))
      || (focusEdge && !data.edges.some(e => e.id === focusEdge && (e.from === n.id || e.to === n.id)));
    const ring = n.kind === 'you' ? 'var(--accent)' : STATUS_COLOR[n.status] ?? 'var(--backlog)';
    const unread = data.edges.filter(e => e.from === n.id && e.to === 'you').reduce((t, e) => t + e.unread, 0);
    const g = s('g', { class: `amap-node${dim ? ' dim' : ''}${focusNode === n.id ? ' on' : ''}`, transform: `translate(${p.x.toFixed(1)},${p.y.toFixed(1)})`, tabindex: 0, role: 'button',
      'aria-label': `${n.name}${n.kind === 'you' ? '' : `, ${n.kind === 'assistant' ? 'project assistant' : STAGE_NAME[n.status] ?? n.status}`}${n.working || n.running ? ', working now' : ''}${unread ? `, ${unread} unread for you` : ''}` });
    if (n.working || n.running) g.append(s('circle', { r: r + 7 * k, class: 'amap-pulse', stroke: 'var(--progress)', 'stroke-width': 2 * k }));
    g.append(s('circle', { r, fill: n.kind === 'you' ? 'var(--accent)' : 'var(--surface)', stroke: ring, 'stroke-width': n.kind === 'you' ? 0 : 3 * k }));
    if (n.kind === 'you') g.append(s('text', { y: 5 * k, 'text-anchor': 'middle', class: 'amap-you', 'font-size': fs(12) }, 'You'));
    if (n.kind === 'assistant') g.append(s('path', { d: 'M0,-6 L1.8,-1.8 L6,0 L1.8,1.8 L0,6 L-1.8,1.8 L-6,0 L-1.8,-1.8 z', fill: ring, transform: `scale(${k.toFixed(2)})` }));
    if (unread) {
      g.append(s('circle', { cx: r * 0.75, cy: -r * 0.75, r: 8 * k, fill: 'var(--danger)' }));
      g.append(s('text', { x: r * 0.75, y: -r * 0.75 + 4 * k, 'text-anchor': 'middle', class: 'amap-badge', 'font-size': fs(10) }, String(unread)));
    }
    if (n.kind !== 'you') {
      const side = anchor.get(n.id) ?? 'below';
      const at = { below: { x: 0, y: r + 16 * k, a: 'middle' }, above: { x: 0, y: -r - 8 * k, a: 'middle' }, right: { x: r + 6 * k, y: 4 * k, a: 'start' }, left: { x: -r - 6 * k, y: 4 * k, a: 'end' } }[side];
      g.append(s('text', { x: at.x, y: at.y, 'text-anchor': at.a, class: 'amap-label', 'font-size': fs(13), 'stroke-width': (4 * k).toFixed(1) }, trunc(n.name, side === 'below' || side === 'above' ? 22 : 24)));
      if (n.nudges) g.append(s('text', { x: at.x, y: at.y + 14 * k, 'text-anchor': at.a, class: 'amap-sub', 'font-size': fs(11) }, `nudged ${n.nudges}×`));
    }
    g.append(s('title', {}, n.name));
    const pick = () => { selected = focusNode === n.id ? null : { type: 'node', id: n.id }; notify(); };
    g.addEventListener('click', pick);
    g.addEventListener('dblclick', () => { if (n.kind === 'task' || n.kind === 'assistant') go(`/task/${n.id}`); });
    g.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); pick(); } });
    svg.append(g);
  }
  svg.addEventListener('click', ev => { if (ev.target === svg && selected) { selected = null; notify(); } });
  return svg;
}

function eventRow(ev) {
  const st = EDGE_STYLE[ev.kind];
  const to = ev.to.length > 2 ? `${nameOf(ev.to[0])} and ${ev.to.length - 1} more` : ev.to.map(nameOf).join(', ');
  const target = [ev.from, ...ev.to].find(id => id !== 'you' && !id.startsWith('@'));
  return h('div.item', { style: 'align-items:flex-start;cursor:pointer', tabindex: '0', on: { click: () => target && go(`/task/${target}`), keydown: e => { if (e.key === 'Enter' && target) go(`/task/${target}`); } } },
    h('span', { style: `width:8px;height:8px;border-radius:50%;margin-top:7px;flex:none;background:${st.stroke}` }),
    h('div.body',
      h('div', { style: 'font-size:13px;color:var(--ink-2)' }, h('strong', nameOf(ev.from)), ev.kind === 'broadcast' ? ' broadcast to ' : ' → ', h('strong', to)),
      h('div', { style: 'margin-top:2px;overflow-wrap:anywhere' }, ev.text),
      h('div.meta', { style: 'margin-top:4px' }, humanTime(ev.at), ev.urgent && h('span.tag.danger', 'Urgent'), ev.state && h('span', ev.state === 'read' ? 'Read' : ev.state === 'delivered' ? 'Delivered' : 'Sent'))));
}

export const activity = {
  title: () => 'Activity',
  deps: r => [data, r.query, selected],
  render(r, ctx) {
    const since = r.query.since ?? '7d';
    const filter = r.query.show ?? 'all';
    if (!mounted || loadedFor !== since) { mounted = true; fetchData(since); }
    const poll = setInterval(() => { if (document.visibilityState === 'visible') fetchData(since); }, 6000);
    ctx.onCleanup(() => clearInterval(poll));
    const head = h('div.page-head',
      h('div', h('h1', 'Activity'), h('div.sub', 'Who’s talking to whom: you, your agents, and agents with each other.')),
      h('div.actions', h('div.seg', [['1d', 'Today'], ['7d', '7 days'], ['30d', '30 days']].map(([v, l]) => h('button', { type: 'button', 'aria-pressed': String(since === v), on: { click: () => setQuery({ since: v === '7d' ? '' : v }) } }, l)))));
    if (!data) return h('div.page.wide', head, h('div.empty', 'Loading…'));
    const st = data.stats;
    const talking = data.edges.filter(e => e.kind !== 'watch').length;
    if (!talking && !data.edges.length) {
      return h('div.page', head, empty({ art: 'network', title: 'No conversations in this period', body: 'When a task asks you something, you reply, a task messages another task, or a project assistant hands work to a subtask, it shows up here as a live map.' }));
    }
    let events = data.events;
    if (filter === 'agents') events = events.filter(e => e.kind === 'agents');
    if (filter === 'you') events = events.filter(e => e.kind === 'you');
    if (filter === 'broadcast') events = events.filter(e => e.kind === 'broadcast');
    let selTitle = null;
    if (selected?.type === 'node') { events = events.filter(e => involves(e, selected.id)); selTitle = nameOf(selected.id); }
    if (selected?.type === 'edge') {
      const e = data.edges.find(x => x.id === selected.id);
      if (e) { events = events.filter(ev => ev.kind === e.kind && ((ev.from === e.from && ev.to.includes(e.to)) || (e.both && ev.from === e.to && ev.to.includes(e.from)))); selTitle = `${nameOf(e.from)} ${e.both ? '⇄' : '→'} ${nameOf(e.to)}`; }
    }
    const stat = (value, label, hint) => h('div.stat', { title: hint }, h('b', String(value ?? 0)), h('span', label));
    const selNode = selected?.type === 'node' ? data.nodes.find(n => n.id === selected.id) : null;
    return h('div.page.wide',
      head,
      h('div.stats', { style: 'margin-bottom:20px' },
        stat(st.agents, 'agents in conversations'), stat(st.messages, 'messages'), stat(st.between_agents, 'between agents', 'Tasks messaging each other, and project assistants handing work to subtasks'),
        stat(st.broadcasts, 'broadcasts'), stat(st.unread_for_you, 'unread for you')),
      h('div.amap-wrap',
        h('div.amap-card', map(),
          h('div.amap-legend', Object.entries(EDGE_STYLE).map(([k, v]) => h('span', h('i', { style: `border-top:2px ${v.dash ? (k === 'broadcast' ? 'dotted' : 'dashed') : 'solid'} ${v.stroke}` }), v.label)),
            h('span', h('i.dot-ring'), 'Moving dot: talked in the last 10 minutes'), h('span', 'Click someone to see their conversations · double-click to open'))),
        h('div.amap-feed',
          h('div.row', { style: 'margin-bottom:10px;flex-wrap:wrap' },
            selTitle ? h('div.grow', h('strong', selTitle), h('button.btn.ghost.sm', { type: 'button', style: 'margin-left:6px', on: { click: () => { selected = null; notify(); } } }, icon('x'), 'Show everyone'),
              selNode && (selNode.kind === 'task' || selNode.kind === 'assistant') && h('button.btn.sm', { type: 'button', style: 'margin-left:6px', on: { click: () => go(`/task/${selNode.id}`) } }, 'Open'))
              : h('div.seg', [['all', 'All'], ['agents', 'Agent to agent'], ['you', 'With you'], ['broadcast', 'Broadcasts']].map(([v, l]) => h('button', { type: 'button', 'aria-pressed': String(filter === v), on: { click: () => setQuery({ show: v === 'all' ? '' : v }) } }, l)))),
          events.length ? h('div.list', events.slice(0, 60).map(eventRow)) : h('div', { style: 'color:var(--muted);padding:12px 0' }, 'Nothing here in this period.'))));
  },
};
