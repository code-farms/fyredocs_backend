# Schema Migrations

Versioned SQL migrations are the **only** authority for the database schema.
The previous GORM `AutoMigrate`-on-every-boot path survives solely as a
development escape hatch (`DB_AUTOMIGRATE=true`) and as the input to the
baseline generator.

> This document covers *schema evolution*. Moving the database between hosts
> (pg_dump/restore) is [db-migration.md](db-migration.md).

## How it works

- **Tool:** [goose](https://github.com/pressly/goose) v3, used as an embedded
  library — no extra container, no CLI on the server, no separate deploy step.
- **Where migrations live:** `<service>/internal/migrations/*.sql`, embedded
  into the binary via `//go:embed` (`embed.go`).
- **When they run:** at service startup, inside `models.Migrate()`, which calls
  `shared/database.RunMigrations(DB, "<service>", migrations.FS)`.
- **History:** one table per service — `goose_db_version_<service>` (e.g.
  `goose_db_version_auth_service`). Services own disjoint tables in the shared
  database, so they own disjoint migration histories too.
- **Concurrency:** every run takes a per-service Postgres **session advisory
  lock**, so `docker compose up --scale <svc>=N` serializes instead of racing
  the same DDL. Different services migrate in parallel (different lock ids).
- **Failure = no startup.** A migration error aborts the process. A service that
  could not apply its schema must not serve traffic. (This also promotes the
  previously warn-only index/constraint statements to fail-fast.)

Services with migrations: `auth-service`, `job-service`, `user-service`,
`document-service`, `notification-service`, `analytics-service`. The four PDF
workers deliberately have none — they read job-service's tables and must never
migrate them.

## Adopting migrations on an existing database

Migration `00001_baseline.sql` captures exactly the schema `AutoMigrate`
produced, written so every statement **converges** the database onto it rather
than assuming anything about the starting state:

- `CREATE TABLE IF NOT EXISTS`, then one `ALTER TABLE … ADD COLUMN IF NOT EXISTS`
  per column;
- `CREATE INDEX IF NOT EXISTS`;
- constraints guarded by a `pg_constraint` lookup plus a
  `duplicate_object / duplicate_table / invalid_table_definition` handler.

The per-column `ALTER`s are not redundant. `CREATE TABLE IF NOT EXISTS` is
idempotent per *table*: on a database whose table was created before the models
gained a column, it does nothing and the column stays missing — then a
`CREATE INDEX` on that column fails with `42703` and the service crash-loops at
boot, since a failed migration is fatal. That is a real outage this project has
had; see `scripts/split_baseline.py`.

That is why **no stamping step is needed**: on an existing production database
run 1 adds only what is missing and records version 1; on a fresh database it
creates the schema.

One asymmetry to be aware of: a column added to an already-populated table
cannot be `NOT NULL` without a default, so the generator emits it nullable. A
fresh database gets the stricter `CREATE TABLE` definition. If a column must be
`NOT NULL` everywhere, backfill it and add the constraint in a *new* numbered
migration.

## Writing a new migration

1. Create `<service>/internal/migrations/0000N_short_snake_case.sql` — numbering
   must be contiguous from 1 (a test enforces this).
2. Write both directions:

```sql
-- +goose Up
ALTER TABLE public.documents ADD COLUMN IF NOT EXISTS archived_at timestamptz;

-- +goose Down
ALTER TABLE public.documents DROP COLUMN IF EXISTS archived_at;
```

3. A statement that itself contains semicolons (a `DO $$ … $$` block, a function
   body) **must** be wrapped:

```sql
-- +goose StatementBegin
DO $$ BEGIN
    ALTER TABLE public.memberships ADD CONSTRAINT chk_role CHECK (role IN ('owner','admin'));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd
```

4. Update the GORM model in the same change — the models still describe the
   schema to the ORM, they just no longer create it.

### Expand / contract discipline

`deploy.sh --rollback=<sha>` puts **old code** back against the **new schema**,
so within a single release migrations must only ever be additive:

| Change | Release N | Release N+1 |
|---|---|---|
| Add a column | add it nullable / with a default | start writing it |
| Rename a column | add the new one, write both | drop the old one |
| Drop a column | stop reading it | drop it |
| Add a constraint | `NOT VALID`, then `VALIDATE` | — |

Adding a `NOT NULL` column with no default, or dropping a column the previous
release still reads, makes rollback impossible.

On large live tables prefer `CREATE INDEX CONCURRENTLY` (note: it cannot run
inside a transaction — use `-- +goose NO TRANSACTION` at the top of the file)
and `ADD CONSTRAINT ... NOT VALID` followed by `VALIDATE CONSTRAINT`, which is
what `00002_enum_check_constraints.sql` does.

## Rolling a migration back

Rollback is a **manual, deliberate** operation — nothing in the deploy path ever
runs `Down`:

```bash
go run github.com/pressly/goose/v3/cmd/goose@latest \
    -dir <service>/internal/migrations \
    -table goose_db_version_<service_with_underscores> \
    postgres "$DATABASE_URL_DIRECT" down
```

The baseline's `Down` is intentionally a no-op: rolling it back would drop every
table the service owns.

## Regenerating / verifying the baseline

```bash
./scripts/gen-migration-baseline.sh          # regenerate the committed baselines
./scripts/gen-migration-baseline.sh --check  # fail if they drifted from the models
```

The script starts a throwaway Postgres, runs each service's legacy
`AutoMigrate` path against it, `pg_dump`s the schema, and splits it per service
by table ownership (`scripts/split_baseline.py`). You normally never need this —
the baselines are committed.

Before changing `split_baseline.py`, run `--check` **first**. It confirms the
committed baselines still match the models, so the regenerated diff contains
only your change and not unrelated model drift swept along with it. The rewrite
rules themselves are unit-tested:

```bash
python3 -m unittest discover -s scripts -p 'test_*.py'
```

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL_DIRECT` | (unset → `DATABASE_URL`) | DSN migrations connect through. Set it to the **direct** Postgres address when the app runs through PgBouncer: session advisory locks are incompatible with transaction pooling. |
| `DB_AUTOMIGRATE` | `false` | Development only. Runs the legacy GORM `AutoMigrate` path instead of migrations, with a loud warning. Never set in production. |
| `DB_MIGRATION_TIMEOUT` | `10m` | Upper bound for a migration run (an index build on a large table legitimately takes minutes). |

## Verifying a deploy

```bash
# history recorded per service
psql "$DATABASE_URL" -c "\dt goose_db_version_*"
psql "$DATABASE_URL" -c "select * from goose_db_version_auth_service order by id desc limit 5"

# second boot is a no-op → "database schema already up to date"
docker compose logs auth-service | grep -i migration

# concurrent replicas serialize on the advisory lock
docker compose up -d --scale user-service=3
```
