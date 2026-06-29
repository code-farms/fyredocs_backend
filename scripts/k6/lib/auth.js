// Auth: provision a reusable pool of PRO users in setup(), reuse their tokens
// across VUs. The access_token JWT (returned as a cookie) is reused as a Bearer.
import http from 'k6/http';
import { postJSON, get, envelope, url, authHeaders } from './http.js';
import {
  USER_POOL_SIZE, FIXED_EMAIL, FIXED_PASSWORD, PASSWORD, TEST_PLAN,
} from '../config.js';
import { uid } from './util.js';

function tokenFromRes(res) {
  // Prefer the access_token cookie; fall back to a token field in the body.
  if (res.cookies && res.cookies.access_token && res.cookies.access_token.length) {
    return res.cookies.access_token[0].value;
  }
  const b = envelope(res);
  return (b.data && (b.data.accessToken || b.data.token)) || '';
}

function signup(email, password) {
  return postJSON('/auth/signup', {
    email, password, fullName: 'Load Tester', country: 'India', phone: '+910000000000',
  });
}

function login(email, password) {
  return postJSON('/auth/login', { email, password });
}

function upgradePlan(token, planId) {
  // Best-effort: dev/test envs let a user self-select a plan via PUT /auth/plan.
  // Non-fatal — capacity mode raises gateway limits anyway; the plan mainly
  // affects max file size (matters for upload-heavy large files).
  const res = http.request('PUT', url('/auth/plan'), JSON.stringify({ planId }),
    { headers: authHeaders(token, { 'Content-Type': 'application/json' }), tags: { kind: 'api' } });
  if (![200, 201, 204].includes(res.status)) {
    console.warn(`plan upgrade to '${planId}' returned ${res.status} (continuing)`);
  }
  return res;
}

// Provision one usable {email, token}. Reuses fixed creds if provided.
function provisionOne(i) {
  let email = FIXED_EMAIL;
  let password = FIXED_PASSWORD || PASSWORD;
  if (!email) {
    email = `${uid('k6')}+${i}@loadtest.fyredocs.local`;
    const su = signup(email, password);
    // 201 = created, 409/400 = already exists -> fall through to login
    if (![200, 201, 409, 400].includes(su.status)) {
      console.warn(`signup failed for ${email}: ${su.status} ${su.body}`);
    }
  }
  let token = '';
  const li = login(email, password);
  if (li.status === 200) token = tokenFromRes(li);
  else console.warn(`login failed for ${email}: ${li.status} ${li.body}`);
  if (token && TEST_PLAN) upgradePlan(token, TEST_PLAN);
  return { email, token };
}

// Called from each scenario's setup(). Returns { users: [{email, token}] }.
export function provisionUsers() {
  const n = FIXED_EMAIL ? 1 : Math.max(1, USER_POOL_SIZE);
  const users = [];
  for (let i = 0; i < n; i++) {
    const u = provisionOne(i);
    if (u.token) users.push(u);
  }
  if (users.length === 0) {
    throw new Error('could not provision any authenticated user — check BASE_URL / auth env / capacity-mode limits');
  }
  console.log(`provisioned ${users.length}/${n} users`);
  return { users };
}

// Pick a token for the current VU+iteration (spreads across the pool).
export function pickToken(data) {
  if (!data || !data.users || !data.users.length) return '';
  // eslint-disable-next-line no-undef
  const idx = ((__VU - 1) + __ITER) % data.users.length;
  return data.users[idx].token;
}

// Sanity check used by smoke: confirm the token works.
export function whoami(token) {
  return get('/auth/me', token);
}
