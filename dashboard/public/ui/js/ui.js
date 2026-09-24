import { h, clear } from './dom.js';
import { icon } from './icons.js';
import { markdownBlocks, inlineTokens, friendlyError } from './logic.js';

// ---------- toasts ----------
let toastBox;
export function toast(text, { error = false, undo = null, ms } = {}) {
  toastBox ??= document.body.appendChild(h('div.toasts', { role: 'status', 'aria-live': 'polite' }));
  const close = () => { el.remove(); clearTimeout(timer); };
  const el = h('div.toast', { class: error ? 'error' : '' },
    h('span.grow', text),
    undo && h('button.btn.sm', { type: 'button', on: { click: () => { close(); undo(); } } }, icon('undo'), 'Undo'),
    h('button.btn.sm.icon', { type: 'button', 'aria-label': 'Dismiss', on: { click: close } }, icon('x')));
  toastBox.append(el);
  while (toastBox.children.length > 3) toastBox.firstChild.remove();
  const timer = setTimeout(close, ms ?? (error ? 12000 : undo ? 7000 : 4500));
  return close;
}
export const oops = (err, what = 'That did not work') => toast(`${what}. ${friendlyError(err)}`, { error: true });

// ---------- popover menu ----------
let openMenu = null;
export function closeMenu() { openMenu?.remove(); openMenu = null; }
// items: [{label, icon, run, danger, hint} | 'sep' | {header}]
export function menu(anchor, items) {
  closeMenu();
  const pop = h('div.menu-pop', { role: 'menu' }, items.filter(Boolean).map(it => {
    if (it === 'sep') return h('hr');
    if (it.header) return h('div.menu-h', it.header);
    return h('button', { type: 'button', role: 'menuitem', class: it.danger ? 'danger' : '', on: { click: () => { closeMenu(); it.run(); } } },
      it.icon && icon(it.icon), h('span.grow', it.label), it.hint && h('span.kbd', it.hint));
  }));
  document.body.append(pop);
  const r = anchor.getBoundingClientRect();
  const w = pop.offsetWidth;
  const top = r.bottom + 6 + pop.offsetHeight > innerHeight ? Math.max(8, r.top - pop.offsetHeight - 6) : r.bottom + 6;
  pop.style.top = `${top}px`;
  pop.style.left = `${Math.max(8, Math.min(innerWidth - w - 8, r.right - w))}px`;
  openMenu = pop;
  pop.querySelector('button')?.focus();
  pop.addEventListener('keydown', e => {
    const btns = [...pop.querySelectorAll('button')];
    const i = btns.indexOf(document.activeElement);
    if (e.key === 'ArrowDown') { e.preventDefault(); btns[(i + 1) % btns.length].focus(); }
    if (e.key === 'ArrowUp') { e.preventDefault(); btns[(i - 1 + btns.length) % btns.length].focus(); }
    if (e.key === 'Escape') { closeMenu(); anchor.focus(); }
  });
  return pop;
}
document.addEventListener('mousedown', e => { if (openMenu && !openMenu.contains(e.target)) closeMenu(); });
addEventListener('resize', closeMenu);
addEventListener('scroll', closeMenu, true);

// ---------- dialogs ----------
export function dialog(build, { className = '', label = 'Dialog' } = {}) {
  const prev = document.activeElement;
  const box = h('div.dialog', { class: className, role: 'dialog', 'aria-modal': 'true', 'aria-label': label });
  const overlay = h('div.overlay', box);
  const close = () => { overlay.remove(); document.removeEventListener('keydown', onKey, true); if (prev && document.contains(prev)) prev.focus(); };
  const onKey = e => {
    if (e.key === 'Escape') { e.stopPropagation(); close(); }
    if (e.key === 'Tab') {
      const f = [...box.querySelectorAll('button, input, textarea, select, a[href], [tabindex="0"]')].filter(n => !n.disabled && n.offsetParent !== null);
      if (!f.length) return;
      if (e.shiftKey && document.activeElement === f[0]) { e.preventDefault(); f.at(-1).focus(); }
      else if (!e.shiftKey && document.activeElement === f.at(-1)) { e.preventDefault(); f[0].focus(); }
    }
  };
  overlay.addEventListener('mousedown', e => { if (e.target === overlay) close(); });
  document.addEventListener('keydown', onKey, true);
  build(box, close);
  document.body.append(overlay);
  (box.querySelector('[autofocus]') ?? box.querySelector('input, textarea, button'))?.focus();
  return close;
}

export function confirmDialog({ title, body, confirm = 'Confirm', danger = false }) {
  return new Promise(resolve => {
    dialog((box, close) => {
      const done = v => { close(); resolve(v); };
      box.append(h('h2', title), h('p', { style: 'margin:0;color:var(--ink-2)' }, body),
        h('div.dialog-foot',
          h('button.btn', { type: 'button', on: { click: () => done(false) } }, 'Cancel'),
          h('button.btn.primary', { type: 'button', class: danger ? 'danger-fill' : '', autofocus: true, on: { click: () => done(true) } }, confirm)));
    }, { label: title });
  });
}

// ---------- markdown ----------
function inline(s) {
  return inlineTokens(s).map(t => t.t === 'b' ? h('strong', t.v) : t.t === 'i' ? h('em', t.v) : t.t === 'code' ? h('code', t.v)
    : t.t === 'a' ? h('a', { href: t.href, ...(t.href.startsWith('#') ? {} : { target: '_blank', rel: 'noreferrer' }) }, t.v) : t.v);
}
export function markdown(src, cls = 'prose') {
  const root = h(`div.${cls}`);
  for (const b of markdownBlocks(src)) {
    if (b.tag === 'pre') root.append(h('pre', b.text));
    else if (b.tag === 'hr') root.append(h('hr'));
    else if (b.tag === 'stamp') root.append(h('p.stamp', b.inline));
    else if (b.tag === 'ul' || b.tag === 'ol') root.append(h(b.tag, b.items.map(it => h('li', inline(it)))));
    else if (b.tag === 'table') root.append(h('table', h('thead', h('tr', b.head.map(c => h('th', inline(c))))), h('tbody', b.rows.map(r => h('tr', r.map(c => h('td', inline(c))))))));
    else root.append(h(b.tag, inline(b.inline)));
  }
  return root;
}

export function empty({ art = 'sparkle', title, body, actions = [] }) {
  return h('div.empty', h('div.art', icon(art)), h('h2', title), body && h('p', body), actions.length ? h('div.row', { style: 'justify-content:center' }, actions) : null);
}

export function btn(label, { icon: ic, cls = '', run, title, disabled } = {}) {
  return h(`button.btn${cls ? '.' + cls.split(' ').join('.') : ''}`, { type: 'button', title, disabled, 'aria-label': !label ? title : undefined, on: { click: e => { e.stopPropagation(); run?.(e); } } }, ic && icon(ic), label);
}

export function setBusy(button, busy, text) {
  if (!button) return;
  if (busy) { button.dataset.label = button.textContent; button.disabled = true; if (text) button.lastChild.textContent = text; }
  else { button.disabled = false; }
}

export { clear };
