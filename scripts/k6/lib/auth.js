// Auth: provision a reusable pool of PRO users in setup(), reuse their tokens
// across VUs. The access_token JWT (returned as a cookie) is reused as a Bearer.
import http from 'k6/http';
import { postJSON, get, envelope, url, authHeaders } from './http.js';
import {
  USER_POOL_SIZE, FIXED_EMAIL, FIXED_PASSWORD, PASSWORD, TEST_PLAN, SEEDED_EMAILS,
} from '../config.js';
import { uid } from './util.js';

function tokenFromRes(res) {
  // The backend delivers the access-token JWT ONLY as an HttpOnly `access_token`
  // cookie (auth-service respondWithTokens) — never in the body. So the cookie
  // is the real path; the body fallback below can't fire against the current
  // backend and is kept only for forward-compat with envs that echo a token.
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

function upgradePlan(token, planName) {
  // Best-effort self-upgrade via PUT /auth/plan. The backend now restricts this
  // to admin/super-admin roles AND only changes the caller's OWN plan, so a
  // regular signed-up load user CANNOT self-upgrade — expect 403 here (non-fatal).
  // To actually run as `pro`, pre-seed users at the DB level with seed-pro-users.sh
  // and pass them via USER_EMAILS/USER_EMAIL_PREFIX (the seeded path skips this).
  // Body field is `planName` (auth-service ChangePlan binds planName, not planId).
  const res = http.request('PUT', url('/auth/plan'), JSON.stringify({ planName }),
    { headers: authHeaders(token, { 'Content-Type': 'application/json' }), tags: { kind: 'api' } });
  if (![200, 201, 204].includes(res.status)) {
    console.warn(`plan self-upgrade to '${planName}' returned ${res.status} (non-fatal; seed users as pro if needed)`);
  }
  return res;
}

// Log in a pre-seeded user (created + promoted out-of-band by seed-pro-users.sh).
// No signup, no plan self-upgrade — the DB already holds the desired plan.
function provisionSeeded(email) {
  const password = FIXED_PASSWORD || PASSWORD;
  const li = login(email, password);
  const token = li.status === 200 ? tokenFromRes(li) : '';
  if (!token) console.warn(`seeded login failed for ${email}: ${li.status} ${li.body} — did you run seed-pro-users.sh?`);
  return { email, token };
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
  // Seeded pool takes precedence: log in existing pro users, skip signup/upgrade.
  if (SEEDED_EMAILS.length) {
    const seeded = [];
    for (const email of SEEDED_EMAILS) {
      const u = provisionSeeded(email);
      if (u.token) seeded.push(u);
    }
    if (seeded.length === 0) {
      throw new Error('seeded pool provided but no user could log in — run seed-pro-users.sh and check USER_EMAILS/USER_PASSWORD');
    }
    console.log(`provisioned ${seeded.length}/${SEEDED_EMAILS.length} seeded users`);
    return { users: seeded };
  }
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
