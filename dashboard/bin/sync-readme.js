import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { readmeBlock, README_START, README_END } from '../lib/help.js';

// Rewrites the generated help block in README.md from lib/help.js (the one
// source of the recovery steps). `--check` only reports whether it is current.
const file = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'README.md');
const text = fs.readFileSync(file, 'utf8');
const start = text.indexOf(README_START);
const end = text.indexOf(README_END);
if (start < 0 || end < start) {
  console.error('README.md has no generated help block (missing markers)');
  process.exit(1);
}
const next = text.slice(0, start) + readmeBlock() + text.slice(end + README_END.length);
if (process.argv.includes('--check')) {
  console.log(next === text ? 'README help block is current' : 'README help block is out of date: run `npm run docs`');
  process.exit(next === text ? 0 : 1);
}
fs.writeFileSync(file, next);
console.log(next === text ? 'README help block already current' : 'README help block updated');
