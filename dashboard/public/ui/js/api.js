// Every request goes through Help.request (public/help.js), which tells "no token", "token rejected"
// and "server not answering" apart and shows the recovery steps for each.
let token = null;
// public/help.js is a classic script whose `const Help` is global but not a window property.
// eslint-disable-next-line no-undef
export const helpApi = () => (typeof Help !== 'undefined' ? Help : window.Help);

export function setToken(fromUrl) {
  try {
    if (fromUrl) localStorage.setItem('focusflow-token', fromUrl);
    token = fromUrl || localStorage.getItem('focusflow-token');
  } catch { token = fromUrl; }
}

export function reloadToken() { try { token = localStorage.getItem('focusflow-token') || token; } catch { /* keep */ } }
export const getToken = () => token;

export async function api(path, method = 'GET', body) {
  const headers = {};
  if (body !== undefined) headers['content-type'] = 'application/json';
  const res = await helpApi().request(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), token });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || `request failed (${res.status})`);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

// For page unload: no Help wrapper, keepalive so it survives navigation.
export function beacon(path, body) {
  try { fetch(path, { method: 'POST', keepalive: true, headers: { 'content-type': 'application/json', 'x-focusflow-token': token || '' }, body: JSON.stringify(body) }); } catch { /* best effort */ }
}
