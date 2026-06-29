# Load testing

A k6 load-test suite lives at [`scripts/k6/`](../../scripts/k6/) and covers
**every tool and endpoint** with realistic, scenario-based traffic (auth →
upload → create job → poll → download), to measure real capacity on the
Contabo VPS-40 (12 vCPU / 48 GB) @ 80% cap.

Quick start (full docs in [`scripts/k6/README.md`](../../scripts/k6/README.md)):

```bash
# 1. (test box only) raise rate limits so you measure the backend, not the limiter
cat scripts/k6/capacity-mode.env >> .env && ./deployment/deploy.sh

# 2. generate synthetic fixtures (pure python stdlib)
bash scripts/k6/fixtures/generate.sh

# 3. validate contracts, then run the headline mixed test
cd scripts/k6
BASE_URL=https://app.yourdomain.com ./run.sh smoke
BASE_URL=https://app.yourdomain.com ./run.sh mixed-realistic
```

Scenarios: `smoke`, `mixed-realistic`, per-group (`convert-to-pdf`,
`convert-from-pdf`, `organize-pdf`, `optimize-pdf`), `browse`, `auth-churn`,
`upload-heavy`, `spike`, `soak`. Key metrics: `job_e2e`, `queue_wait`,
`job_success`, `http_req_duration{kind:api|storage}`. Watch `docker stats`
(stays within the 80% cap) and `convert-to-pdf` logs for unoserver "fallback"
warnings during a run.
