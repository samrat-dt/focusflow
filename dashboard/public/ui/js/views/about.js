import { h } from '../dom.js';
import { icon } from '../icons.js';
import { markdown } from '../ui.js';
import { ABOUT, HELP } from '../content.js';

function docs(active) {
  const all = [...ABOUT.map(a => ({ ...a, path: a.id === 'about' ? '/about' : `/about/${a.id}` })), ...HELP.map(a => ({ ...a, path: `/help/${a.id}` }))];
  const page = all.find(a => a.id === active) ?? all[0];
  const link = a => h('a.nav-item', { href: `#${a.path}`, 'aria-current': a.id === page.id ? 'page' : undefined }, h('span.label', a.title));
  const groups = [...new Set(HELP.map(a => a.group))];
  const idx = all.indexOf(page);
  const next = all[idx + 1];
  return h('div.page', { style: 'max-width:1100px' },
    h('div.docs',
      h('nav.docs-nav', { 'aria-label': 'About and Help' },
        h('div.nav-h', 'About'), ABOUT.map(a => link({ ...a, path: a.id === 'about' ? '/about' : `/about/${a.id}` })),
        groups.map(g => [h('div.nav-h', g), HELP.filter(a => a.group === g).map(a => link({ ...a, path: `/help/${a.id}` }))])),
      h('article', markdown(page.body),
        next && h('a.tile', { href: `#${next.path}`, style: 'margin-top:48px;min-height:0;flex-direction:row;align-items:center;text-decoration:none;max-width:720px' },
          h('div.grow', h('div', { style: 'color:var(--muted);font-size:13px' }, 'Next'), h('div', { style: 'font-weight:600;color:var(--ink)' }, next.title)), icon('chevronRight')))));
}

export const about = { title: r => (ABOUT.find(a => a.id === r.id)?.title ?? 'About Focusflow'), deps: r => [r.id], render: r => docs(r.id ?? 'about') };
export const help = { title: r => (HELP.find(a => a.id === r.id)?.title ?? 'Help'), deps: r => [r.id], render: r => docs(r.id ?? 'getting-started') };
