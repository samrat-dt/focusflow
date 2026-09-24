import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateName, slugify, uniqueSlug, thinBrief, isStubBrief, CaptureError, MAX_NAME, MAX_NOTE } from '../lib/capture.js';

const rejects = (name, re) => assert.throws(() => validateName(name), e => e instanceof CaptureError && re.test(e.message), JSON.stringify(name));

test('validateName trims and accepts ordinary names', () => {
  assert.equal(validateName('  Write the Q3 report  '), 'Write the Q3 report');
  assert.equal(validateName('a'.repeat(MAX_NAME)), 'a'.repeat(MAX_NAME));
  assert.equal(validateName('Fix bug - urgent'), 'Fix bug - urgent');
});

test('validateName rejects names starting with "-" (focusflow would treat them as flags)', () => {
  for (const n of ['-x', '--help', '  --help', '-', '--', '- x', '-h', '--slug=foo']) rejects(n, /start with/);
});

test('validateName rejects empty, non-string, too long and control characters', () => {
  rejects('', /required/);
  rejects('   ', /required/);
  rejects(undefined, /required/);
  rejects(42, /required/);
  rejects('a'.repeat(MAX_NAME + 1), /at most/);
  rejects('line1\nline2', /control/);
  rejects('tab\there', /control/);
  rejects('nul\u0000byte', /control/);
});

test('slugify: kebab-case of the first six words, ASCII only', () => {
  assert.equal(slugify('Write the Q3 report for finance today please'), 'write-the-q3-report-for-finance');
  assert.equal(slugify('Café résumé!!'), 'cafe-resume');
  assert.equal(slugify('Fix  bug #42 (urgent)'), 'fix-bug-42-urgent');
  assert.equal(slugify("It's Sam's"), 'it-s-sam-s');
});

test('slugify falls back for names with no ASCII letters or digits, and caps length', () => {
  assert.equal(slugify('日本語'), 'task');
  assert.equal(slugify('!!!'), 'task');
  const s = slugify('a'.repeat(100));
  assert.ok(s.length <= 64);
  assert.match(s, /^[a-z0-9][a-z0-9-]{0,127}$/);
  assert.doesNotMatch(slugify(`${'a'.repeat(63)} bbb`), /-$/);
});

test('uniqueSlug appends -2, -3 on collision', () => {
  assert.equal(uniqueSlug('report', new Set()), 'report');
  assert.equal(uniqueSlug('report', new Set(['report'])), 'report-2');
  assert.equal(uniqueSlug('report', new Set(['report', 'report-2'])), 'report-3');
  assert.equal(uniqueSlug('report', new Set(['report', 'report-3'])), 'report-2');
});

test('thinBrief matches focusflow\'s thin template with the deferred markers intact', () => {
  const b = thinBrief({ name: 'Write report', note: 'Quarterly numbers for finance', workDir: '/w/x' });
  assert.equal(b, `# Write report

## What
Quarterly numbers for finance

## Why
*Deferred — fill in at task start.*

## Where
work_dir: /w/x

## Done when
*Deferred — fill in at task start.*

## Out of scope
*Deferred*

## Open questions
*Deferred*

---
*This brief is thin. Before you start substantive work, the bootstrap
session will prompt you to fill in the deferred sections.*
`);
});

test('thinBrief uses the name as What when there is no note', () => {
  assert.match(thinBrief({ name: 'Write report', note: '', workDir: '/w' }), /## What\nWrite report\n/);
  assert.match(thinBrief({ name: 'Write report', workDir: '/w' }), /## What\nWrite report\n/);
});

test('isStubBrief recognises focusflow\'s stub and nothing with real content', () => {
  const stub = '# Try it\n\nEdit this brief freely via `focusflow edit` or by composing a focusflow skill\nsession. Sections to cover: What / Why / Where / Done when / Out of scope /\nOpen questions.\n';
  assert.equal(isStubBrief(stub, 'Try it'), true);
  assert.equal(isStubBrief(stub, 'Other name'), false);
  assert.equal(isStubBrief(stub + '\n## What\nreal content\n', 'Try it'), false);
  assert.equal(isStubBrief('# Try it\n\nMy own notes.\n', 'Try it'), false);
  assert.equal(isStubBrief('', 'Try it'), false);
});

test('limits are exported', () => {
  assert.equal(MAX_NAME, 200);
  assert.equal(MAX_NOTE, 2000);
});

test('slugify falls back to the given word when the name has no ASCII letters or digits', () => {
  assert.equal(slugify('日本語'), 'task');
  assert.equal(slugify('日本語', 'project'), 'project');
  assert.equal(slugify('Billing rewrite', 'project'), 'billing-rewrite');
});
