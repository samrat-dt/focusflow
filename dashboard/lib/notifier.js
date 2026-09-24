import { execFile } from 'node:child_process';

const flat = s => String(s).replace(/\s+/g, ' ').trim();

// Text going into an AppleScript string literal. Backslash first, then the quote;
// control characters (newlines, tabs, NUL) become spaces so nothing can end the literal early.
export function appleScriptQuote(s) {
  const clean = String(s).replace(/[\u0000-\u001f\u007f]/g, ' ').replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  return `"${clean}"`;
}

// One arrival: its task's name and message. Several in one poll: one collapsed banner.
export function bannerFor(arrivals) {
  const urgent = arrivals.some(a => a.urgent);
  if (arrivals.length === 1) {
    const [a] = arrivals;
    return { title: a.name, subtitle: urgent ? 'urgent' : null, body: flat(a.body).slice(0, 120) };
  }
  const tasks = new Set(arrivals.map(a => a.slug)).size;
  return {
    title: 'Focusflow',
    subtitle: urgent ? 'urgent' : null,
    body: `${arrivals.length} new messages in ${tasks} task${tasks === 1 ? '' : 's'}`,
  };
}

export function osascriptArgs({ title, subtitle, body }) {
  const script = `display notification ${appleScriptQuote(body)} with title ${appleScriptQuote(title)}` +
    (subtitle ? ` subtitle ${appleScriptQuote(subtitle)}` : '');
  return ['-e', script];
}

const defaultRun = argv => new Promise((resolve, reject) => {
  execFile('osascript', argv, { timeout: 10000 }, (err, _stdout, stderr) => {
    if (err) reject(new Error((stderr || '').trim() || err.message));
    else resolve();
  });
});

// No click-through: osascript banners open Script Editor when clicked, and the
// app takes no dependency such as terminal-notifier. A failure shows in the footer.
export function createNotifier({ enabled = true, run = defaultRun } = {}) {
  let lastError = null;
  return {
    async notify(arrivals) {
      if (!enabled || !arrivals.length) return;
      try {
        await run(osascriptArgs(bannerFor(arrivals)));
        lastError = null;
      } catch (err) {
        lastError = err.message;
      }
    },
    status: () => ({ last_error: lastError }),
  };
}
