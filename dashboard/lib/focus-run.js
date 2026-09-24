import { execFile } from 'node:child_process';

// Runs focusflow with an argv array (never a shell). On failure the error message is
// focusflow's own stderr, unchanged, so the UI can relay it as is. No retries.
export function runFocus(args, { focusBin, env, timeoutMs = 30000 }) {
  return new Promise((resolve, reject) => {
    execFile(focusBin, args, { env, timeout: timeoutMs, maxBuffer: 8 * 1024 * 1024 }, (err, stdout, stderr) => {
      if (!err) return resolve({ stdout, stderr });
      let message;
      if (err.code === 'ENOENT') message = `cannot run focusflow (${focusBin}): not found`;
      else if (err.killed && err.signal) message = `focusflow timed out after ${Math.round(timeoutMs / 1000)}s`;
      else message = (stderr || '').trim() || (stdout || '').trim() || err.message;
      const e = new Error(message);
      e.stderr = stderr;
      e.stdout = stdout;
      reject(e);
    });
  });
}
