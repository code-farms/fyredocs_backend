// Soak test — steady, sustainable mixed load for a long duration (default 45m)
// to surface leaks, slow memory growth, queue drift, TTL/cleanup behaviour, and
// result-cache effects. Run JOB_RATE at ~60-70% of the measured per-tool max.
import { provisionUsers } from '../lib/auth.js';
import { oneWeightedJob, browseOnce } from '../lib/actions.js';
import { PROFILE, THRESHOLDS } from '../config.js';

const DURATION = __ENV.SOAK_DURATION || '45m';
const rate = Math.round(PROFILE.job_rate * Number(__ENV.SOAK_FACTOR || 0.65));

export const options = {
  scenarios: {
    jobs: {
      executor: 'constant-arrival-rate',
      exec: 'jobs',
      rate, timeUnit: '1m', duration: DURATION,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
    browse: {
      executor: 'constant-arrival-rate',
      exec: 'browse',
      rate: PROFILE.browse_rate, timeUnit: '1m', duration: DURATION,
      preAllocatedVUs: Math.ceil(PROFILE.preAllocVUs / 2), maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: THRESHOLDS,
};

export function setup() { return provisionUsers(); }
export function jobs(data) { oneWeightedJob(data); }
export function browse(data) { browseOnce(data); }
