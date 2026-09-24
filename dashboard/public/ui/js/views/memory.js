import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { notify } from '../state.js';
import { onRoute, route, setQuery } from '../router.js';
import { empty } from '../ui.js';
import { memoryList, rememberForm } from './task.js';

let data = null;
let mounted = false;
onRoute(r => { if (r.name !== 'memory') mounted = false; });
async function fetchAll() {
  try { data = (await api(`/api/memories${route.query.all === '1' ? '?all=1' : ''}`)).memories; } catch { data = data ?? []; }
  notify();
}

export const memory = {
  title: () => 'Memory',
  deps: r => [data, r.query],
  render(r) {
    if (!mounted) { mounted = true; fetchAll(); }
    const q = (r.query.q ?? '').toLowerCase();
    const scope = r.query.scope ?? '';
    const list = (data ?? []).filter(m => (!scope || m.scope === scope) && (!q || `${m.body} ${m.question ?? ''}`.toLowerCase().includes(q)));
    const search = h('input.input', { type: 'search', placeholder: 'Search memory…', value: r.query.q ?? '', style: 'width:240px', dataset: { key: 'mem-q' } });
    let t;
    search.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => setQuery({ q: search.value }), 250); });
    return h('div.page',
      h('div.page-head', h('div', h('h1', 'Memory'), h('div.sub', 'What you’ve told Claude, remembered for every future session. You never have to say it twice.'))),
      rememberForm({ onDone: fetchAll }),
      h('div.toolbar',
        h('div.seg', [['', 'All'], ['global', 'Everywhere'], ['project', 'Projects'], ['task', 'Tasks']].map(([v, l]) => h('button', { type: 'button', 'aria-pressed': String(scope === v), on: { click: () => setQuery({ scope: v }) } }, l))),
        h('span.grow'), search),
      data === null ? h('div.empty', 'Loading…') : list.length ? memoryList(list, { onChange: fetchAll })
        : empty({ art: 'memory', title: q || scope ? 'Nothing matches' : 'Nothing remembered yet', body: 'When you answer a question or give a task an instruction, Focusflow remembers it and hands it to every later session. You can also teach it things above.' }));
  },
};
