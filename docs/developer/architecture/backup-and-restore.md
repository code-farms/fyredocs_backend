# Database Backup & Restore

The database is a self-hosted PostgreSQL 18 container co-located on the VPS
(fast, but a single point of failure). Code lives on GitHub; the **data does
not** — so an offsite backup is required for disaster recovery.

## What runs

The **`db-backup`** sidecar (`deployment/backup/`) runs a loop that, every
`BACKUP_INTERVAL` seconds (default **3600 = hourly**):

1. `pg_dump` the local Postgres → gzip → `/tmp/db-<UTC-timestamp>.sql.gz`
2. uploads it to an **external** S3-compatible bucket via **rclone**
   (remote `dest`, prefix `BACKUP_PREFIX`, default `postgres/`)
3. prunes so only the newest `BACKUP_RETAIN` dumps remain (default **10**)

Backup failures are logged and retried next cycle (the loop never exits). The
container's `HEALTHCHECK` goes **unhealthy** if no backup has succeeded within
~2 intervals, so a silent failure (e.g. bad credentials) shows up in
`docker ps`.

> **Full snapshots, not incremental.** `pg_dump` dumps the **entire** database
> every run, so each file is a complete standalone copy — the 2:00 dump contains
> *all* data as of 2:00, not just 1:00–2:00's changes. To recover you restore
> **one** file and have the whole database; there is no hourly stitching. Keeping
> the newest `BACKUP_RETAIN` (10) is therefore a ~10-hour rollback history of full
> copies, not accumulating deltas — total storage is bounded (see "Cost" below),
> it does not grow forever. This section covers the `fyredocs` **database**;
> MinIO file bytes are mirrored separately — see "MinIO file backup" below.

> **rclone, not aws-cli.** rclone is a provider-neutral S3 client. There is no
> AWS account involved — it speaks the S3 API to whatever endpoint is configured.

## Configuration (root `.env`)

| Var | Meaning |
|-----|---------|
| `BACKUP_S3_PROVIDER` | rclone S3 provider: `Cloudflare` \| `Backblaze` \| `AWS` \| `Minio` \| `Other` |
| `BACKUP_S3_ENDPOINT` | bucket endpoint URL (e.g. `https://<acct>.r2.cloudflarestorage.com`) |
| `BACKUP_S3_ACCESS_KEY` / `BACKUP_S3_SECRET_KEY` | bucket credentials (e.g. an R2 API token) |
| `BACKUP_S3_BUCKET` | bucket name |
| `BACKUP_S3_REGION` | region (`auto` for R2) |
| `BACKUP_PREFIX` | key prefix (default `postgres/`) |
| `BACKUP_INTERVAL` | seconds between backups (default `3600`) |
| `BACKUP_RETAIN` | how many recent DB dumps to keep (default `10`) |
| `BACKUP_MIN_USERS` | skip the DB backup if `users` rows < this (default `1`; `0` disables) — empty-source protection |
| `BACKUP_MAX_DELETE` | optional: abort a file sync that would delete more than N objects |
| `BACKUP_ALERT_WEBHOOK_URL` | optional: POST an alert here (Slack/Discord/healthchecks) when a guard trips |

**The target MUST be external.** The on-server MinIO is *not* valid disaster
recovery — it dies with the server. Recommended free target: **Cloudflare R2**
(10 GB free, zero egress). Backblaze B2 and Oracle Cloud always-free work
identically.

> **Version note:** dumps are produced by PG18 `pg_dump` and restore cleanly into
> PostgreSQL **18**. Do not target an older-major server (e.g. today's Neon,
> PG17) — a PG18 dump cannot restore into PG17.

## Restore

List available backups:

```sh
docker exec fyredocs-db-backup-1 rclone lsf dest:$BACKUP_S3_BUCKET/postgres/
```

Restore a chosen dump into a target database (streamed, no local temp file):

```sh
docker exec fyredocs-db-backup-1 sh -c \
  'rclone cat dest:$BACKUP_S3_BUCKET/postgres/db-<TS>.sql.gz \
     | gunzip \
     | psql "postgresql://fyredocs:<password>@db:5432/<target-db>?sslmode=disable" \
         --single-transaction -v ON_ERROR_STOP=1'
```

`--single-transaction` makes the restore atomic — on any error the target is left
untouched. For a full recovery onto a fresh box: bring up the `db` service, then
restore into the `fyredocs` database (create it first if needed).

## Verified

The pipeline was validated end-to-end against the on-server MinIO as an
S3 stand-in: dump → upload → **restore into a throwaway PG18 database with
matching row counts** (users, analytics_events, processing_jobs, documents all
equal to live) → retention prune keeps the newest N. For production, the same
mechanism points at an external bucket.

## MinIO file backup

The DB dump covers all database records; the file **bytes** live in MinIO. The
same `db-backup` sidecar also mirrors MinIO buckets offsite each cycle, driven by
`BACKUP_FILES_BUCKETS` (space-separated; empty = DB-only):

- **`outputs uploads`** by default — the converted results (which `documents`
  reference; document-service sets `StoragePath = OutputPath`) **and** the raw
  user inputs, so a job whose input is still in flight when the host is lost can
  be recovered/retried. Set `BACKUP_FILES_BUCKETS=outputs` to skip uploads.
- **Retention is DB-driven, not a bucket lifecycle rule.** `minio-init` clears
  any object-expiry lifecycle on the buckets; input/output objects are deleted by
  job-service's in-process cleanup loop per plan TTL. (An earlier version of this
  doc claimed uploads "auto-expire in 2 days via a bucket lifecycle rule" — that
  is not the case.)

It uses `rclone sync src:<bucket> dest:<bucket>/<BACKUP_FILES_PREFIX><bucket>/`
(default prefix `minio/`), where `src` is the local MinIO (app S3 creds). **Sync
mirrors**: new/changed objects are copied and objects removed from MinIO (expired
/ cleaned by job-service's cleanup loop) are removed from the backup too — so the backup
tracks the live bucket and storage stays ≈ current bucket size. A file-sync
failure is logged but does not flip the DB-backup healthcheck.

> To instead **keep files forever** (retain outputs past the app's own expiry),
> change `rclone sync` to `rclone copy` in `backup.sh` — note storage then grows
> unbounded.

**Registered-users-only mode** (`BACKUP_FILES_REGISTERED_ONLY=true`): back up only
signed-in users' outputs and skip guest jobs. Output keys are `jobs/<jobId>/…`
with no user info, so each cycle the sidecar queries the DB for guest job IDs
(`processing_jobs.user_id IS NULL`) and passes their `jobs/<id>/**` prefixes to
`rclone sync --exclude-from`. The whole bucket stays in scope minus those
prefixes, so **registered files are still fully mirrored and pruned**; guest
files are skipped. If the guest-list query fails, the cycle falls back to a full
sync (a complete registered backup beats none). Note guest outputs are ephemeral
regardless (30-min expiry → pruned within ~1 h), so this is mainly a policy/space
choice.

**Restore files** — mirror the backup prefix back into a (new or live) bucket:

```sh
docker exec fyredocs-db-backup-1 \
  rclone copy dest:$BACKUP_S3_BUCKET/minio/outputs/ src:outputs/
```

## Empty-source protection

If the server dies and comes back with a **wiped/empty** DB or MinIO (lost
volume, fresh schema), a naive backup would destroy the good offsite copy — an
`rclone sync` mirrors the emptiness (wiping the file backup), and an empty
`pg_dump` would enter retention and eventually prune the good dumps. Guards
prevent this:

- **DB**: each cycle the sidecar checks `SELECT count(*) FROM users`. If it's
  below `BACKUP_MIN_USERS` (default 1) — or the query fails — it **skips the DB
  backup entirely** (no dump, no upload, no prune) and alerts. So an empty DB
  never creates a snapshot or prunes a good one; the newest retained snapshot is
  always real data. (`users` is the signal — a fresh DB re-seeds
  `subscription_plans` but never `users`.)
- **Files**: before each `rclone sync`, if the source bucket is **empty but the
  backup still holds objects**, it **skips the sync** (the mirror is preserved)
  and alerts. Optional `BACKUP_MAX_DELETE` additionally aborts a sync that would
  delete more than N objects.
- **Auto-resume**: skipped cycles keep retrying; normal backups resume
  automatically once real data returns. No manual restart needed.
- **Alerting**: each trip logs an `ALERT:` line and, if `BACKUP_ALERT_WEBHOOK_URL`
  is set, POSTs a message (Slack/Discord/healthchecks-style). The container also
  goes **unhealthy** after it stops succeeding (slower backstop).

> **On a real disaster, RESTORE — don't wait.** The guards protect the backup
> from being clobbered, buying you time. Recover the DB/files from the newest
> good snapshot (see Restore above) rather than letting the app keep running on
> empty data.

## Cost & the R2 free tier

Cloudflare R2's "10 GB / month free" is **10 GB-month** — a *standing capacity*
limit, **not** an accumulating monthly credit. You **cannot** bank unused space
(e.g. "50 GB free over 5 months" is wrong). It means you may hold up to 10 GB at
any time for free; if you ever hold more, you pay only for the overage at
**$0.015/GB-month** (billed on average GB stored during the month).

For this workload that essentially never costs anything, because **retention
bounds total storage**:

```
total stored ≈ BACKUP_RETAIN × (one full compressed dump)
             ≈ 10 × ~47 KB ≈ ~0.5 MB today   (≈0.005% of the free 10 GB)
```

Operations are tiny too — a few writes/lists per hour ≈ ~2,200 Class A ops/month
vs the **1,000,000** free (Class B reads: 10M free), and **egress is always $0**,
so restores/downloads cost nothing. Cost would only appear if the database grows
so large that `BACKUP_RETAIN × dump size` exceeds 10 GB — mitigate by lowering
`BACKUP_RETAIN` or using a lifecycle rule (below).

## Deploy rollback (application images)

Separate from data backup: every full `deploy.sh` run tags each built service
image as `fyredocs-<svc>:<git-sha>` and keeps the most recent `IMAGE_TAG_RETAIN`
(default 5). Because the images stay tagged, `docker image prune -f` never
deletes a previous build, so you can roll the application back without rebuilding:

```bash
docker images 'fyredocs-*'            # list retained SHA tags
deployment/deploy.sh --rollback=<sha> # retag <sha> → :latest and recreate the stack
```

This rolls back **code**, not data. A rollback does not undo schema migrations
that a newer version applied (GORM AutoMigrate is additive — it does not drop
columns — so a rollback is generally safe, but verify per change).

## Known limitations & stronger RPO/DR (follow-ups)

- **RPO ≈ 1 hour.** Hourly logical dumps only. For near-zero data loss, add
  continuous **WAL archiving / PITR** (pgBackRest or WAL-G → object storage) or a
  streaming physical replica on a second host. *(Not yet configured.)*
- **Single on-host Postgres volume + single MinIO** are the durability SPOFs;
  the offsite pg_dump + `outputs`/`uploads` mirror is the only copy that survives
  a host loss. Do not `docker compose down -v` (destroys the volumes).
- **No image registry yet.** Rollback images are local to the host. If the host
  itself is lost, images must be rebuilt from source — push SHA-tagged images to
  a registry (GHCR/ECR/R2) to make host-independent rollback/redeploy possible.
- For retention beyond `BACKUP_RETAIN` hourly dumps, keep dailies via an
  offsite-bucket policy rather than growing `BACKUP_RETAIN`.
- The Windows `deploy.bat` twin does not yet mirror the SHA-tagging/rollback
  logic; use `deploy.sh` where rollback matters.
