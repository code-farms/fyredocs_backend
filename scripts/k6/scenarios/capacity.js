// Capacity ladder — answers "how many req/s (or jobs/min) can this box actually
// take?", which none of the other scenarios can.
//
// Every other scenario runs constant-arrival-rate at ONE fixed rate: that tells
// you whether the system survives that rate, never where the ceiling is. Finding
// the ceiling used to mean re-running with JOB_RATE=20, 40, 60… by hand and
// eyeballing which run went red (README §5). This walks the rate up
// automatically and names the last step that held the SLO.
//
// Two modes, because "requests per second" means two very different things here:
//
//   MODE=api  (default)  browseOnce() issues exactly ONE request per iteration,
//                        so the arrival rate IS the request rate -> req/min, req/s.
//   MODE=jobs            one iteration is a whole job lifecycle (create, upload,
//                        poll, maybe download), so the rate is JOBS/min — NOT
//                        req/min. These numbers are not comparable; the job rate
//                        is bounded by the worker pools, the API rate by the Go
//                        services.
//
// Usage:
//   ./run.sh capacity -- -e MODE=api  -e START_RATE=300 -e RATE_STEP=300 -e STEPS=8
//   ./run.sh capacity -- -e MODE=jobs -e START_RATE=3 -e RATE_STEP=3 -e STEPS=6 \
//                        -e STEP_DURATION=3m -e SETTLE=1m
//
// Read capacity-mode.env first — without it you measure the rate limiter.
import { provisionUsers } from '../lib/auth.js';
import { oneWeightedJob, browseOnce } from '../lib/actions.js';
import { THRESHOLDS } from '../config.js';

const MODE = (__ENV.MODE || 'api') === 'jobs' ? 'jobs' : 'api';
const IS_JOBS = MODE === 'jobs';

// Ladder shape. Rates are PER MINUTE, matching the rest of the suite.
const START_RATE = Number(__ENV.START_RATE || (IS_JOBS ? 3 : 300));
const RATE_STEP = Number(__ENV.RATE_STEP || (IS_JOBS ? 3 : 300));
const STEPS = Number(__ENV.STEPS || (IS_JOBS ? 6 : 8));
const STEP_DURATION = __ENV.STEP_DURATION || (IS_JOBS ? '3m' : '1m');
// Gap between steps. Jobs mode NEEDS this: without a drain window a step
// inherits the previous step's backlog and reports someone else's queue.
const SETTLE = __ENV.SETTLE || (IS_JOBS ? '1m' : '0s');
// Unmeasured warm-up before the ladder starts. Without it the FIRST step eats
// every cold-start cost — TCP/TLS setup, empty caches, cold DB plans, unoserver
// and LibreOffice spin-up — and reports a latency the server never has again.
// Observed: step 1 at p95 4.7s while later, HIGHER steps sat at ~100-400ms.
// Since the ceiling is "last step before the first failure", one poisoned first
// step throws away the whole run. Set WARMUP=0s to disable.
const WARMUP = __ENV.WARMUP || (IS_JOBS ? '90s' : '30s');

const STEP_SECONDS = toSeconds(STEP_DURATION);
const SETTLE_SECONDS = toSeconds(SETTLE);
const WARMUP_SECONDS = toSeconds(WARMUP);
const TOP_RATE = START_RATE + (STEPS - 1) * RATE_STEP;

// VU sizing by Little's law: concurrency = arrival_rate x latency. The profiles'
// fixed 50/300 were sized for a single rate and would starve the top rungs —
// and a VU-starved step measures k6, not the server.
const EXPECTED_LATENCY_S = Number(__ENV.EXPECTED_LATENCY_S || (IS_JOBS ? 60 : 1));
const VU_MARGIN = Number(__ENV.VU_MARGIN || 3);
// A step counts as actually driven only if it achieved this fraction of target.
// Below it, the generator (not the server) set the rate.
const ACHIEVED_TOLERANCE = Number(__ENV.ACHIEVED_TOLERANCE || 0.9);
const MAX_VUS = Number(
  __ENV.MAX_VUS || Math.max(10, Math.ceil((TOP_RATE / 60) * EXPECTED_LATENCY_S * VU_MARGIN))
);
const PRE_ALLOC_VUS = Number(__ENV.PRE_ALLOC_VUS || Math.max(5, Math.ceil(MAX_VUS / 4)));

// ---------------------------------------------------------------------------
// SLO — parsed out of config.js THRESHOLDS so the capacity number means the same
// thing as the SLO the rest of the suite enforces. One source of truth.
// ---------------------------------------------------------------------------
const SLO = {
  errRate: numFrom(THRESHOLDS.http_req_failed, /rate\s*<\s*([\d.]+)/, 0.02),
  p95: numFrom(THRESHOLDS['http_req_duration{kind:api}'], /p\(95\)\s*<\s*([\d.]+)/, 800),
  p99: numFrom(THRESHOLDS['http_req_duration{kind:api}'], /p\(99\)\s*<\s*([\d.]+)/, 2000),
  jobP95: numFrom(THRESHOLDS.job_e2e, /p\(95\)\s*<\s*([\d.]+)/, 60000),
  jobSuccess: numFrom(THRESHOLDS.job_success, /rate\s*>\s*([\d.]+)/, 0.98),
};

// ---------------------------------------------------------------------------
// Build the ladder: one sequential constant-arrival-rate scenario per step.
// ---------------------------------------------------------------------------
const RATES = [];
const scenarios = {};
const thresholds = {};

// Warm-up runs at START_RATE and is tagged `warmup`, so it never appears as a
// ladder row and its latencies never count toward the ceiling.
if (WARMUP_SECONDS > 0) {
  scenarios.warmup = {
    executor: 'constant-arrival-rate',
    exec: IS_JOBS ? 'jobs' : 'browse',
    rate: START_RATE,
    timeUnit: '1m',
    duration: WARMUP,
    startTime: '0s',
    preAllocatedVUs: PRE_ALLOC_VUS,
    maxVUs: MAX_VUS,
    tags: { step: 'warmup' },
    gracefulStop: IS_JOBS ? '60s' : '10s',
  };
}

for (let i = 0; i < STEPS; i++) {
  const rate = START_RATE + i * RATE_STEP;
  RATES.push(rate);

  scenarios[`step_${rate}`] = {
    executor: 'constant-arrival-rate',
    exec: IS_JOBS ? 'jobs' : 'browse',
    rate,
    timeUnit: '1m',
    duration: STEP_DURATION,
    startTime: `${WARMUP_SECONDS + i * (STEP_SECONDS + SETTLE_SECONDS)}s`,
    preAllocatedVUs: PRE_ALLOC_VUS,
    maxVUs: MAX_VUS,
    // Scenario tags are stamped onto EVERY metric the scenario emits, which is
    // what makes per-step sub-metrics work without touching lib/http.js.
    tags: { step: String(rate) },
    // In-flight work at step end gets time to land instead of being cut off.
    gracefulStop: IS_JOBS ? '60s' : '10s',
  };

  // These thresholds exist to MATERIALISE the sub-metrics (k6 only computes a
  // tagged sub-metric if a threshold references it) — so they are written to
  // always pass. The real SLO verdict is computed in handleSummary.
  //
  // Deliberate: a ladder is SUPPOSED to have failing steps at the top. Using
  // real thresholds here would exit non-zero and, under run.sh's `set -e`, kill
  // the run before it prints the report.
  for (const m of metricsFor(rate)) thresholds[m] = [{ threshold: 'count>=0', abortOnFail: false }];
  thresholds[`http_req_duration{step:${rate},kind:api}`] = [
    { threshold: 'p(95)>=0', abortOnFail: false },
  ];
  thresholds[`http_req_failed{step:${rate}}`] = [{ threshold: 'rate>=0', abortOnFail: false }];
  if (IS_JOBS) {
    thresholds[`job_e2e{step:${rate}}`] = [{ threshold: 'p(95)>=0', abortOnFail: false }];
    thresholds[`job_success{step:${rate}}`] = [{ threshold: 'rate>=0', abortOnFail: false }];
    thresholds[`queue_wait{step:${rate}}`] = [{ threshold: 'p(95)>=0', abortOnFail: false }];
  }
}

function metricsFor(rate) {
  return [`iterations{step:${rate}}`, `dropped_iterations{step:${rate}}`, `http_reqs{step:${rate}}`];
}

export const options = {
  scenarios,
  thresholds,
  // p(99) is NOT in k6's default trend stats, and the SLO needs it.
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  return provisionUsers();
}

export function browse(data) {
  browseOnce(data);
}

export function jobs(data) {
  oneWeightedJob(data);
}

// ---------------------------------------------------------------------------
// Summary — the actual deliverable.
// ---------------------------------------------------------------------------
export function handleSummary(data) {
  const rows = RATES.map((rate) => row(data, rate));

  // The ceiling is the last PASS before the first FAIL — you cannot claim a rate
  // as sustainable if a LOWER rate already breached the SLO. Steps k6 could not
  // drive (INVALID) are skipped rather than counted either way.
  let ceiling = null;
  let firstFailIdx = -1;
  for (let i = 0; i < rows.length; i++) {
    if (rows[i].verdict === 'FAIL') { firstFailIdx = i; break; }
    if (rows[i].verdict === 'PASS') ceiling = rows[i];
  }

  // A healthy ladder degrades monotonically. If something passed ABOVE the first
  // failure, the curve is noisy (cold start, a neighbour process, GC) and the
  // strict rule above would quietly throw away a real result — so say so loudly
  // instead of reporting a number nobody should trust.
  const passesAboveFail =
    firstFailIdx >= 0 ? rows.slice(firstFailIdx + 1).filter((r) => r.verdict === 'PASS') : [];

  const invalid = rows.filter((r) => r.verdict === 'INVALID');
  const text = render(rows, ceiling, invalid, passesAboveFail);

  const out = { stdout: '\n' + text + '\n' };
  const dir = (__ENV.RESULTS_DIR || '.').replace(/\/$/, '');
  const stamp = __ENV.RUN_TS || String(Date.now());
  out[`${dir}/capacity-${MODE}-${stamp}.md`] = text;
  out[`${dir}/capacity-${MODE}-${stamp}.json`] = JSON.stringify(
    { mode: MODE, slo: SLO, ladder: { START_RATE, RATE_STEP, STEPS, STEP_DURATION, SETTLE },
      vus: { preAllocated: PRE_ALLOC_VUS, max: MAX_VUS }, rows, ceiling },
    null,
    2
  );
  return out;
}

function row(data, rate) {
  const iters = val(data, `iterations{step:${rate}}`, 'count', 0);
  const dropped = val(data, `dropped_iterations{step:${rate}}`, 'count', 0);
  const reqs = val(data, `http_reqs{step:${rate}}`, 'count', 0);
  const errRate = val(data, `http_req_failed{step:${rate}}`, 'rate', 0);
  const p95 = val(data, `http_req_duration{step:${rate},kind:api}`, 'p(95)', 0);
  const p99 = val(data, `http_req_duration{step:${rate},kind:api}`, 'p(99)', 0);
  const jobP95 = val(data, `job_e2e{step:${rate}}`, 'p(95)', 0);
  const jobOk = val(data, `job_success{step:${rate}}`, 'rate', 0);
  const queueP95 = val(data, `queue_wait{step:${rate}}`, 'p(95)', 0);

  const minutes = STEP_SECONDS / 60;
  const achieved = minutes > 0 ? iters / minutes : 0;

  // Did k6 actually OFFER the target rate? Two independent signals, because
  // neither is sufficient alone:
  //   - dropped_iterations: the documented "ran out of VUs" counter, but k6 does
  //     not always emit it (a forced-starvation run produced a 56% shortfall and
  //     reported zero dropped iterations), so trusting it alone silently lets a
  //     generator limit through as a server result;
  //   - achieved vs target: executor-independent and the thing we actually care
  //     about — a step we never drove to its target rate cannot be called PASS.
  const shortfall = achieved < rate * ACHIEVED_TOLERANCE;

  let verdict;
  const reasons = [];
  const sloBreached = [];
  if (errRate > SLO.errRate) sloBreached.push(`errors ${pct(errRate)} > ${pct(SLO.errRate)}`);
  if (p95 > SLO.p95) sloBreached.push(`api p95 ${ms(p95)} > ${ms(SLO.p95)}`);
  if (p99 > SLO.p99) sloBreached.push(`api p99 ${ms(p99)} > ${ms(SLO.p99)}`);
  if (IS_JOBS) {
    if (jobP95 > SLO.jobP95) sloBreached.push(`job e2e p95 ${ms(jobP95)} > ${ms(SLO.jobP95)}`);
    if (jobOk < SLO.jobSuccess) sloBreached.push(`job success ${pct(jobOk)} < ${pct(SLO.jobSuccess)}`);
  }

  if (iters === 0 && reqs === 0) {
    verdict = 'INVALID';
    reasons.push('no traffic recorded for this step');
  } else if (sloBreached.length) {
    // The server already broke at (or below) the rate it did receive — a real
    // failure regardless of whether the full target was offered.
    verdict = 'FAIL';
    reasons.push(...sloBreached);
    if (shortfall) reasons.push(`offered only ${achieved.toFixed(0)}/min of ${rate}/min`);
  } else if (shortfall || dropped > 0) {
    // SLO held, but at a rate we never actually delivered — so this says nothing
    // about the target rate and must not be reported as capacity.
    verdict = 'INVALID';
    reasons.push(
      `offered only ${achieved.toFixed(0)}/min of ${rate}/min` +
        (dropped > 0 ? ` (${dropped} dropped)` : '') +
        ' — generator-bound, raise MAX_VUS or run k6 off-box'
    );
  } else {
    verdict = 'PASS';
  }

  return { rate, achieved, iters, reqs, dropped, errRate, p95, p99, jobP95, jobOk, queueP95, verdict, reasons };
}

function render(rows, ceiling, invalid, passesAboveFail) {
  const L = [];
  L.push(`Capacity ladder — MODE=${MODE}`);
  L.push(
    `SLO: api p95<${ms(SLO.p95)}, p99<${ms(SLO.p99)}, errors<${pct(SLO.errRate)}` +
      (IS_JOBS ? `, job e2e p95<${ms(SLO.jobP95)}, job success>${pct(SLO.jobSuccess)}` : '')
  );
  L.push(
    `Steps: ${STEPS} x ${STEP_DURATION} (settle ${SETTLE}, warmup ${WARMUP})   ` +
      `VUs: ${PRE_ALLOC_VUS} pre / ${MAX_VUS} max`
  );
  L.push('');

  const head = IS_JOBS
    ? pad('target', 12) + pad('done/min', 11) + pad('job p95', 10) + pad('queue p95', 11) + pad('ok%', 8) + pad('err%', 8) + pad('drop', 7) + 'verdict'
    : pad('target', 12) + pad('achieved', 11) + pad('p95', 10) + pad('p99', 10) + pad('err%', 8) + pad('drop', 7) + 'verdict';
  L.push('  ' + head);
  L.push('  ' + '-'.repeat(head.length));

  for (const r of rows) {
    const line = IS_JOBS
      ? pad(`${r.rate}/min`, 12) + pad(r.achieved.toFixed(1), 11) + pad(ms(r.jobP95), 10) +
        pad(ms(r.queueP95), 11) + pad(pct(r.jobOk), 8) + pad(pct(r.errRate), 8) + pad(String(r.dropped), 7) + r.verdict
      : pad(`${r.rate}/min`, 12) + pad(r.achieved.toFixed(0) + '/min', 11) + pad(ms(r.p95), 10) +
        pad(ms(r.p99), 10) + pad(pct(r.errRate), 8) + pad(String(r.dropped), 7) + r.verdict;
    L.push('  ' + line + (r.reasons.length ? `  <- ${r.reasons.join('; ')}` : ''));
  }

  L.push('');
  if (ceiling) {
    const perSec = ceiling.rate / 60;
    L.push(
      IS_JOBS
        ? `  => Sustained job capacity: ${ceiling.rate} jobs/min (job e2e p95 ${ms(ceiling.jobP95)})`
        : `  => Sustained API capacity: ${ceiling.rate} req/min (${perSec.toFixed(1)} req/s, p95 ${ms(ceiling.p95)})`
    );
    if (ceiling.rate === RATES[RATES.length - 1]) {
      L.push('  !! The TOP step still passed — the real ceiling is higher. Raise STEPS or RATE_STEP.');
    }
  } else {
    L.push('  => No step met the SLO. Lower START_RATE, or the SLO is unreachable on this host.');
  }
  if (passesAboveFail.length) {
    const higher = passesAboveFail.map((r) => `${r.rate}`).join(', ');
    L.push('');
    L.push(`  !! NON-MONOTONIC: ${higher}/min passed but a LOWER step failed, so the curve is noisy.`);
    L.push('     Usually cold start bleeding into the first step (raise WARMUP), or something else');
    L.push('     on the box stealing CPU. The number above is the conservative reading — re-run');
    L.push('     before trusting it.');
  }
  if (invalid.length) {
    L.push(
      `  !! ${invalid.length} step(s) INVALID (k6 could not offer the rate). Those are generator limits,`
    );
    L.push('     not server limits — raise MAX_VUS (or run k6 off-box) and re-run before trusting them.');
  }
  L.push('');
  L.push('  NOTE: with k6 on the same host as the stack, both compete for CPU — treat this');
  L.push('        as a LOWER BOUND. Run the generator off-box for a true figure.');
  return L.join('\n');
}

// --- small helpers ---------------------------------------------------------
function val(data, metric, key, dflt) {
  const m = (data.metrics || {})[metric];
  if (!m || !m.values || m.values[key] === undefined) return dflt;
  return m.values[key];
}

function numFrom(list, re, dflt) {
  for (const s of list || []) {
    const m = re.exec(typeof s === 'string' ? s : s.threshold || '');
    if (m) return Number(m[1]);
  }
  return dflt;
}

function toSeconds(d) {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(String(d).trim());
  if (!m) throw new Error(`capacity: cannot parse duration "${d}"`);
  const n = Number(m[1]);
  switch (m[2] || 's') {
    case 'ms': return n / 1000;
    case 's': return n;
    case 'm': return n * 60;
    case 'h': return n * 3600;
  }
}

function ms(v) {
  if (!v) return '0ms';
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
}
function pct(v) {
  return `${(v * 100).toFixed(2)}%`;
}
function pad(s, n) {
  s = String(s);
  return s.length >= n ? s + ' ' : s + ' '.repeat(n - s.length);
}
