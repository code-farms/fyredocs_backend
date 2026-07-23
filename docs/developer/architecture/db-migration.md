# Database Migration — moving Postgres to a dedicated host (no data loss)

**A step-by-step runbook for the day you move the co-located Postgres onto its
own machine — the DB step of scaling to a second server.**

Today Postgres runs as a **co-located container** on the app host (`db` in
`deployment/docker-compose.yml`), reachable only on the internal `fyredocs_net`
Docker network (`DATABASE_URL=…@db:5432/fyredocs?sslmode=disable`). Every Go
service points at that one DSN.

When you add a **second app server**, the app tier scales freely — the services
are stateless (state lives in Postgres/Redis/NATS/MinIO). But **Postgres is the
source of truth and there must stay exactly one of it.** You do *not* run a second
Postgres; you move the one Postgres onto a **dedicated DB host** that both app
servers point at.

> **This document is the DB-migration mechanics only.** For the surrounding
> scale-out sequence (private network, infra auth/TLS, worker fan-out, HA tier)
> see [horizontal-scaling.md](./horizontal-scaling.md) §2 (second host) and §3 (HA
> stateful tier). For the backup pipeline this runbook leans on, see
> [backup-and-restore.md](./backup-and-restore.md). Capacity/SPOF *why* is in
> [production-readiness.md](./production-readiness.md).

> **Status: reference for future scaling. Nothing here is applied today.** Execute
> it only when you actually move the DB. Until then it is a study/checklist doc.

Two runbooks are given:

- **Runbook A — Cold cutover (dump/restore).** Recommended. Zero data loss with a
  short maintenance window (~15–60 min). Do this one unless a window is
  unacceptable.
- **Runbook B — Near-zero-downtime (logical replication).** Seconds of downtime,
  zero data loss, more moving parts. Study it; use it only when the window in A is
  too costly.

---

## Target architecture

```
                 ┌──────────────┐        ┌──────────────┐
   Server-1 ────▶│ app + workers│        │ app + workers│◀──── Server-2
   (Caddy edge)  │  (Go svcs)   │        │  (Go svcs)   │
                 └──────┬───────┘        └──────┬───────┘
                        │   private network (TLS)   │
                        └────────────┬──────────────┘
                                     ▼
                           ┌────────────────────┐
                           │  Dedicated DB host │  PostgreSQL 18
                           │  (fyredocs DB)     │  + db-backup → R2
                           └────────────────────┘
```

App servers become truly stateless and scale to N. The single Postgres lives on
its own box behind a private network + TLS. **No application code changes** — the
DSN builder (`shared/config/postgres_dsn.go`) already accepts a remote/managed
DSN; you only change `DATABASE_URL`.

---

## Read this first — gotchas that apply to both runbooks

These are the things that actually cause data loss, corruption, or an outage if
skipped. They are not optional.

### 1. Move schema with `pg_dump`, never "let AutoMigrate re-create it"

The services build their schema with GORM `AutoMigrate`, but **some indexes are
hand-managed outside AutoMigrate** — see
`document-service/internal/models/database.go`: a `search_vector` **GIN** index
and several **partial-unique** indexes (`idx_doc_search`,
`idx_doc_user_source_job … WHERE source_job_id IS NOT NULL`, `idx_tag_personal`,
`idx_tag_org`). `pg_dump` reproduces the *entire* schema — tables, data, those
indexes, constraints, defaults, sequences — in one shot. AutoMigrate does **not**
move data and can miss those extra objects. So the migration path is always
**dump → restore**. After restore, services boot and their AutoMigrate is an
idempotent no-op against the already-correct schema.

> **Single migrator per shared table.** Where two services share a physical table,
> exactly one of them owns the schema. In particular, **only job-service** runs
> `AutoMigrate` on `processing_jobs` / `file_metadata`; the four PDF workers
> (convert-to-pdf, convert-from-pdf, organize-pdf, optimize-pdf) **no longer
> AutoMigrate** those tables — they only read/update `ProcessingJob` and write their
> own output `FileMetadata` rows. This removes concurrent DDL races on those tables at
> stack start (the workers can only ever run against jobs job-service already created).

### 2. Connection-pool budget — raise `max_connections` (or add PgBouncer) *before* Server-2

Default per-service pools sum to **~275 potential connections** against the
current `max_connections=200` (see
[production-readiness.md](./production-readiness.md) §5.6 and
[horizontal-scaling.md](./horizontal-scaling.md) §2, step 3). One app host already
lives within that only because not every pool is full at once. **A second app host
multiplies the pools** and will blow past 200. On the dedicated host you must
either:

- raise `max_connections` well above 200 (the box now has RAM to spare — it no
  longer shares the VPS with LibreOffice/OCR/Ghostscript), **and/or**
- put **PgBouncer** (transaction mode) in front of Postgres to multiplex — the
  preferred fix in the existing docs, and it doubles as the HA-tier pooler.

This is a hard prerequisite for adding Server-2, not a follow-up.

### 3. Security — flip `sslmode=disable` → `require`/`verify-full`

`sslmode=disable` is only safe because the DB is a localhost Docker hop today. Once
Postgres is on the network:

- `ssl = on` on the DB with a cert; app DSN `sslmode=require` (or `verify-full`
  with the CA pinned).
- `pg_hba.conf`: only the app servers' **private IPs**, `scram-sha-256`, over
  `hostssl`.
- Firewall: 5432 open **only** to those private IPs. **Never** expose Postgres to
  the public internet.
- Keep both hosts on a private network / VPC / WireGuard / Tailscale
  ([horizontal-scaling.md](./horizontal-scaling.md) §2).

### 4. Re-point the backup sidecar at the new host

The `db-backup` sidecar (`deployment/backup/backup.sh`) currently `pg_dump`s the
co-located `db`. After the move it must target the dedicated host (its DSN /
pg_dump host), so the hourly dump → rclone → R2 keeps running. Keep the
`BACKUP_MIN_USERS` empty-source guard on. See
[backup-and-restore.md](./backup-and-restore.md).

### 5. Version parity

The dedicated host must run **PostgreSQL 18** (same major). A PG18 dump does not
restore into PG17 or earlier.

### 6. Scope — this is Postgres only

Redis, NATS, and MinIO are also co-located. When Server-2 truly joins they need
their own "single shared instance, reachable + secured" decisions
([horizontal-scaling.md](./horizontal-scaling.md) §2/§3). This runbook does not
cover them.

---

## Runbook A — Cold cutover (dump/restore) · RECOMMENDED

**Data loss: zero** (no writes happen during the window). **Downtime: ~15–60 min**
depending on DB size. The old co-located `db` is left **untouched** until the new
host is confirmed, so rollback is instant and lossless.

### A0 · Pre-flight (before the window — no downtime)

1. **Provision the dedicated host.** Install **PostgreSQL 18**. Create the
   `fyredocs` database and `fyredocs` user with the **same credentials** as the
   current `.env`.
2. **Tune `postgresql.conf`** on the new box (it's a dedicated DB machine now):
   - `listen_addresses` = the private IP (never `0.0.0.0`)
   - `max_connections` **> 200** (see gotcha #2), or plan PgBouncer
   - `shared_buffers` / `effective_cache_size` / `work_mem` — raise above the
     cramped co-located values (`256MB` etc.) since the box is DB-only
   - `ssl = on` + certificate
3. **Lock down access:** `pg_hba.conf` `hostssl fyredocs fyredocs <srv-ip>/32
   scram-sha-256` per app server; firewall 5432 to those IPs only (gotcha #3).
4. **Take a fresh safety backup.** The hourly R2 dump exists, but take an explicit
   one:
   ```sh
   docker compose exec db pg_dump -U "$POSTGRES_USER" \
     --no-owner --no-privileges -Fc fyredocs > fyredocs_premigration.dump
   ```

### A1 · Maintenance window (downtime starts)

5. **Quiesce writes** — stop the app + worker services, **leave `db` running** so
   it can be dumped:
   ```sh
   docker compose stop \
     api-gateway auth-service user-service job-service document-service \
     analytics-service notification-service \
     convert-from-pdf convert-to-pdf organize-pdf optimize-pdf
   ```
   (Optional: put Caddy on a maintenance page instead of a hard stop.)
6. **Dump and restore** into the new host (custom format → `pg_restore`):
   ```sh
   docker compose exec -T db pg_dump -U "$POSTGRES_USER" \
     --no-owner --no-privileges -Fc fyredocs > fyredocs_cutover.dump

   pg_restore --no-owner --no-privileges --clean --if-exists \
     -h <NEW_DB_HOST> -U fyredocs -d fyredocs fyredocs_cutover.dump
   ```
   `--no-owner --no-privileges` matches the backup pipeline; `pg_dump` carries the
   full schema incl. the hand-managed GIN/partial-unique indexes (gotcha #1).
7. **Verify on the new host** — row counts match source for the key tables, and
   indexes exist:
   ```sql
   SELECT count(*) FROM users;
   SELECT count(*) FROM documents;
   SELECT count(*) FROM processing_jobs;
   SELECT count(*) FROM analytics_events;
   \d+ documents      -- confirm idx_doc_search (GIN) + partial-unique indexes
   ```
   (Same table set the backup pipeline validates against — see
   [backup-and-restore.md](./backup-and-restore.md) "Verified".)

### A2 · Cutover

8. **Re-point `DATABASE_URL`** on Server-1 at the new host, with TLS:
   ```
   DATABASE_URL=postgresql://fyredocs:<pw>@<NEW_DB_HOST>:5432/fyredocs?sslmode=require
   ```
9. **Re-point `db-backup`** at the new host (gotcha #4).
10. **Restart the app services.** Each boots, runs its idempotent AutoMigrate
    (no-op), and connects. Expect "Database connection established"
    (`shared/database/database.go`) and green healthchecks.
11. **End-to-end verify** (see the checklist at the bottom): login → upload → one
    conversion job → download output → document appears in the list. This
    exercises Postgres read *and* write across services.

### A3 · Cleanup & rollback

- **Keep the old `db` stopped, do NOT delete its `postgres_data` volume**, until
  the new host is proven. If anything is wrong, flip `DATABASE_URL` back to
  `db:5432` and restart — instant, lossless rollback (the old DB received no
  writes after the dump).
- Once confident: remove (or leave stopped) the `db` service. **Never run
  `down -v` / `docker volume rm` on the stack** — that destroys the volume (the
  compose file warns about exactly this).
- **Adding Server-2 later:** give its stack the same `DATABASE_URL`, and add its
  private IP to `pg_hba.conf` + firewall. No other DB step.

---

## Runbook B — Near-zero-downtime via logical replication (study reference)

**Downtime: seconds. Data loss: zero. Complexity: higher.** Uses PostgreSQL 18
**native logical replication** (no extension). Idea: make the dedicated Postgres a
**live logical replica** of the co-located one; when replication lag reaches ~0,
quiesce writes for a few seconds and flip `DATABASE_URL`.

### B0 · Prerequisites

- Both sides PostgreSQL 18.
- **Every table needs a primary key / replica identity.** GORM models carry an
  `id` PK (fine); **verify link/association tables** (e.g. memberships, tags) also
  have a PK/unique replica identity, or their UPDATE/DELETE won't replicate.
- Source needs `wal_level = logical`, which requires a **Postgres restart**.
  Schedule this small restart *well ahead* of cutover day so cutover itself needs
  no restart. Also ensure `max_replication_slots` and `max_wal_senders` are ≥ 1–2.

### B1 · Prepare source (co-located `db`)

```sql
-- postgresql.conf: wal_level = logical    (restart required — pre-scheduled)
CREATE PUBLICATION fyredocs_pub FOR ALL TABLES;
```

### B2 · Create schema on the target first (data-less)

The subscription needs the tables to already exist:

```sh
docker compose exec -T db pg_dump -U "$POSTGRES_USER" \
  --schema-only --no-owner --no-privileges fyredocs > schema.sql
psql -h <NEW_DB_HOST> -U fyredocs -d fyredocs -f schema.sql
```

### B3 · Subscribe on the target (initial copy + streaming)

```sql
CREATE SUBSCRIPTION fyredocs_sub
  CONNECTION 'host=<OLD_DB_PRIVATE_IP> dbname=fyredocs user=fyredocs password=<pw> sslmode=require'
  PUBLICATION fyredocs_pub;
```

This performs the initial full data copy, then streams changes.

### B4 · Watch lag until caught up

```sql
-- on the target:
SELECT subname, received_lsn, latest_end_lsn FROM pg_stat_subscription;

-- on the source (slot lag in bytes):
SELECT slot_name,
       pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn)) AS lag
FROM pg_replication_slots;
```

Wait until lag is ~0. **Freeze schema during this window** — DDL does **not**
replicate (gotcha B-ii), so hold any deploy that would trigger a new AutoMigrate.

### B5 · Cutover (seconds of downtime)

a. **Stop app/worker services** (writes stop) — same `docker compose stop` list as
   Runbook A step 5.
b. **Drain the last LSN:** confirm lag = 0 on the source slot.
c. **Sync sequences — mandatory.** Logical replication **does not copy sequence
   values**. If you skip this, the target's `id` sequences start low and the first
   inserts collide on the primary key. Set each sequence to the source max before
   flipping. Generate the statements from the source and run on the target:
   ```sql
   -- generate on SOURCE, run output on TARGET:
   SELECT format('SELECT setval(%L, (SELECT COALESCE(max(id),1) FROM %I));',
                 pg_get_serial_sequence(quote_ident(t.table_name), 'id'),
                 t.table_name)
   FROM information_schema.tables t
   WHERE t.table_schema = 'public'
     AND pg_get_serial_sequence(quote_ident(t.table_name), 'id') IS NOT NULL;
   ```
d. **Flip `DATABASE_URL`** to the new host (`sslmode=require`) and restart
   services.
e. **End-to-end verify** (checklist below) — pay special attention to creating a
   **new** record to prove the sequence sync worked (no PK collision).

### B6 · Cleanup

```sql
-- target:
DROP SUBSCRIPTION fyredocs_sub;   -- also drops the remote replication slot
-- source:
DROP PUBLICATION fyredocs_pub;
```

### Runbook B — critical gotchas (don't skip)

- **(i) Sequences don't replicate** → `setval` at cutover (B5c). This is the #1
  cause of a broken logical-replication cutover.
- **(ii) DDL doesn't replicate** → freeze schema for the whole replication window;
  block AutoMigrate-triggering deploys.
- **(iii) Replica identity** required on every table for UPDATE/DELETE (B0).
- **(iv)** Size `max_replication_slots` / `max_wal_senders` on the source.

> **Related but different: RPO, not migration.** For ongoing near-zero *data loss*
> in steady state (WAL archiving / PITR, or a streaming physical replica), see
> [backup-and-restore.md](./backup-and-restore.md) "Longer retention & stronger
> RPO" and [horizontal-scaling.md](./horizontal-scaling.md) §3. Logical
> replication here is a one-time **migration** tool, not the HA design.

---

## Post-migration verification checklist (either runbook)

- [ ] **Connectivity:** every service logs "Database connection established"; no
      ping timeouts; healthchecks green.
- [ ] **Integrity:** row counts on the new host match source for `users`,
      `documents`, `processing_jobs`, `analytics_events`; `\d+ documents` shows the
      GIN + partial-unique indexes.
- [ ] **App flow (through Caddy):** login → upload (`/uploads/*` → MinIO) → trigger
      one PDF conversion → job completes → download output (`/outputs/*`) → the new
      document appears in the list.
- [ ] **New write is safe:** create a fresh record and confirm **no PK collision**
      (the decisive check for Runbook B's sequence sync).
- [ ] **Backup:** `db-backup` produces its first successful dump against the new
      host to R2 (healthcheck green within ~2 intervals).
- [ ] **Rollback drill (optional):** flip `DATABASE_URL` back to the old `db`,
      confirm fallback works, then return to the new host.

---

## See also

- [horizontal-scaling.md](./horizontal-scaling.md) — §2 second host (private
  network, infra auth/TLS), §3 HA stateful tier (managed Postgres + PgBouncer).
- [backup-and-restore.md](./backup-and-restore.md) — the `db-backup` pipeline,
  restore mechanics, RPO options this runbook reuses.
- [database.md](./database.md) — connection pooling, DSN defaults, schema
  ownership.
- [production-readiness.md](./production-readiness.md) — §5.6 pool budget, §6
  capacity math, SPOF analysis.
