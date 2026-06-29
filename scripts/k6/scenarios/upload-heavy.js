// Upload-bandwidth test — forces the PRESIGNED flow with MEDIUM/LARGE files
// (init -> PUT parts -> complete), then a cheap compress job. Stresses the
// gateway object-proxy + MinIO + the 800 Mbit/s port. Watch upload_bytes and
// http_req_duration{kind:storage}. Provisioned users are PRO (large file cap).
import { provisionUsers, pickToken } from '../lib/auth.js';
import { runJob } from '../lib/jobs.js';
import { randItem } from '../lib/util.js';
import { PROFILE, THRESHOLDS } from '../config.js';

// compress + repair accept plain PDFs; bias toward large bytes.
const TOOLS = [
  { tool: 'compress-pdf', group: 'optimize-pdf', fixture: 'pdf', options: { quality: 'medium' } },
  { tool: 'pdf-to-word', group: 'convert-from-pdf', fixture: 'pdf', options: {} },
];
const SIZES = ['medium', 'medium', 'large']; // mostly medium, some large

export const options = {
  scenarios: {
    upload: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.UPLOAD_RATE || 30), timeUnit: '1m', duration: PROFILE.duration,
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
    },
  },
  thresholds: Object.assign({}, THRESHOLDS, {
    'http_req_duration{kind:storage}': ['p(95)<30000'],
  }),
};

export function setup() { return provisionUsers(); }

export default function (data) {
  const token = pickToken(data);
  runJob(randItem(TOOLS), token, 'presigned', randItem(SIZES));
}
