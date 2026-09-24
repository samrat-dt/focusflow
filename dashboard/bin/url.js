import { execFileSync } from 'node:child_process';
import { loadConfig } from '../lib/config.js';

// Prints the local URL with the token in the fragment (never sent to the server
// or logged). With --open it also opens it in the default browser (macOS).
const c = loadConfig();
const url = `http://127.0.0.1:${c.tunables.port}/#token=${c.token}`;
console.log(url);
if (process.argv.includes('--open')) execFileSync('open', [url]);
