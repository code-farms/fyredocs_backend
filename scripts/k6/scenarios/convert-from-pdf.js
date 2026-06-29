// Capacity test for convert-from-pdf (PDF→Office/text/image; pdf2docx is the
// slowest path). Raise JOB_RATE until job_e2e p95 / job_success break.
import { provisionUsers } from '../lib/auth.js';
import { oneJobFrom } from '../lib/actions.js';
import { toolsForGroup, PROFILE, THRESHOLDS } from '../config.js';

const TOOLS = toolsForGroup('convert-from-pdf');

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
