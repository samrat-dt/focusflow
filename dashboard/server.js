import { loadConfig } from './lib/config.js';
import { rebuild } from './lib/store.js';
import { readTasks } from './lib/focus-reader.js';
import { ensureInstance } from './lib/snapshotter.js';
import { createApp } from './lib/server.js';

const config = loadConfig();
const t = config.tunables;

// The cache is disposable: always rebuild from the event log at start.
const { store, eventCount, malformed } = rebuild({ dbPath: config.cacheDb, dataDir: config.dataDir });
if (malformed.length) console.warn(`skipped ${malformed.length} malformed event line(s), see /api/health`);

const app = createApp({
  config, store, malformed: malformed.length,
  readTasks: () => readTasks({ focusBin: config.focusBin, focusRoot: config.focusRoot, dbName: config.focusDbName }),
});

ensureInstance({ store, dataDir: config.dataDir, device: config.device });
const first = app.refresh();
if (first.skipped) console.warn(`first focusflow reading failed: ${first.error}`);

const timer = setInterval(() => {
  const r = app.refresh();
  if (r.skipped && r.error) console.warn(`focusflow reading failed: ${r.error}`);
}, t.tick_minutes * 60 * 1000);

// Read-only poll of the user queue. Warn once per distinct error, not every few seconds.
let lastInboxWarn = null;
const inboxTick = async () => {
  const r = await app.pollInbox();
  if (r.error && r.error !== lastInboxWarn) console.warn(`inbox poll failed: ${r.error}`);
  lastInboxWarn = r.error ?? null;
};
inboxTick();
const inboxTimer = setInterval(inboxTick, t.inbox_poll_seconds * 1000);

app.server.on('error', err => {
  console.error(err.code === 'EADDRINUSE'
    ? `port ${t.port} is already in use (change tunables.port in ${config.home}/instance.json)`
    : err.message);
  process.exit(1);
});
app.server.listen(t.port, '127.0.0.1', () => {
  const url = `http://127.0.0.1:${t.port}/`;
  console.log(`focusflow on ${url} (device ${config.device}, ${eventCount} events replayed)`);
  console.log(process.stdout.isTTY ? `open: ${url}#token=${config.token}` : 'run `npm run url` to get the link with your token');
});

for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, async () => {
    clearInterval(timer);
    clearInterval(inboxTimer);
    app.server.close();
    store.close();
    process.exit(0);
  });
}
