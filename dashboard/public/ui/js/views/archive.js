import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { act, projectAct, notify } from '../state.js';
import { onRoute } from '../router.js';
import { empty } from '../ui.js';
import { humanTime } from '../logic.js';

let data = null;
let mounted = false;
onRoute(r => { if (r.name !== 'archive') mounted = false; });
async function fetchAll() { try { data = await api('/api/archive'); } catch { data = data ?? { projects: [], tasks: [] }; } notify(); }

export const archive = {
  title: () => 'Archive',
  deps: () => [data],
  render() {
    if (!mounted) { mounted = true; fetchAll(); }
    if (!data) return h('div.page', h('div.empty', 'Loading…'));
    const restore = (fn) => h('button.btn.sm', { type: 'button', on: { click: async e => { e.stopPropagation(); e.currentTarget.disabled = true; await fn(); fetchAll(); } } }, icon('undo'), 'Restore');
    return h('div.page',
      h('div.page-head', h('div', h('h1', 'Archive'), h('div.sub', 'Put away, never deleted. Restore anything at any time.'))),
      !data.projects.length && !data.tasks.length ? empty({ art: 'archive', title: 'The archive is empty', body: 'Archived projects and tasks appear here.' }) : null,
      data.projects.length ? h('div.section', h('div.section-h', 'Projects', h('span.count', String(data.projects.length))),
        h('div.list', data.projects.map(p => h('div.item', { style: 'cursor:default' }, icon('folder'), h('div.body', h('div.title', p.name), h('div.meta', `${p.tasks.length} task${p.tasks.length === 1 ? '' : 's'}`)), h('div.end', restore(() => projectAct(p.slug, 'unarchive'))))))) : null,
      data.tasks.length ? h('div.section', h('div.section-h', 'Tasks', h('span.count', String(data.tasks.length))),
        h('div.list', data.tasks.map(t => h('div.item', { style: 'cursor:default' }, icon('tasks'), h('div.body', h('div.title', t.name), h('div.meta', [t.project, `updated ${humanTime(t.updated_at)}`].filter(Boolean).join(' · '))), h('div.end', restore(() => act(t.slug, 'unarchive'))))))) : null);
  },
};
