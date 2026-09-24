import path from 'node:path';
import readline from 'node:readline/promises';
import { fileURLToPath } from 'node:url';
import { runSetup, agentSpec, SetupError } from '../lib/setup.js';
import { buildPlist, installAgent, uninstallAgent, agentPath, LABEL } from '../lib/launchd.js';
import { helpContent, renderText } from '../lib/help.js';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const args = new Set(process.argv.slice(2));

async function confirm(question) {
  if (!process.stdin.isTTY) return false;
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  const a = (await rl.question(`${question} [y/N] `)).trim().toLowerCase();
  rl.close();
  return a === 'y' || a === 'yes';
}

try {
  if (args.has('--uninstall-launchd')) {
    await uninstallAgent({ file: agentPath(), label: LABEL, uid: process.getuid() });
    console.log('login item removed (data and config are untouched)');
    process.exit(0);
  }

  const r = await runSetup({ repoRoot });
  console.log(`focusflow            ${r.focusVersion}`);
  console.log(`device          ${r.device}${r.instanceCreated ? ' (new)' : ''}`);
  console.log(`config          ${r.home}${r.tokenCreated ? ' (token created)' : ''}`);
  console.log(`data            ${r.dataDir}`);
  console.log(`tasks           ${r.tasks}${r.newSnapshot ? ', first snapshot recorded' : ''}`);
  console.log('backup          `focusflow backup` covers this data (it lives under ~/.focusflow); `focusflow sync` mirrors it to git');

  if (process.platform !== 'darwin') {
    console.log('login item      skipped (macOS only)');
  } else {
    const install = args.has('--no-launchd') ? false
      : args.has('--launchd') || args.has('--yes') ? true
      : await confirm(`Install the login item (${agentPath()}) so the app starts at login?`);
    if (install) {
      const spec = agentSpec({ config: r.config, repoRoot });
      await installAgent({ plist: buildPlist(spec), file: agentPath(), label: LABEL, uid: process.getuid() });
      console.log(`login item      installed and started (log: ${spec.logPath})`);
      console.log(`                Terminal.app and Warp need Accessibility for: ${spec.nodePath}`);
      console.log('                (System Settings > Privacy & Security > Accessibility)');
    } else {
      console.log('login item      not installed (run with --launchd to install)');
    }
  }

  console.log('app             `npm run app` builds Focusflow.app (window + always-on-top mini board) in ~/Applications');
  console.log(`open            ${r.url}  (npm run url prints the link with your token)`);

  console.log('\nIf something goes wrong, these steps are also on screen in the app and in the help page:\n');
  console.log(renderText(helpContent({ projectDir: repoRoot, port: r.config.tunables.port })));
} catch (err) {
  if (err instanceof SetupError) {
    console.error(`setup failed: ${err.message}`);
    process.exit(1);
  }
  throw err;
}
