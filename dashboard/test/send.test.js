import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MAX_TEXT, REPLY_CUE, HEADLESS_CUE, checkText, queueBody, headlessBody, parseFocusId } from '../lib/send.js';

test('the reply cue is appended exactly as the spec words it', () => {
  assert.equal(queueBody('Please rebase'), 'Please rebase (via focusflow: reply with focusflow message user "<answer>" --reply-to <this message\'s id>)');
});

test('the longest allowed message stays under focusflow\'s 500 character cap', () => {
  assert.equal(MAX_TEXT, 380);
  assert.ok(queueBody('x'.repeat(MAX_TEXT)).length <= 500);
  assert.ok(REPLY_CUE.length <= 100);
});

test('the headless cue asks for a reply and warns against focusflow done, and has no reply-to (there is no bus id)', () => {
  const b = headlessBody('Run the tests');
  assert.ok(b.startsWith('Run the tests (via focusflow: reply with focusflow message user "<answer>"'));
  assert.match(b, /started headlessly/);
  assert.match(b, /do not run focusflow done unless the task is complete/);
  assert.ok(!b.includes('--reply-to'));
  assert.ok(HEADLESS_CUE.length > 0);
});

test('checkText trims and accepts normal text', () => {
  assert.deepEqual(checkText('  hello there \n'), { ok: true, text: 'hello there' });
  assert.deepEqual(checkText('x'.repeat(380)), { ok: true, text: 'x'.repeat(380) });
});

test('checkText rejects non-strings, empty, too long, and anything focusflow would take for a flag', () => {
  for (const bad of [undefined, null, 5, {}, ['a'], '', '   \n ', 'x'.repeat(381), '-h', '--help', '  --slug=x', '-']) {
    const r = checkText(bad);
    assert.equal(r.ok, false, JSON.stringify(bad));
    assert.equal(typeof r.error, 'string');
  }
  assert.match(checkText('--help').error, /start with/);
  assert.match(checkText('x'.repeat(381)).error, /380/);
});

test('parseFocusId reads the id focusflow prints', () => {
  assert.equal(parseFocusId('[07cfae37]\n'), '07cfae37');
  assert.equal(parseFocusId('sent [07cfae37] to user/task-a'), '07cfae37');
  assert.equal(parseFocusId('sent'), null);
  assert.equal(parseFocusId(''), null);
  assert.equal(parseFocusId('[NOTHEX!!]'), null);
});
