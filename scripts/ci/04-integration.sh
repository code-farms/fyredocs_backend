#!/usr/bin/env bash
# Stage 4 — integration-tagged tests against real Postgres/Redis/NATS/MinIO.
# Sources .env (set -a, as deploy.sh does) so shared/database/dbtest and the
# storage tests pick up the stack's credentials; waits for Postgres first so
# a cold stack doesn't produce a wall of connection errors. -p 1 because the
# packages share one test database.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

set -a; [ -f .env ] && . ./.env; set +a

host="${FYREDOCS_TEST_DB_HOST:-localhost:5432}"
for i in $(seq 1 30); do
  if (exec 3<>"/dev/tcp/${host%%:*}/${host##*:}") 2>/dev/null; then
    exec 3>&- 3<&-
    break
  fi
  [ "$i" = 30 ] && { echo "Postgres not reachable at $host — is the essentials stack up? (make test-infra-up)"; exit 1; }
  sleep 2
done

for_each_module go test -tags=integration -race -count=1 -shuffle=on -timeout=10m -p 1
