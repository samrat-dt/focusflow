import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';

export const LABEL = 'local.focusflow.dashboard';

export const agentPath = (home = os.homedir()) => path.join(home, 'Library', 'LaunchAgents', `${LABEL}.plist`);

const esc = v => String(v)
  .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;').replace(/'/g, '&apos;');

// Runs server.js at login and restarts it if it dies (at most every 30 seconds).
export function buildPlist({ label, nodePath, serverPath, workDir, logPath, env }) {
  const vars = Object.entries(env).map(([k, v]) => `    <key>${esc(k)}</key>\n    <string>${esc(v)}</string>`).join('\n');
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${esc(label)}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${esc(nodePath)}</string>
    <string>${esc(serverPath)}</string>
  </array>
  <key>WorkingDirectory</key>
  <string>${esc(workDir)}</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>30</integer>
  <key>StandardOutPath</key>
  <string>${esc(logPath)}</string>
  <key>StandardErrorPath</key>
  <string>${esc(logPath)}</string>
  <key>EnvironmentVariables</key>
  <dict>
${vars}
  </dict>
</dict>
</plist>
`;
}

const defaultRun = (cmd, args) => new Promise((resolve, reject) => {
  execFile(cmd, args, (err, stdout, stderr) => {
    if (!err) return resolve({ stdout, stderr });
    const e = new Error((stderr || '').trim() || err.message);
    reject(e);
  });
});

// Idempotent: replaces and reloads an existing job.
export async function installAgent({ plist, file, label, uid, run = defaultRun }) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, plist);
  await run('launchctl', ['bootout', `gui/${uid}/${label}`]).catch(() => {}); // not loaded yet is fine
  await run('launchctl', ['bootstrap', `gui/${uid}`, file]);
}

export async function uninstallAgent({ file, label, uid, run = defaultRun }) {
  await run('launchctl', ['bootout', `gui/${uid}/${label}`]).catch(() => {});
  fs.rmSync(file, { force: true });
}
