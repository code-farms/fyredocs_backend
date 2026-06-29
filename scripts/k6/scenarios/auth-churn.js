// Auth churn — login/refresh dominated, with occasional signup. Exercises the
// bcrypt cost path on auth-service (the hidden CPU limiter). CAPACITY MODE ONLY:
// the auth rate limits (login 5/min, signup 3/min per IP) must be raised or this
// just measures the limiter. Each iteration creates real DB rows on signup.
import { check } from 'k6';
import { postJSON, envelope } from '../lib/http.js';
import { PASSWORD } from '../config.js';
import { uid } from '../lib/util.js';
import { PROFILE, THRESHOLDS } from '../config.js';

export const options = {
  scenarios: {
    auth: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.AUTH_RATE || 300), timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: Object.assign({}, THRESHOLDS, { job_success: ['rate>=0'] }), // no jobs here
};

// A small shared set of pre-made accounts to log in against, plus ~10% signups.
export function setup() {
  const users = [];
  for (let i = 0; i < 20; i++) {
    const email = `${uid('churn')}+${i}@loadtest.fyredocs.local`;
    const su = postJSON('/auth/signup', {
      email, password: PASSWORD, fullName: 'Churn', country: 'India', phone: '+910000000000',
    });
    if ([200, 201].includes(su.status)) users.push(email);
  }
  return { users };
}

export default function (data) {
  const roll = Math.random();
  if (roll < 0.1 || data.users.length === 0) {
    // signup
    const email = `${uid('churn')}@loadtest.fyredocs.local`;
    const r = postJSON('/auth/signup', {
      email, password: PASSWORD, fullName: 'Churn', country: 'India', phone: '+910000000000',
    });
    check(r, { 'signup 2xx': (x) => x.status === 201 || x.status === 200 });
  } else {
    // login (+ refresh half the time)
    const email = data.users[Math.floor(Math.random() * data.users.length)];
    const li = postJSON('/auth/login', { email, password: PASSWORD });
    check(li, { 'login 200': (x) => x.status === 200 });
    if (roll < 0.55 && li.status === 200) {
      const token = (li.cookies.access_token && li.cookies.access_token[0].value) ||
        ((envelope(li).data || {}).accessToken);
      const rf = postJSON('/auth/refresh', {}, token);
      check(rf, { 'refresh 2xx': (x) => x.status === 200 });
    }
  }
}
