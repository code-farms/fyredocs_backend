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
| `capacity` | **Answers "how many req/s / jobs/min?"** Walks the arrival rate up step by step and reports the last step that held the SLO. `-e MODE=api` (req/min) or `-e MODE=jobs` (jobs/min). See §5. |
| `mixed-realistic` | **Headline.** Weighted job mix across all tools + browse traffic, fixed arrival rate. Behaviour *at* that rate — use `capacity` to find the ceiling. |
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
`config.js` `TOOL_MATRIX` exercises 32 tools, each verified end-to-end by `smoke`
against the live backend. Tools whose fixture is absent are auto-skipped
(`hasFixture`). ODF conversions (`word-to-odt`, `excel-to-ods`, `powerpoint-to-odp`,
`odt/ods/odp-to-pdf`) and `pdf-to-excel`/`pdf-to-ods`/`pdf-to-odp` are covered as
of the 2026-07 conversion-bug fixes: the convert-to-pdf allowlist was extended for
ODF, and `pdf-to-excel/ods` now use real table extraction (pdfplumber + openpyxl)
while `pdf-to-odp` builds an image-slide deck then converts to ODP. **Not covered**
(need special inputs): `unlock-pdf` (encrypted-PDF fixture), `scan-to-pdf` (per-page
scanner payload); plus stubs `sign-pdf`/`edit-pdf`.

> ODF fixtures (`odt`/`ods`/`odp`) are real, known-good templates decoded from
> `fixtures/odf_base.py` (produced once by LibreOffice — hand-rolled minimal ODF
> is rejected by LibreOffice's importer, and padding a valid ODF package breaks
> it). They are `small`-size only; `pickFile` falls back to `small` for the
> medium/large classes. To use your own, drop a file at
> `fixtures/out/<odt|ods|odp>/small.<ext>`.

## 5. Finding the capacity ceiling

Use the `capacity` scenario — it walks the arrival rate up automatically and tells
you the last step that stayed inside the SLO, instead of you re-running by hand at
20 → 40 → 60 and eyeballing which run went red.

**"Requests per second" means two different things here**, so there are two modes.
Do not mix the numbers: the API rate is bounded by the gateway and Go services, the
job rate by the worker pools (currently one slot per worker on a small host).

```bash
./run.sh smoke                    # 1. contracts + fixtures green first

# 2. API request capacity (~8 min). One iteration = exactly one request.
./run.sh capacity -- -e MODE=api -e START_RATE=300 -e RATE_STEP=300 -e STEPS=8

# 3. Job throughput (~25 min). One iteration = a whole job lifecycle, so the
#    rate is JOBS/min. Slower: each step needs a drain window.
./run.sh capacity -- -e MODE=jobs -e START_RATE=3 -e RATE_STEP=3 -e STEPS=6 \
                     -e STEP_DURATION=3m -e SETTLE=1m
```

It prints a table and writes `results/capacity-<mode>-<ts>.md|json`:

```
  target      achieved   p95       p99       err%    drop   verdict
  60/min      60/min     439ms     591ms     0.00%   0      PASS
  120/min     120/min    254ms     388ms     0.00%   0      PASS
  180/min     183/min    713ms     1.7s      0.00%   0      PASS

  => Sustained API capacity: 180 req/min (3.0 req/s, p95 713ms)
```

Read it as: **the last `PASS` before the first `FAIL` is the answer.** Three things
the summary does for you rather than leaving you to spot them:

- **`INVALID` steps.** If k6 delivered materially less than the target rate
  (default: under 90%, tunable with `ACHIEVED_TOLERANCE`), the step is marked
  `INVALID (generator-bound)` and excluded — a load-generator limit is never
  reported as a server limit. Raise `MAX_VUS` and re-run those.
- **Warm-up.** The first step otherwise absorbs all cold-start cost and reports a
  latency the server never has again — measured at p95 4.7s on step 1 versus
  ~100–400 ms on the higher steps. `WARMUP` (default `30s` api / `90s` jobs) runs
  untagged before the ladder. `WARMUP=0s` disables it.
- **Non-monotonic curves.** If a step above the first failure passed, the run was
  noisy and the summary says so instead of quietly reporting a number.

The knobs: `MODE`, `START_RATE`, `RATE_STEP`, `STEPS`, `STEP_DURATION`, `SETTLE`,
`WARMUP`, `MAX_VUS` / `PRE_ALLOC_VUS` (auto-sized from the top step by Little's law),
`ACHIEVED_TOLERANCE`. The SLO is not a knob — it is read from `THRESHOLDS` in
`config.js`, so a capacity number always means the same thing the rest of the suite
enforces.

If the top step still passes, the real ceiling is higher — raise `STEPS` or
`RATE_STEP` (the summary tells you when this happens).

For a single tool group you can still step `-e JOB_RATE=` by hand across runs of
`convert-to-pdf` / `organize-pdf` / etc. Then `./run.sh mixed-realistic` for the
blended picture, and save `results/*` as the baseline to diff after tuning.

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
- **Co-located generator**: running k6 on the same box as the stack means both
  compete for the same CPU, so every capacity number is a **lower bound** — the
  harder you push, the more k6 itself steals. On a small host (8 vCPU, stack already
  capped at 80%) the distortion is large. Run k6 from another machine against
  `BASE_URL=http://<host>` for a figure you can quote.
