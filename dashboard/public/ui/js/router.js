// Hash routes: #/home, #/tasks?view=board, #/task/<slug>?tab=memory, #/project/<slug>, #/help/<article>…
const listeners = new Set();
export let route = parse();
export let routeOpts = {};

function parse() {
  const raw = location.hash.replace(/^#/, '') || '/home';
  const [p, q = ''] = raw.split('?');
  const parts = p.split('/').filter(Boolean);
  return { path: p, name: parts[0] ?? 'home', id: parts[1] ? decodeURIComponent(parts[1]) : null, query: Object.fromEntries(new URLSearchParams(q)) };
}

export function go(path, opts = {}) {
  routeOpts = opts;
  const target = `#${path}`;
  if (location.hash === target) { route = parse(); for (const fn of listeners) fn(route); return; }
  if (opts.replace) history.replaceState(null, '', target); else history.pushState(null, '', target);
  route = parse();
  for (const fn of listeners) fn(route);
}

// Change query parameters without a new history entry.
export function setQuery(patch) {
  const q = { ...route.query, ...patch };
  for (const k of Object.keys(q)) if (q[k] === '' || q[k] == null) delete q[k];
  const qs = new URLSearchParams(q).toString();
  go(`${route.path}${qs ? `?${qs}` : ''}`, { replace: true, keepScroll: true });
}

export const onRoute = fn => listeners.add(fn);
// After start-up rewrites the address (token links from the Mac app), read it again.
export function reparse() { route = parse(); }
addEventListener('popstate', () => { routeOpts = {}; route = parse(); for (const fn of listeners) fn(route); });
addEventListener('hashchange', () => { const r = parse(); if (r.path !== route.path || JSON.stringify(r.query) !== JSON.stringify(route.query)) { routeOpts = {}; route = r; for (const fn of listeners) fn(route); } });
