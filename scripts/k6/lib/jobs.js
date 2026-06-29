// Job lifecycle: create (multipart or presigned) -> wait (poll) -> optional
// download, with custom metrics. This is the core building block for scenarios.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { get, postJSON, envelope, authHeaders, url } from './http.js';
import { presignedUpload, multipartBody } from './upload.js';
import { pickFile } from './files.js';
import {
  jobE2E, queueWait, jobProcessing, jobsOk, jobsFailed, jobsTimedOut, jobSuccess,
} from './metrics.js';
import {
  UPLOAD_MODE, COMPLETION, POLL_INTERVAL_MS, JOB_TIMEOUT_MS, DOWNLOAD_RATIO,
} from '../config.js';

const TERMINAL = { completed: true, failed: true };

// Create a job for a tool definition. Returns {ok, jobId, group, tool, t0}.
export function createJob(toolDef, token, mode, sizeName) {
  const m = mode || UPLOAD_MODE;
  const nFiles = toolDef.multi || 1;
  const files = [];
  for (let i = 0; i < nFiles; i++) files.push(pickFile(toolDef.fixture, sizeName));
  const path = `/api/${toolDef.group}/${toolDef.tool}`;
  const t0 = Date.now();

  let res;
  if (nFiles === 1 && m === 'multipart') {
    res = http.post(url(path), multipartBody(files[0], toolDef.options), {
      headers: authHeaders(token), // no Content-Type -> k6 sets multipart boundary
      tags: { kind: 'api', tool: toolDef.tool },
    });
  } else {
    // presigned: upload each file, then JSON create with uploadIds
    const ids = [];
    for (const f of files) {
      const up = presignedUpload(f, token);
      if (!up.ok) {
        jobsFailed.add(1); jobSuccess.add(false);
        return { ok: false, stage: 'upload', status: up.status, group: toolDef.group, tool: toolDef.tool };
      }
      ids.push(up.uploadId);
    }
    const body = ids.length === 1 ? { uploadId: ids[0], options: toolDef.options }
                                  : { uploadIds: ids, options: toolDef.options };
    res = postJSON(path, body, token, { kind: 'api', tool: toolDef.tool });
  }

  const created = check(res, { [`create ${toolDef.tool}: 201`]: (r) => r.status === 201 || r.status === 200 });
  if (!created) {
    jobsFailed.add(1); jobSuccess.add(false);
    return { ok: false, stage: 'create', status: res.status, body: res.body, group: toolDef.group, tool: toolDef.tool };
  }
  const jobId = (envelope(res).data || {}).id;
  return { ok: true, jobId, group: toolDef.group, tool: toolDef.tool, t0 };
}

// Poll job status until terminal or timeout. Records queue_wait, job_e2e,
// job_processing, success/fail counters.
export function pollUntilDone(job, token) {
  const tags = { tool: job.tool };
  const deadline = Date.now() + JOB_TIMEOUT_MS;
  let sawProcessing = false;
  while (Date.now() < deadline) {
    const res = get(`/api/${job.group}/${job.tool}/${job.jobId}`, token, { kind: 'api', tool: job.tool });
    const data = (envelope(res).data) || {};
    const status = String(data.status || '').toLowerCase();
    if (!sawProcessing && status === 'processing') {
      sawProcessing = true;
      queueWait.add(Date.now() - job.t0, tags);
    }
    if (TERMINAL[status]) {
      const e2e = Date.now() - job.t0;
      jobE2E.add(e2e, tags);
      if (data.createdAt && data.completedAt) {
        const proc = new Date(data.completedAt) - new Date(data.createdAt);
        if (proc > 0) jobProcessing.add(proc, tags);
      }
      const ok = status === 'completed';
      jobSuccess.add(ok);
      if (ok) { jobsOk.add(1); } else { jobsFailed.add(1); }
      return { status, e2e, data };
    }
    sleep(POLL_INTERVAL_MS / 1000);
  }
  jobsTimedOut.add(1); jobSuccess.add(false);
  return { status: 'timeout' };
}

// Follow the download (302 -> presigned). Exercises the gateway object proxy.
export function downloadResult(job, token) {
  const res = get(`/api/${job.group}/${job.tool}/${job.jobId}/download`, token, { kind: 'storage', tool: job.tool });
  check(res, { [`download ${job.tool}: 2xx`]: (r) => r.status >= 200 && r.status < 300 });
  return res;
}

// One full realistic job: create -> wait -> sometimes download.
export function runJob(toolDef, token, mode, sizeName) {
  const job = createJob(toolDef, token, mode, sizeName);
  if (!job.ok) return job;
  if (COMPLETION === 'sse') {
    // True SSE needs k6's experimental module; poll is equivalent for capacity.
    // (Documented in README.) Fall through to poll.
  }
  const result = pollUntilDone(job, token);
  if (result.status === 'completed' && Math.random() < DOWNLOAD_RATIO) {
    downloadResult(job, token);
  }
  return Object.assign({ ok: result.status === 'completed' }, job, result);
}
