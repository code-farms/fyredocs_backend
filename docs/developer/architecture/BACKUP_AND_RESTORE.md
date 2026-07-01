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
3. prunes so only the newest `BACKUP_RETAIN` dumps remain (default **48**)

Backup failures are logged and retried next cycle (the loop never exits). The
container's `HEALTHCHECK` goes **unhealthy** if no backup has succeeded within
~2 intervals, so a silent failure (e.g. bad credentials) shows up in
`docker ps`.

> **rclone, not aws-cli.** rclone is a provider-neutral S3 client. There is no
> AWS account involved — it speaks the S3 API to whatever endpoint is configured.

## Configuration (`deployment/.env`)

| Var | Meaning |
|-----|---------|
| `BACKUP_S3_PROVIDER` | rclone S3 provider: `Cloudflare` \| `Backblaze` \| `AWS` \| `Minio` \| `Other` |
| `BACKUP_S3_ENDPOINT` | bucket endpoint URL (e.g. `https://<acct>.r2.cloudflarestorage.com`) |
| `BACKUP_S3_ACCESS_KEY` / `BACKUP_S3_SECRET_KEY` | bucket credentials (e.g. an R2 API token) |
| `BACKUP_S3_BUCKET` | bucket name |
| `BACKUP_S3_REGION` | region (`auto` for R2) |
| `BACKUP_PREFIX` | key prefix (default `postgres/`) |
| `BACKUP_INTERVAL` | seconds between backups (default `3600`) |
| `BACKUP_RETAIN` | how many recent dumps to keep (default `48`) |

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

## Longer retention & stronger RPO

- For retention beyond `BACKUP_RETAIN` hourly dumps, add a **bucket lifecycle
  rule** (e.g. keep dailies for 30 days) rather than growing `BACKUP_RETAIN`.
- Hourly logical dumps give an **RPO of up to one hour**. If near-zero data loss
  is needed later, upgrade to continuous **WAL archiving / PITR** (pgBackRest or
  WAL-G → object storage) or a streaming physical replica on another host.
