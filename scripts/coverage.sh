#!/usr/bin/env bash
# Per-module coverage for the fyredocs_backend workspace, merged into one
# profile. Runs from the repo root (or anywhere — it cd's to its own parent).
#
# A single `go test -coverprofile ./...` cannot work here: the repo is a
# go.work workspace of 12 sibling modules and root-relative ./... patterns
# fail (CLAUDE.md §10 Step 4). So each module gets its own profile under
# coverage/, and the profiles are concatenated with the duplicate `mode:`
# header lines stripped — `go tool cover` accepts exactly one.
#
# Usage:
#   ./scripts/coverage.sh                 # all modules
#   ./scripts/coverage.sh job-service     # one or more modules
#   COVER_TAGS=integration ./scripts/coverage.sh   # include tagged tests

set -euo pipefail
cd "$(dirname "$0")/.."

ALL_SERVICES=(
  shared
  api-gateway
  auth-service
  job-service
  convert-to-pdf
  convert-from-pdf
  organize-pdf
  optimize-pdf
  analytics-service
  document-service
  user-service
  notification-service
)

TARGETS=("$@")
if [ ${#TARGETS[@]} -eq 0 ]; then
  TARGETS=("${ALL_SERVICES[@]}")
fi

# Integration profiles must not clobber the unit ones, so tagged runs are
# kept apart by a filename suffix.
TAGS="${COVER_TAGS:-}"
TAG_ARGS=()
SUFFIX=""
if [ -n "$TAGS" ]; then
  TAG_ARGS=(-tags="$TAGS" -p 1)
  SUFFIX=".$TAGS"
fi

mkdir -p coverage

FAIL=0
for svc in "${TARGETS[@]}"; do
  echo "--- $svc (cover) ---"
  # ${arr[@]+...} keeps bash 3.2's set -u happy when TAG_ARGS is empty.
  if ! go test ${TAG_ARGS[@]+"${TAG_ARGS[@]}"} -count=1 -covermode=atomic \
      -coverprofile="coverage/$svc$SUFFIX.out" "./$svc/..."; then
    FAIL=1
  fi
done

MERGED="coverage/merged$SUFFIX.out"
# First profile's mode line survives; every later one is a duplicate header.
first=1
: > "$MERGED"
for svc in "${TARGETS[@]}"; do
  profile="coverage/$svc$SUFFIX.out"
  [ -s "$profile" ] || continue
  if [ $first -eq 1 ]; then
    cat "$profile" >> "$MERGED"
    first=0
  else
    tail -n +2 "$profile" >> "$MERGED"
  fi
done

if [ -s "$MERGED" ]; then
  echo ""
  echo "=== merged coverage ($MERGED) ==="
  go tool cover -func="$MERGED" | tail -1
  echo "Per-function detail: go tool cover -func=$MERGED"
  echo "HTML report:        go tool cover -html=$MERGED"
fi

exit $FAIL
