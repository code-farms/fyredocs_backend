// Custom metrics shared across scenarios.
import { Trend, Counter, Rate } from 'k6/metrics';

// End-to-end: job create -> status=completed. Tagged by {tool:...}.
export const jobE2E = new Trend('job_e2e', true);
// Queue/start latency: job create -> first status=processing.
export const queueWait = new Trend('queue_wait', true);
// Server-reported processing time when available (completedAt - createdAt).
export const jobProcessing = new Trend('job_processing', true);

export const jobsOk = new Counter('jobs_ok');
export const jobsFailed = new Counter('jobs_failed');
export const jobsTimedOut = new Counter('jobs_timed_out');
// Overall success rate (completed / attempted) — drives the job_success SLO.
export const jobSuccess = new Rate('job_success');

export const uploadBytes = new Counter('upload_bytes');
