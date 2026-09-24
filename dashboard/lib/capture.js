export const MAX_NAME = 200;
export const MAX_NOTE = 2000;

export class CaptureError extends Error {}

// focusflow treats an unknown flag such as --help as a task name and creates a real
// task, so a name that starts with "-" must never reach it.
export function validateName(input) {
  if (typeof input !== 'string' || input.trim() === '') throw new CaptureError('name is required');
  const name = input.trim();
  if (name.startsWith('-')) throw new CaptureError('name must not start with "-"');
  if (name.length > MAX_NAME) throw new CaptureError(`name must be at most ${MAX_NAME} characters`);
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(name)) throw new CaptureError('name must not contain control characters or line breaks');
  return name;
}

// Kebab-case of the first six words; ASCII only.
export function slugify(name, fallback = 'task') {
  const words = name
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)
    .slice(0, 6);
  const slug = words.join('-').slice(0, 64).replace(/-+$/, '');
  return slug || fallback;
}

export function uniqueSlug(base, taken) {
  if (!taken.has(base)) return base;
  for (let n = 2; ; n++) {
    const candidate = `${base}-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}

// focusflow's thin task brief (skill reference: templates.md). The bootstrap session
// detects the Deferred markers and asks for those sections when the task opens.
export function thinBrief({ name, note, workDir }) {
  const what = note && note.trim() ? note.trim() : name;
  return `# ${name}

## What
${what}

## Why
*Deferred — fill in at task start.*

## Where
work_dir: ${workDir}

## Done when
*Deferred — fill in at task start.*

## Out of scope
*Deferred*

## Open questions
*Deferred*

---
*This brief is thin. Before you start substantive work, the bootstrap
session will prompt you to fill in the deferred sections.*
`;
}

// The placeholder `focusflow add task` writes. Anything else has real content and is
// never overwritten.
export function isStubBrief(text, name) {
  return typeof text === 'string' &&
    text.startsWith(`# ${name}\n`) &&
    text.includes('Edit this brief freely via `focusflow edit`') &&
    !/^## /m.test(text);
}
