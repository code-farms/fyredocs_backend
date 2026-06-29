// Capacity test for convert-to-pdf (Office→PDF, LibreOffice/unoserver — heavy).
// Raise JOB_RATE (-e JOB_RATE=...) until job_e2e p95 / job_success break: that's
// the sustainable throughput. Watch `docker compose logs convert-to-pdf` for
// "fallback" warnings (= unoserver pool too small).
import { provisionUsers } from '../lib/auth.js';
import { oneJobFrom } from '../lib/actions.js';
import { toolsForGroup, PROFILE, THRESHOLDS } from '../config.js';

const TOOLS = toolsForGroup('convert-to-pdf');

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
