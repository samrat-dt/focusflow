import { h } from '../dom.js';
import { icon } from '../icons.js';
import { api } from '../api.js';
import { S, load } from '../state.js';
import { go } from '../router.js';
import { toast, empty } from '../ui.js';
import { humanTime, STAGE_NAME } from '../logic.js';
import { projectName, displayName } from '../parts.js';
import { task } from './task.js';

// Two panes: every conversation on the left, the selected one on the right (the task page's conversation).
export const inbox = {
  title: () => 'Inbox',
  deps: r => [S.inbox, r.id, r.query, r.id && task.deps({ id: r.id, query: r.query })],
  render(r, ctx) {
    const threads = S.inbox.threads;
    const sel = r.id ?? null;
    const list = h('div.list', { style: 'align-self:start' }, threads.map(th => {
      const urgent = th.last.urgent && th.last.direction === 'in';
      return h('div.item', { tabindex: '0', class: sel === th.slug ? 'selected' : '', dataset: { key: `th:${th.slug}` }, style: 'align-items:flex-start',
        on: { click: () => go(`/inbox/${th.slug}`, { replace: !!sel }), keydown: e => { if (e.key === 'Enter') go(`/inbox/${th.slug}`); } } },
        h('div.body',
          h('div.row', { style: 'gap:8px' }, h('div.title.grow', { style: th.unread ? 'font-weight:650' : '' }, displayName(S.tasks.find(x => x.slug === th.slug) ?? th)), th.unread ? h('span.badge', String(th.unread)) : null),
          h('div.meta', urgent && h('span.tag.danger', 'Urgent'), h('span', STAGE_NAME[th.status] ?? th.status), th.project && [h('span.sep'), projectName(th.project)], h('span.sep'), humanTime(th.last.at)),
          h('div', { style: 'color:var(--ink-2);font-size:13px;margin-top:4px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, `${th.last.direction === 'out' ? 'You: ' : ''}${th.last.preview}`)));
    }));
    const markAll = async () => { await Promise.all(threads.filter(t => t.unread).map(t => api(`/api/tasks/${encodeURIComponent(t.slug)}/read`, 'POST', {}).catch(() => null))); toast('All caught up.'); load(); };
    let pane;
    if (!threads.length) pane = null;
    else if (!sel) pane = h('div.list', empty({ art: 'inbox', title: 'Pick a conversation', body: 'Replies you send are remembered for that task and start work in the background.' }));
    else {
      const node = task.render({ id: sel, query: r.query, name: 'task' }, ctx);
      pane = h('div', { style: 'min-width:0' }, node);
      node.classList.remove('page');
      node.style.padding = '0';
    }
    return h('div.page', { style: 'max-width:1200px' },
      h('div.page-head', h('div', h('h1', 'Inbox'), h('div.sub', S.inbox.unread_total ? `${S.inbox.unread_total} unread` : 'Every conversation with Claude, in one place.')),
        h('div.actions', S.inbox.unread_total ? h('button.btn', { type: 'button', on: { click: markAll } }, icon('check'), 'Mark all read') : null)),
      threads.length ? h('div', { style: 'display:grid;grid-template-columns:minmax(260px,360px) minmax(0,1fr);gap:24px' }, list, pane)
        : empty({ art: 'inbox', title: 'No conversations yet', body: 'Open a task and send Claude a message. Replies land here.' }));
  },
};
