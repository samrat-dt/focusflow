import { execFile } from 'node:child_process';

// "Proof of work" for a task that is ready for review: what Claude said it did, the latest progress
// note, and what changed in the project folder since the task started. Every part is optional: a
// folder that is not a git repository simply has no commits to show.

const run = (cmd, args, cwd, timeout = 4000) => new Promise(resolve => {
  execFile(cmd, args, { cwd, timeout, maxBuffer: 512 * 1024 }, (err, stdout) => resolve(err ? null : stdout));
});

export async function gitSince({ workDir, since, exec = run }) {
  if (!workDir) return null;
  const inside = await exec('git', ['rev-parse', '--is-inside-work-tree'], workDir);
  if (!inside || inside.trim() !== 'true') return null;
  const branch = ((await exec('git', ['branch', '--show-current'], workDir)) ?? '').trim() || null;
  const sinceArg = since && !Number.isNaN(Date.parse(since)) ? [`--since=${new Date(since).toISOString()}`] : ['-n', '10'];
  const log = (await exec('git', ['log', ...sinceArg, '-n', '25', '--pretty=format:%h%x09%s%x09%cI'], workDir)) ?? '';
  const commits = log.split('\n').filter(Boolean).map(l => { const [hash, subject, at] = l.split('\t'); return { hash, subject, at }; });
  const committedFiles = commits.length
    ? ((await exec('git', ['log', ...sinceArg, '-n', '25', '--name-only', '--pretty=format:'], workDir)) ?? '').split('\n').filter(Boolean)
    : [];
  const status = ((await exec('git', ['status', '--porcelain'], workDir)) ?? '').split('\n').filter(Boolean).map(l => l.slice(3));
  const files = [...new Set([...status, ...committedFiles])].slice(0, 40);
  return { branch, commits, files, uncommitted: status.length };
}

// The latest thing the agent told the human, without the delivery cue.
export function latestSummary(messages) {
  const last = [...messages].reverse().find(m => m.direction === 'in');
  return last ? { text: String(last.body).replace(/\s*\(via focusflow:[\s\S]*\)\s*$/, '').trim(), at: last.at } : null;
}
