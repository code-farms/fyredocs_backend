# CI Pipeline

The pipeline is a set of **vendor-neutral stage scripts** under `scripts/ci/`.
Each stage is an independent, exit-code-clean bash script that runs from any
directory and needs only the Go toolchain (plus Docker infra for stage 4).
There is no CI-vendor config in the repo by design — the user's chosen system
(Argo Workflows) invokes the same scripts a laptop does, so the pipeline can
never drift from what developers run locally.

## Stages

| # | Script | What it gates | Needs |
|---|---|---|---|
| 1 | `scripts/ci/01-format.sh` | `gofmt -l` clean | Go |
| 2 | `scripts/ci/02-static.sh` | `go vet` per module; golangci-lint (`.golangci.yml`, correctness linters only) when installed — warns loudly when skipped | Go (+ golangci-lint, pinned in the CI image) |
| 3 | `scripts/ci/03-unit.sh` | unit tests, `-race -count=1 -shuffle=on -timeout=10m`, fully offline | Go |
| 4 | `scripts/ci/04-integration.sh` | `-tags=integration` suite against real Postgres/Redis/NATS/MinIO; sources `.env`, waits for Postgres, `-p 1` (shared test DB) | Go + the essentials infra (`make test-infra-up` locally; sidecar/services in CI) |
| 5 | `scripts/ci/05-coverage.sh` | per-module coverprofiles merged to `coverage/merged.out` (artifact) | Go |
| 6 | `scripts/ci/06-fuzz-short.sh` | every `Fuzz*` target for `FUZZTIME` (default 10s) each | Go |
| 7 | `scripts/ci/07-build.sh` | every module compiles | Go |

Deliberately **excluded from the PR pipeline** (run on demand or on a
schedule): `make bench` (benchmarks), long fuzz sessions
(`go test -fuzz=FuzzX -fuzztime=10m ./pkg`), `make test-binaries`
(dockerized gs/libreoffice/tesseract conversion tests), and the k6 load suite
(`scripts/k6/`, see `load-testing.md`).

Local parity: `make lint` ≈ stages 1–2, `make test` = stage 3,
`make test-infra-up && make test-integration` = stage 4, `make cover` = 5,
`make fuzz-short` = 6.

## Argo Workflows wiring

A ready-to-adapt `WorkflowTemplate`. Assumptions: a `golang:1.25`-based runner
image with `golangci-lint` baked in, the repo checked out into a shared
workspace volume, and Kubernetes services (or sidecars, shown here) for the
stage-4 infra. Adjust image tags to match `deployment/docker-compose.yml`.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: WorkflowTemplate
metadata:
  name: fyredocs-backend-ci
spec:
  entrypoint: pipeline
  volumeClaimTemplates:
    - metadata: {name: work}
      spec:
        accessModes: [ReadWriteOnce]
        resources: {requests: {storage: 4Gi}}
  templates:
    - name: pipeline
      dag:
        tasks:
          - name: checkout
            template: checkout
          - name: format
            template: stage
            arguments: {parameters: [{name: script, value: 01-format.sh}]}
            depends: checkout
          - name: static
            template: stage
            arguments: {parameters: [{name: script, value: 02-static.sh}]}
            depends: checkout
          - name: unit
            template: stage
            arguments: {parameters: [{name: script, value: 03-unit.sh}]}
            depends: checkout
          - name: build
            template: stage
            arguments: {parameters: [{name: script, value: 07-build.sh}]}
            depends: checkout
          - name: integration
            template: integration
            depends: unit && build
          - name: coverage
            template: coverage
            depends: unit
          - name: fuzz-short
            template: stage
            arguments: {parameters: [{name: script, value: 06-fuzz-short.sh}]}
            depends: unit

    - name: checkout
      container:
        image: alpine/git:latest
        workingDir: /work
        command: [sh, -c]
        # Clone the ref under test; wire your repo secret/SSH key here.
        args: ["git clone --depth 1 $REPO_URL -b $REF /work/src"]
        volumeMounts: [{name: work, mountPath: /work}]

    - name: stage
      inputs:
        parameters: [{name: script}]
      container:
        image: your-registry/fyredocs-ci-go:1.25   # golang:1.25 + golangci-lint (pinned)
        workingDir: /work/src
        command: [bash]
        args: ["scripts/ci/{{inputs.parameters.script}}"]
        volumeMounts: [{name: work, mountPath: /work}]

    # Stage 4 runs with the infra as sidecars; 04-integration.sh's own
    # Postgres wait loop covers sidecar startup. FYREDOCS_TEST_DB_HOST points
    # at the sidecar since there is no compose network here.
    - name: integration
      container:
        image: your-registry/fyredocs-ci-go:1.25
        workingDir: /work/src
        command: [bash]
        args: ["scripts/ci/04-integration.sh"]
        env:
          - {name: FYREDOCS_TEST_DB_HOST, value: "localhost:5432"}
          - {name: POSTGRES_USER, value: fyredocs}
          - {name: POSTGRES_PASSWORD, valueFrom: {secretKeyRef: {name: fyredocs-ci, key: pg-password}}}
          - {name: POSTGRES_DB, value: fyredocs}
        volumeMounts: [{name: work, mountPath: /work}]
      sidecars:
        - name: db
          image: postgres:18-alpine
          env:
            - {name: POSTGRES_USER, value: fyredocs}
            - {name: POSTGRES_PASSWORD, valueFrom: {secretKeyRef: {name: fyredocs-ci, key: pg-password}}}
            - {name: POSTGRES_DB, value: fyredocs}
        - name: redis
          image: redis:7-alpine
        - name: nats
          image: nats:2.10-alpine
          args: ["-js"]
        - name: minio
          image: pgsty/minio:RELEASE.2026-08-04T00-00-00Z
          args: [server, /data]
          env:
            - {name: MINIO_ROOT_USER, value: fyredocs}
            - {name: MINIO_ROOT_PASSWORD, valueFrom: {secretKeyRef: {name: fyredocs-ci, key: minio-password}}}

    - name: coverage
      container:
        image: your-registry/fyredocs-ci-go:1.25
        workingDir: /work/src
        command: [bash]
        args: ["scripts/ci/05-coverage.sh"]
        volumeMounts: [{name: work, mountPath: /work}]
      outputs:
        artifacts:
          - name: coverage
            path: /work/src/coverage/merged.out
```

Notes for the Argo install:

- **Secrets**: `.env` is not in git; stage 4 in CI relies on the exported
  `POSTGRES_*` / `S3_*` env vars instead (the dbtest harness and the storage
  integration test read env first and only fall back to compose defaults).
  MinIO in CI uses root credentials, so `S3_ACCESS_KEY`/`S3_SECRET_KEY` should
  be set to those and `S3_ENDPOINT=localhost:9000`.
- **Caching**: mount a persistent volume at `/root/go` (module + build cache)
  to keep stage times sane; without it every run recompiles the world.
- **Trigger**: wire an Argo Events GitHub/webhook source to submit this
  template per push/PR; benchmarks and long fuzz get their own CronWorkflow.
