#!/usr/bin/env bash
# Seed a pool of load-test users and promote them to the `pro` plan.
#
# WHY THIS EXISTS:
#   PUT /auth/plan is now admin/super-admin only and changes only the caller's
#   OWN plan, so k6 users cannot self-upgrade. The plan must be set at the DB
#   level. This script signs up a fixed pool via the public API (so password
#   hashing / user rows are created exactly as the app expects) and then flips
#   their plan_name to `pro` directly in the auth-service database.
#
# USAGE:
#   BASE_URL=http://localhost:8080 \
#   AUTH_DATABASE_URL='postgres://user:pass@localhost:5432/auth?sslmode=disable' \
#   ./seed-pro-users.sh
#
# Then run k6 pointing at the seeded pool (login-only, no self-upgrade):
#   USER_EMAIL_PREFIX=k6pool USER_POOL_SIZE=10 USER_PASSWORD='LoadTest!23456' \
#   k6 run scenarios/upload-heavy.js
#
# ENV:
#   BASE_URL            gateway base URL           (default http://localhost:8080)
#   AUTH_DATABASE_URL   auth-service Postgres DSN  (required; same as its DATABASE_URL)
#   USER_EMAIL_PREFIX   local-part prefix          (default k6pool)
#   SEED_EMAIL_DOMAIN   email domain               (default loadtest.fyredocs.local)
#   USER_POOL_SIZE      number of users            (default 10)
#   USER_PASSWORD       shared password (>=8 chars)(default LoadTest!23456)
#   PLAN_NAME           plan to grant              (default pro)
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
PREFIX="${USER_EMAIL_PREFIX:-k6pool}"
DOMAIN="${SEED_EMAIL_DOMAIN:-loadtest.fyredocs.local}"
POOL_SIZE="${USER_POOL_SIZE:-10}"
PASSWORD="${USER_PASSWORD:-LoadTest!23456}"
PLAN_NAME="${PLAN_NAME:-pro}"

if [[ -z "${AUTH_DATABASE_URL:-}" ]]; then
  echo "ERROR: AUTH_DATABASE_URL is required (the auth-service Postgres DSN)." >&2
  echo "       e.g. postgres://user:pass@localhost:5432/auth?sslmode=disable" >&2
  exit 1
fi
command -v curl >/dev/null || { echo "ERROR: curl not found" >&2; exit 1; }
command -v psql >/dev/null || { echo "ERROR: psql not found" >&2; exit 1; }

echo "Seeding ${POOL_SIZE} users (${PREFIX}+1..${POOL_SIZE}@${DOMAIN}) as '${PLAN_NAME}'"
echo "  gateway: ${BASE_URL}"

# 1) Sign up each user via the public API (idempotent: 409 = already exists).
for i in $(seq 1 "${POOL_SIZE}"); do
  email="${PREFIX}+${i}@${DOMAIN}"
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE_URL}/auth/signup" \
    -H 'Content-Type: application/json' \
    -d "$(printf '{"email":"%s","password":"%s","fullName":"K6 Pool %s","country":"India","phone":"+910000000000"}' \
      "${email}" "${PASSWORD}" "${i}")")
  case "${code}" in
    200|201) echo "  signup ${email}: created" ;;
    409|400) echo "  signup ${email}: exists (${code})" ;;
    *)       echo "  signup ${email}: WARN ${code}" >&2 ;;
  esac
done

# 2) Promote the whole pool to `pro` directly in the auth DB.
#    (Single UPDATE keyed by the email pattern; requires the `pro` plan to be
#     seeded — auth-service seedPlans() does this on migrate.)
pattern="${PREFIX}+%@${DOMAIN}"
echo "Promoting users LIKE '${pattern}' to plan_name='${PLAN_NAME}'"
updated=$(psql "${AUTH_DATABASE_URL}" -qtAX \
  -v plan="${PLAN_NAME}" -v pat="${pattern}" <<'SQL'
UPDATE users SET plan_name = :'plan' WHERE email LIKE :'pat';
SELECT COUNT(*) FROM users WHERE email LIKE :'pat' AND plan_name = :'plan';
SQL
)
echo "Done — ${updated} user(s) now on '${PLAN_NAME}'."
echo
echo "Run k6 with:"
echo "  USER_EMAIL_PREFIX=${PREFIX} USER_POOL_SIZE=${POOL_SIZE} USER_PASSWORD='${PASSWORD}' \\"
echo "  SEED_EMAIL_DOMAIN=${DOMAIN} k6 run scenarios/upload-heavy.js"
