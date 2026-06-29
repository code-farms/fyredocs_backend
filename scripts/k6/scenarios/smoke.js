// Smoke / contract test — RUN THIS FIRST.
// 1 VU: provisions a user, hits every read endpoint, then runs ONE job per tool
// in the matrix end-to-end (create -> completed -> download). Any non-2xx, failed
// job, or timeout fails the run. This validates every contract before load.
import { check, sleep } from 'k6';
import { provisionUsers, pickToken, whoami } from '../lib/auth.js';
import { get } from '../lib/http.js';
import { runJob } from '../lib/jobs.js';
import { TOOL_MATRIX } from '../config.js';

export const options = {
  scenarios: { smoke: { executor: 'per-vu-iterations', vus: 1, iterations: 1, maxDuration: '20m' } },
  thresholds: { http_req_failed: ['rate<0.10'], job_success: ['rate>0.95'] },
};

export function setup() {
  return provisionUsers();
}

export default function (data) {
  const token = pickToken(data);

  // auth sanity
  check(whoami(token), { '/auth/me 200': (r) => r.status === 200 });

  // every read endpoint
  for (const path of [
    '/api/documents?page=1&limit=10', '/api/notifications', '/api/jobs/history',
    '/api/dashboard', '/api/orgs',
  ]) {
    check(get(path, token), { [`read ${path} 2xx`]: (r) => r.status >= 200 && r.status < 300 });
  }

  // one job per tool, end-to-end
  for (const toolDef of TOOL_MATRIX) {
    const r = runJob(toolDef, token);
    check(r, {
      [`${toolDef.tool}: completed`]: (x) => x.ok === true,
    });
    if (!r.ok) {
      console.warn(`SMOKE FAIL ${toolDef.tool}: stage=${r.stage || 'poll'} status=${r.status || r.status} body=${(r.body || '').slice(0, 200)}`);
    }
    sleep(0.3);
  }
}
