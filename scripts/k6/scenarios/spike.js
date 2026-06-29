// Spike test — sudden surge of mixed job traffic to see how the async queue
// absorbs bursts (jobs should queue, not error). ramping-arrival-rate: baseline
// -> sharp spike -> recovery. Watch queue_wait + job_success during/after spike.
import { provisionUsers } from '../lib/auth.js';
import { oneWeightedJob } from '../lib/actions.js';
import { PROFILE, THRESHOLDS } from '../config.js';

const base = PROFILE.job_rate;

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: base, timeUnit: '1m',
      preAllocatedVUs: PROFILE.preAllocVUs, maxVUs: PROFILE.maxVUs,
      stages: [
        { target: base, duration: '1m' },        // warm baseline
        { target: base * 5, duration: '30s' },    // sharp spike
        { target: base * 5, duration: '1m' },     // hold the surge
        { target: base, duration: '30s' },        // drop back
        { target: base, duration: '2m' },         // recovery / drain
      ],
    },
  },
  // success may dip during the spike; relax the SLO but keep error-rate honest.
  thresholds: Object.assign({}, THRESHOLDS, { job_success: ['rate>0.90'] }),
};

export function setup() { return provisionUsers(); }
export default function (data) { oneWeightedJob(data); }
