import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  APP_NAME, LAUNCHER_FILE, HELP_PAGE_FILE, PROJECT_PLACEHOLDER,
  shQuote, helpContent, renderText, renderMarkdown, renderCaseHtml, renderHelpPage, readmeBlock, README_START, README_END,
} from '../lib/help.js';
import { LABEL } from '../lib/launchd.js';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const DIR = '/Users/someone/code/focusflow';
const commands = c => [...c.token.steps, ...c.server.steps].map(s => s.command).filter(Boolean);

test('shQuote leaves plain paths alone and single-quotes anything the shell would split or expand', () => {
  assert.equal(shQuote('/Users/a/code/x'), '/Users/a/code/x');
  assert.equal(shQuote('/Users/a/My Code/x'), "'/Users/a/My Code/x'");
  assert.equal(shQuote("/Users/a/it's"), "'/Users/a/it'\\''s'");
  assert.equal(shQuote('/Users/a/$HOME'), "'/Users/a/$HOME'");
});

test('the access-key steps: open Terminal, run focusflow dashboard open, press Enter', () => {
  const c = helpContent({ projectDir: DIR, port: 4177 });
  assert.match(c.token.heading, /access key/i);
  assert.equal(c.token.steps.length, 3);
  assert.match(c.token.steps[0].text, /Terminal/);
  assert.equal(c.token.steps[1].command, 'focusflow dashboard open');
  assert.match(c.token.steps[2].text, /remembers/);
});

test('the server steps: start it, reload the right address, and a fallback for no login item', () => {
  const c = helpContent({ projectDir: DIR, port: 4200 });
  assert.match(c.server.heading, /not running/);
  assert.equal(c.server.steps[1].command, 'focusflow dashboard start');
  assert.match(c.server.steps[2].text, /127\.0\.0\.1:4200/);
  assert.equal(c.server.fallback.command, `cd ${DIR} && npm start`);
});

test('a folder with a space is quoted in every command, and no command hard-codes a path', () => {
  const c = helpContent({ projectDir: '/Users/a/My Code/cc' });
  assert.equal(c.server.fallback.command, "cd '/Users/a/My Code/cc' && npm start");
  const other = helpContent({ projectDir: '/somewhere/else' });
  assert.ok([...commands(other), other.server.fallback.command].some(x => x.includes('/somewhere/else')));
  assert.ok(commands(other).every(x => !x.includes('/Users/')), 'a path from another machine leaked into the commands');
});

test('with no folder given (README) the commands use a placeholder, unquoted', () => {
  const c = helpContent({});
  assert.equal(c.server.fallback.command, `cd ${PROJECT_PLACEHOLDER} && npm start`);
});

test('the launcher is offered as the one-click way, and the Gatekeeper warning is explained', () => {
  const c = helpContent({ projectDir: DIR });
  assert.ok(c.launcher.text.includes(LAUNCHER_FILE));
  assert.match(c.launcher.text, /starts Focusflow/);
  assert.match(c.launcher.gatekeeper, /right-click/i);
});

test('the same words reach every surface: text, markdown and both html renderers', () => {
  const c = helpContent({ projectDir: DIR });
  const pieces = [c.token.heading, c.token.what, c.server.heading, c.server.what, c.token.steps[0].text, c.token.steps[2].text, c.server.steps[2].text, c.launcher.text, c.launcher.gatekeeper, c.server.fallback.text];
  const unescape = s => s.replace(/&#39;/g, "'").replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
  const text = renderText(c);
  const md = renderMarkdown(c);
  const page = unescape(renderHelpPage(c));
  const frag = unescape(renderCaseHtml(c.token) + renderCaseHtml(c.server));
  for (const p of pieces) {
    assert.ok(text.includes(p), `text is missing: ${p}`);
    assert.ok(md.includes(p), `markdown is missing: ${p}`);
    assert.ok(page.includes(p), `help page is missing: ${p}`);
  }
  for (const p of [c.token.heading, c.token.what, c.token.steps[0].text, c.server.what, c.server.steps[2].text]) assert.ok(frag.includes(p), `fragment is missing: ${p}`);
  for (const cmd of commands(c)) {
    assert.ok(text.includes(cmd) && md.includes(cmd) && page.includes(cmd), `command missing somewhere: ${cmd}`);
  }
});

test('the html fragment numbers the steps and gives every command a Copy button carrying the exact command', () => {
  const c = helpContent({ projectDir: DIR });
  const html = renderCaseHtml(c.token);
  assert.match(html, /<ol class="help-steps">/);
  assert.equal((html.match(/<li>/g) || []).length, 3);
  assert.equal((html.match(/class="help-copy"/g) || []).length, 1);
  assert.ok(html.includes('data-copy="focusflow dashboard open"'));
  assert.equal((renderCaseHtml(c.server).match(/class="help-copy"/g) || []).length, 2, 'main command plus the fallback command');
});

test('html output escapes a hostile folder name', () => {
  const evil = `/tmp/"><img src=x onerror=alert(1)>`;
  const c = helpContent({ projectDir: evil });
  for (const html of [renderCaseHtml(c.token), renderCaseHtml(c.server), renderHelpPage(c)]) {
    assert.ok(!html.includes('<img'), 'unescaped markup in html');
    assert.ok(!/data-copy="[^"]*"[^>]*onerror/.test(html));
  }
});

test('the help page is self-contained: inline style and script only, nothing fetched from anywhere', () => {
  const page = renderHelpPage(helpContent({ projectDir: DIR }));
  assert.match(page, /^<!doctype html>/i);
  assert.match(page, new RegExp(`<title>${APP_NAME} Help</title>`));
  assert.ok(!/<link\b/i.test(page), 'no <link>');
  assert.ok(!/<script[^>]*\ssrc=/i.test(page), 'no external script');
  assert.ok(!/<(img|iframe|video|audio|source|object|embed)\b/i.test(page), 'no embedded resources');
  assert.ok(!/@import|url\(/i.test(page), 'no css imports or urls');
  assert.ok(!/\s(src|srcset)=/i.test(page), 'nothing loaded by attribute');
  for (const [, href] of page.matchAll(/\shref="([^"]*)"/g)) assert.match(href, /^http:\/\/127\.0\.0\.1:\d+\/$/, `unexpected link ${href}`);
  assert.match(page, /<style>[\s\S]+<\/style>/);
  assert.match(page, /<script>[\s\S]+<\/script>/);
  assert.match(page, /prefers-color-scheme: dark/);
});

test('the help page says up front that it works without the dashboard, and carries both cases and the launcher', () => {
  const page = renderHelpPage(helpContent({ projectDir: DIR }));
  assert.match(page, /works even when Focusflow is not running/);
  assert.match(page, /id="token"/);
  assert.match(page, /id="server"/);
  assert.ok(page.includes(LAUNCHER_FILE));
  assert.ok(HELP_PAGE_FILE.endsWith('.html'));
});

test('the README carries the same steps, generated from this source', () => {
  const readme = fs.readFileSync(path.join(ROOT, 'README.md'), 'utf8');
  const start = readme.indexOf(README_START);
  const end = readme.indexOf(README_END);
  assert.ok(start >= 0 && end > start, 'README needs the generated markers');
  assert.equal(readme.slice(start, end + README_END.length), readmeBlock(), 'README steps are out of date: run `npm run docs`');
});

test('the text form (setup output) is plain: numbered steps, indented commands, no markdown fences', () => {
  const t = renderText(helpContent({ projectDir: DIR }));
  assert.ok(!t.includes('```'));
  assert.match(t, /^ {2}1\. Open Terminal/m);
  assert.match(t, /^ {6}focusflow dashboard open$/m);
});

test('the text form keeps a blank line between the steps, the fallback and the next case', () => {
  const t = renderText(helpContent({ projectDir: DIR }));
  assert.match(t, /Focusflow opens with its key and remembers it\. You will not need to do this again\.\n\nFocusflow is not running/);
  assert.match(t, /open 127\.0\.0\.1:4177\)\.\n\n {2}If that says/);
});

test('the recovery wording talks about Focusflow, never a dashboard or a particular browser', () => {
  const c = helpContent({ projectDir: DIR });
  const words = [c.token.heading, c.token.what, c.server.heading, c.server.what, c.server.stopped.heading, c.server.stopped.what,
    ...c.token.steps.map(s => s.text), ...c.server.steps.map(s => s.text), c.launcher.text];
  for (const w of words) assert.ok(!/dashboard|chrome/i.test(w), `mentions a dashboard or Chrome: ${w}`);
});
