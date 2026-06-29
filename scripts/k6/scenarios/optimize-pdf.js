// Capacity test for optimize-pdf (Ghostscript compress + Tesseract OCR — the
// heaviest, CPU-bound). OCR throughput scales with pages × OCR_MAX_WORKERS; this
// is usually the lowest-throughput tool. Raise JOB_RATE until thresholds break.
import { provisionUsers } from '../lib/auth.js';
import { oneJobFrom } from '../lib/actions.js';
import { toolsForGroup, PROFILE, THRESHOLDS } from '../config.js';

const TOOLS = toolsForGroup('optimize-pdf');

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
