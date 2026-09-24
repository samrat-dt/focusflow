'use strict';

// On-screen recovery help: a full-screen panel when this browser has no access token (or the
// server rejects it), and a banner while the server does not answer. The wording is not in this
// file. The server renders it from lib/help.js and embeds it in the page (#help-data), so it is
// already in memory when the server goes away and the page can no longer ask for it.
const Help = (() => {
  const $ = id => document.getElementById(id);
  const NO_STEPS = 'The recovery steps could not be loaded. Open the Help file that setup put next to the launcher (setup printed where).';

  let steps = null;
  try { steps = JSON.parse($('help-data').textContent).html; } catch { steps = null; }

  let handlers = { onRecovered() {}, onRetry() {} };
  let lost = false;
  let panelUp = false;

  // `helped` tells callers this failure is already on screen, so they need not show it again.
  function coded(code, message) {
    const err = new Error(message);
    err.code = code;
    err.helped = true;
    return err;
  }

  // `steps` is html the server escaped and built from its own config, never from user input.
  function paint(box, key) {
    if (steps) box.innerHTML = steps[key] + steps.launcher;
    else box.textContent = NO_STEPS;
  }

  function showTokenPanel() {
    panelUp = true;
    const panel = $('token-panel');
    if (!panel.hidden) return;
    const body = $('token-panel-body');
    paint(body, 'token');
    const actions = document.createElement('div');
    actions.className = 'token-panel-actions';
    const retry = document.createElement('button');
    retry.setAttribute('type', 'button');
    retry.className = 'primary';
    retry.textContent = 'I ran it, check again';
    retry.addEventListener('click', () => handlers.onRetry());
    actions.append(retry);
    body.append(actions);
    panel.hidden = false;
    retry.focus();
  }

  function hideTokenPanel() {
    panelUp = false;
    $('token-panel').hidden = true;
  }

  function setLost(on) {
    if (on === lost) return;
    lost = on;
    if (on) paint($('lost-banner-body'), 'lost');
    $('lost-banner').hidden = !on;
    if (!on) handlers.onRecovered();
  }

  // Every request the app makes goes through here, so the three ways it can fail are told apart
  // in one place: no token (nothing is sent), a rejected token, and a server that does not answer.
  async function request(path, { method = 'GET', headers = {}, body, token } = {}) {
    if (!token) {
      showTokenPanel();
      throw coded('no-token', 'This browser has no access token.');
    }
    let res;
    try {
      res = await fetch(path, { method, headers: { ...headers, 'x-focusflow-token': token }, body });
    } catch {
      setLost(true);
      throw coded('offline', 'The dashboard is not answering.');
    }
    setLost(false); // any answer means the server is back
    if (res.status === 401) {
      showTokenPanel();
      throw coded('bad-token', 'The dashboard did not accept this browser\'s access token.');
    }
    if (panelUp) hideTokenPanel();
    return res;
  }

  function copy(button) {
    const text = button.getAttribute('data-copy');
    const done = ok => {
      button.textContent = ok ? 'Copied' : 'Press Command-C';
      setTimeout(() => { button.textContent = 'Copy'; }, 2000);
    };
    try { // select the command, so a failed copy can still be finished with Command-C
      const code = button.parentNode.querySelector('code');
      const range = document.createRange();
      range.selectNodeContents(code);
      const sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    } catch { /* selecting is only a fallback */ }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(() => done(true), () => done(false));
    } else {
      let ok = false;
      try { ok = document.execCommand('copy'); } catch { ok = false; }
      done(ok);
    }
  }

  document.addEventListener('click', e => {
    const button = e.target && e.target.closest && e.target.closest('[data-copy]');
    if (button) copy(button);
  });

  // `onRecovered`: the server answers again. `onRetry`: the user says they ran the command, or
  // another tab stored a token (the storage event), so look again.
  function init(h) {
    handlers = { ...handlers, ...h };
    window.addEventListener('storage', e => { if (e.key === 'focusflow-token' && panelUp) handlers.onRetry(); });
  }

  return { init, request, showTokenPanel, hideTokenPanel, setLost, isLost: () => lost, isPanelUp: () => panelUp };
})();
