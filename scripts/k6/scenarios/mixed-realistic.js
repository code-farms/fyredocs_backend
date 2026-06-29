// Headline scenario: realistic mixed load — weighted job traffic across ALL
// tools running concurrently with read/browse traffic, at a fixed arrival rate.
// constant-arrival-rate keeps firing regardless of backend slowness, so the
// queue/latency growth (job_e2e, queue_wait) reveals the real capacity ceiling.
import { provisionUsers } from '../lib/auth.js';
import { oneWeightedJob, browseOnce } from '../lib/actions.js';
import { PROFILE, THRESHOLDS } from '../config.js';

export const options = {
  scenarios: {
    jobs: {
      executor: 'constant-arrival-rate',
      exec: 'jobs',
      rate: PROFILE.job_rate, timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
    browse: {
      executor: 'constant-arrival-rate',
      exec: 'browse',
      rate: PROFILE.browse_rate, timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: Math.ceil(PROFILE.preAllocVUs / 2), maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: THRESHOLDS,
};

export function setup() {
  return provisionUsers();
}

export function jobs(data) {
  oneWeightedJob(data);
}

export function browse(data) {
  browseOnce(data);
}
