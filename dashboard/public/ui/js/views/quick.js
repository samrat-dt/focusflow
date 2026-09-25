import { h } from '../dom.js';
import { icon } from '../icons.js';
import { S, capture } from '../state.js';
import { parseQuickAdd } from '../logic.js';

// The quick-capture window (the Mac app's global hotkey): one line, Enter to add, Esc to close.
const closeWindow = () => { try { window.webkit?.messageHandlers?.focusflow?.postMessage('close'); } catch { /* not in the app */ } };
addEventListener('keydown', e => { if (e.key === 'Escape' && document.documentElement.classList.contains('quick')) closeWindow(); });

let status = null; // { text, error } shown under the field; kept across redraws

export const quick = {
  title: () => 'New task',
  deps: () => [S.projectList.length, status],
  render(r, ctx) {
    const input = h('input.input', { placeholder: 'New task…  #project !high', maxlength: '200', autofocus: true, 'aria-label': 'New task', dataset: { key: 'quick' }, style: 'font-size:18px;min-height:52px;border:0;box-shadow:none;background:transparent' });
    const hint = h('div', { style: `font-size:12px;padding:0 14px 10px;color:${status ? (status.error ? 'var(--danger)' : 'var(--accent)') : 'var(--muted)'}` }, status?.text ?? 'Enter adds it to Up next · Shift+Enter to the backlog · Esc closes');
    input.addEventListener('keydown', async e => {
      if (e.key !== 'Enter') return;
      const p = parseQuickAdd(input.value, S.projectList.map(x => x.slug));
      if (!p.name) return;
      input.disabled = true;
      try {
        input.value = '';
        await capture({ name: p.name, project: p.project, priority: p.priority ?? 'medium', stage: e.shiftKey ? 'backlog' : 'next' });
        status = { text: `Added “${p.name}”${e.shiftKey ? ' to the backlog' : ' to Up next'}.` };
        setTimeout(() => { status = null; closeWindow(); ctx.refresh(); }, 900);
      } catch (err) {
        status = { text: err.message, error: true };
      }
      ctx.refresh();
    });
    setTimeout(() => input.focus(), 30);
    return h('div', h('div.row', { style: 'padding:6px 8px 0 16px;color:var(--accent)' }, icon('plus', 'i-lg'), input), hint);
  },
};
