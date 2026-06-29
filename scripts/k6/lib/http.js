// Thin HTTP helpers: auth headers, standard-envelope parsing, tagged requests.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL } from '../config.js';

// Authorization header from a token (the access_token JWT works as a Bearer).
export function authHeaders(token, extra) {
  const h = extra ? Object.assign({}, extra) : {};
  if (token) h['Authorization'] = `Bearer ${token}`;
  return h;
}

// Parse the standard { success, message, data, error, meta } envelope. Never
// throws — returns {} on non-JSON so callers can check r.status instead.
export function envelope(res) {
  try {
    return res.json();
  } catch (_e) {
    return {};
  }
}

// Tag a request so thresholds can target it (e.g. {kind:'api'}). All app/API
// calls should pass tags:{kind:'api'}; presigned S3 PUTs use {kind:'storage'}.
export function get(path, token, tags) {
  return http.get(url(path), { headers: authHeaders(token), tags: tags || { kind: 'api' } });
}

export function postJSON(path, body, token, tags, extraHeaders) {
  const headers = authHeaders(token, Object.assign({ 'Content-Type': 'application/json' }, extraHeaders || {}));
  return http.post(url(path), JSON.stringify(body), { headers, tags: tags || { kind: 'api' } });
}

export function url(path) {
  return path.startsWith('http') ? path : `${BASE_URL}${path}`;
}

// Standard success check on the envelope.
export function checkOk(res, name, expected) {
  const codes = expected || [200, 201, 204];
  return check(res, {
    [`${name}: status ${codes.join('/')}`]: (r) => codes.includes(r.status),
    [`${name}: success!=false`]: (r) => {
      if (r.status === 204) return true;
      const b = envelope(r);
      return b.success !== false; // tolerate endpoints that omit the flag
    },
  });
}
