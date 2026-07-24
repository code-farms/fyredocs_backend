# Fyredocs k6 load-test suite

Realistic, scenario-based load tests covering **every tool and endpoint**, built
to measure the real capacity of the stack on the **Contabo VPS-40 (12 vCPU /
48 GB) @ 80% cap** — how many PDF jobs/min and API req/s it sustains before
latency/queue blow up.

> The suite drives the full lifecycle: **auth → upload → create job → poll until
> completed → (sometimes) download**, with custom metrics for end-to-end job
> time and queue wait, tagged per tool.

---

## 1. Prerequisites

- **k6** on the machine that generates load — ideally a *separate* box from the
  server (so the load generator isn't competing for the VPS's CPU).
  `brew install k6` (macOS) · [other installers](https://grafana.com/docs/k6/latest/set-up/install-k6/).
- **python3** to generate fixtures (stdlib only — no LibreOffice/gs needed).
- The Fyredocs stack running and reachable via `BASE_URL`. **Default is
  `http://localhost`** (the Caddy edge — the docker deploy publishes only :80/:443;
  the api-gateway :8080 is internal-only). For services run on the host via
  `go run`, pass `BASE_URL=http://localhost:8080`.

## 2. Capacity mode (do this first — important)

The suite hits the API hard from one IP. Default rate limits (gateway per-plan,
per-IP job-create/upload, per-IP login) would throttle it and you'd just measure
the limiter. For the **test deployment only**, raise the limits:

```bash
cp .env .env.bak                              # back up first
cat scripts/k6/capacity-mode.env >> .env      # TEST box only — never production
./deployment/deploy.sh                        # redeploy with raised limits
```

Revert afterwards: `mv .env.bak .env && ./deployment/deploy.sh`.
(If you prefer realistic-mode testing *with* limits, skip this and expect 429s in
the results — that measures user-experience, not raw capacity.)

## 3. Generate fixtures

```bash
bash scripts/k6/fixtures/generate.sh          # all categories, sizes small/medium/large
```

Synthetic but valid: `pdf`, `scanned-pdf` (image-only, for OCR), `docx`, `xlsx`,
`pptx`, `image` (png), `html`. To test with **your own representative files**,
drop them at `fixtures/out/<category>/{small,medium,large}.<ext>` — the suite uses
whatever is present and skips tools whose fixture is missing.

## 4. Run

```bash
cd scripts/k6
BASE_URL=https://app.yourdomain.com ./run.sh smoke           # ALWAYS run first
BASE_URL=...                       ./run.sh mixed-realistic   # the headline test
BASE_URL=...                       ./run.sh optimize-pdf vps40 -- -e JOB_RATE=40
```

Outputs a JSON summary + an HTML dashboard under `scripts/k6/results/`.

By default each scenario's `setup()` signs up a fresh pool of users on the
**`free`** plan (50 MB / 10 files). Every bundled fixture is ≤38 MB, so this is
enough for the whole suite — including `upload-heavy`.

### Pro-plan users (optional — for headroom / >50 MB files)
`PUT /auth/plan` is admin-only and only changes the caller's own plan, so k6
**cannot self-upgrade** users. To run as `pro` (500 MB / 50 files), pre-seed the
pool at the DB level, then point k6 at it:

```bash
cd scripts/k6
BASE_URL=https://app.yourdomain.com \
AUTH_DATABASE_URL='postgres://user:pass@host:5432/auth?sslmode=disable' \
./seed-pro-users.sh                      # signs up + promotes k6pool+1..N to pro

# then log in that pool (no signup, no self-upgrade):
USER_EMAIL_PREFIX=k6pool USER_POOL_SIZE=10 USER_PASSWORD='LoadTest!23456' \
BASE_URL=... ./run.sh upload-heavy
```

`AUTH_DATABASE_URL` is the auth-service's `DATABASE_URL` DSN. Requires `psql`.

### Scenarios
| Scenario | What it does |
|---|---|
| `smoke` | 1 VU: every read endpoint + **one job per tool** end-to-end. Contract check — run first; a failure here means a broken contract or bad fixture. |
| `mixed-realistic` | **Headline.** Weighted job mix across all tools + browse traffic, fixed arrival rate. Real-world capacity. |
| `convert-to-pdf` / `convert-from-pdf` / `organize-pdf` / `optimize-pdf` | Per-group capacity. Raise `-e JOB_RATE=` until thresholds break = that group's max throughput. |
| `browse` | Read-heavy (documents/notifications/history/dashboard/orgs/job-lists). |
| `auth-churn` | login/refresh + some signup — exercises bcrypt (capacity-mode only). |
| `upload-heavy` | Presigned upload of medium/large files — bandwidth (800 Mbit/s) + object proxy. |
| `spike` | Sudden 5× surge then recovery — verifies the async queue absorbs bursts. |
| `soak` | Long steady load (default 45m) — leaks, queue drift, TTL/cleanup, cache. |

### Useful env knobs (`-e KEY=val`)
`BASE_URL`, `PROFILE` (`vps40`|`laptop`), `JOB_RATE`, `BROWSE_RATE`, `DURATION`,
`UPLOAD_MODE` (`multipart`|`presigned`), `USER_POOL_SIZE`, `USER_EMAIL`/`USER_PASSWORD`
(reuse fixed creds), `USER_EMAILS`/`USER_EMAIL_PREFIX` + `SEED_EMAIL_DOMAIN` (log in a
pre-seeded pool — see Pro-plan users above), `DOWNLOAD_RATIO`, `JOB_TIMEOUT_MS`, `TEST_PLAN`,
`TEST_NOTIFICATIONS` (default `false` — `/api/notifications` is only hit when `true`,
since notification-service is behind the `notifications` compose profile and is down by
default; enable both together).

### Tool coverage
`config.js` `TOOL_MATRIX` exercises 23 tools, each verified end-to-end by `smoke`
against the live backend. Tools whose fixture is absent are auto-skipped
(`hasFixture`). **Excluded** (verified failing on the current backend): `word-to-odt`,
`odt/ods/odp-to-pdf` (convert-to-pdf worker returns "operation isn't supported" —
routing.go lists them but ODF isn't implemented) and `pdf-to-excel` ("failed to
rename output file"). **Not covered** (need special inputs): `unlock-pdf`
(encrypted-PDF fixture), `scan-to-pdf` (per-page scanner payload); plus stubs
`sign-pdf`/`edit-pdf`.

## 5. Finding the capacity ceiling

1. `./run.sh smoke` → all green (contracts + fixtures OK).
2. For each heavy group, run its scenario and **step `JOB_RATE` up** (e.g. 20 → 40
   → 60/min) until `job_e2e p95` or `job_success` threshold fails. The last passing
   rate is that tool's sustainable throughput.
3. `./run.sh mixed-realistic` at the VPS-40 profile for the blended number.
4. Save the `results/*.json|html` as your baseline to compare after tuning.

### Metrics to read
- `job_e2e` — create→completed (the number users feel). `queue_wait` — create→processing
  (rises first when workers saturate). `job_processing` — server-side processing time.
- `job_success` rate, `jobs_ok` / `jobs_failed` / `jobs_timed_out`.
- `http_req_duration{kind:api}` (API latency) vs `{kind:storage}` (upload/download bytes).
- `upload_bytes` (bandwidth) in `upload-heavy`.

### Watch on the SERVER during a run
```bash
docker stats                                            # must stay within the 80% cap
docker compose -f deployment/docker-compose.yml logs -f convert-to-pdf | grep -i fallback
#   ^ "direct LibreOffice fallback" = unoserver pool too small (raise UNOSERVER_INSTANCES)
docker compose ... logs -f | grep -iE 'oom|killed|panic'    # nothing should appear
```
Also eyeball NATS queue depth (the monitoring port) — a growing backlog means the
arrival rate exceeds processing capacity (expected past the ceiling; jobs queue).

## 6. Cleanup

Jobs and uploads expire via the normal TTLs (job-service's cleanup loop, guest 30m / free 24h)
and the uploads-bucket lifecycle. Provisioned test users (`*@loadtest.fyredocs.local`)
remain in the Neon DB — delete them with a one-off SQL `DELETE FROM users WHERE email
LIKE '%@loadtest.fyredocs.local'` (and cascade) if you want a clean slate.

## 7. Caveats
- **Synthetic office docs**: hand-built minimal OOXML. If `word/excel/ppt-to-pdf`
  fails in `smoke`, drop a real `.docx/.xlsx/.pptx` into `fixtures/out/<cat>/`.
- **scanned-pdf** size classes scale by *page count* (1/4/12), not bytes — OCR cost
  is per-page, so this scales OCR work correctly even though file sizes are similar.
- **Completion** is by polling. `COMPLETION=sse` currently falls back to polling
  (true SSE needs k6's experimental module); polling is equivalent for capacity.
- **Estimates vs reality**: prior back-of-envelope was ~50–120 jobs/min for typical
  small files — this suite replaces that guess with measured numbers.
