// Read-heavy "browsing" load — no jobs, just authenticated reads (documents,
// notifications, history, dashboard, orgs, job lists). Stresses the gateway
// routing + the read-side Go services + Neon DB.
import { provisionUsers } from '../lib/auth.js';
import { browseOnce } from '../lib/actions.js';
import { PROFILE, THRESHOLDS } from '../config.js';

export const options = {
  scenarios: {
    browse: {
      executor: 'constant-arrival-rate',
      rate: PROFILE.browse_rate, timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: THRESHOLDS,
};

export function setup() { return provisionUsers(); }
export default function (data) { browseOnce(data); }
