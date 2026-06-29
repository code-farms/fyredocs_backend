// Reusable high-level actions composed by scenarios.
import { check } from 'k6';
import { get } from './http.js';
import { runJob } from './jobs.js';
import { pickToken } from './auth.js';
import { weightedPick, randItem } from './util.js';
import { TOOL_MATRIX } from '../config.js';

// Read-only "browsing" hit — one of the common authenticated read endpoints.
const READ_ENDPOINTS = [
  '/api/documents?page=1&limit=25',
  '/api/notifications',
  '/api/jobs/history?page=1&limit=25',
  '/api/dashboard',
  '/api/orgs',
  '/api/convert-to-pdf/word-to-pdf?page=1&limit=10',
];

export function browseOnce(data) {
  const token = pickToken(data);
  const path = randItem(READ_ENDPOINTS);
  const res = get(path, token, { kind: 'api' });
  check(res, { [`browse ${path}: 2xx`]: (r) => r.status >= 200 && r.status < 300 });
  return res;
}

// Run one weighted job from the full matrix (used by mixed-realistic).
export function oneWeightedJob(data) {
  const token = pickToken(data);
  const toolDef = weightedPick(TOOL_MATRIX);
  return runJob(toolDef, token);
}

// Run one weighted job from a subset (used by per-group scenarios).
export function oneJobFrom(data, tools) {
  const token = pickToken(data);
  const toolDef = weightedPick(tools);
  return runJob(toolDef, token);
}
