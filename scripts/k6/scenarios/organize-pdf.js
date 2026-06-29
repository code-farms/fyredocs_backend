// Capacity test for organize-pdf (pdfcpu — fast/cheap; should sustain high
// rates). Raise JOB_RATE aggressively here; the limit is usually CPU share, not
// per-job cost.
import { provisionUsers } from '../lib/auth.js';
import { oneJobFrom } from '../lib/actions.js';
import { toolsForGroup, PROFILE, THRESHOLDS } from '../config.js';

const TOOLS = toolsForGroup('organize-pdf');

export const options = {
  scenarios: {
    jobs: {
      executor: 'constant-arrival-rate',
      rate: PROFILE.job_rate, timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: THRESHOLDS,
};

export function setup() { return provisionUsers(); }
export default function (data) { oneJobFrom(data, TOOLS); }
