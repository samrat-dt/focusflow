export const MAX_TEXT = 380;

// The agent sees the id of this message in its inbox output, so it can name it in --reply-to.
export const REPLY_CUE = ' (via focusflow: reply with focusflow message user "<answer>" --reply-to <this message\'s id>)';
// A headless run has no bus message, hence no id to reply to.
export const HEADLESS_CUE = ' (via focusflow: reply with focusflow message user "<answer>". You were started headlessly; do the request, reply, and do not run focusflow done unless the task is complete)';

// focusflow takes an unknown flag such as `--help` for a name, so the text may not start with `-`.
export function checkText(input) {
  if (typeof input !== 'string') return { ok: false, error: 'text must be a string' };
  const text = input.trim();
  if (text === '') return { ok: false, error: 'Type a message first.' };
  if (text.length > MAX_TEXT) return { ok: false, error: `The message must be at most ${MAX_TEXT} characters.` };
  if (text.startsWith('-')) return { ok: false, error: 'The message must not start with "-".' };
  return { ok: true, text };
}

export const queueBody = text => text + REPLY_CUE;
export const headlessBody = text => text + HEADLESS_CUE;

// `focusflow message` prints the new id as [07cfae37].
export function parseFocusId(stdout) {
  const m = /\[([0-9a-f]{8})\]/.exec(String(stdout ?? ''));
  return m ? m[1] : null;
}
