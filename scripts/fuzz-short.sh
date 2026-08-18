#!/usr/bin/env bash
# Smoke-fuzz every Fuzz* target briefly. Go's fuzzer accepts only ONE -fuzz
# pattern per package invocation, so each target gets its own run — that is
# why this is a script and not a Make one-liner.
#
# FUZZTIME sets the per-target budget (default 10s): long enough to execute
# the corpus plus a short exploration burst, short enough for a PR pipeline.
# Extended sessions stay manual: go test -fuzz=FuzzX -fuzztime=10m ./pkg
#
# Usage:
#   ./scripts/fuzz-short.sh                    # all modules
#   ./scripts/fuzz-short.sh job-service        # scope to module(s)
#   FUZZTIME=30s ./scripts/fuzz-short.sh

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

FUZZTIME="${FUZZTIME:-10s}"
FAIL=0
FOUND=0

for svc in "${TARGETS[@]}"; do
  # grep -l over test files, then dedupe to package dirs: `go test -list`
  # would need a compile of every package first, which is slower than grep
  # for a repo this size.
  while IFS= read -r dir; do
    while IFS= read -r fn; do
      FOUND=1
      echo "--- $dir: $fn (${FUZZTIME}) ---"
      if ! go test -run='^$' -fuzz="^${fn}\$" -fuzztime="$FUZZTIME" "./$dir"; then
        FAIL=1
      fi
    done < <(grep -ho 'func \(Fuzz[A-Za-z0-9_]*\)' "$dir"/*_test.go | sed 's/func //' | sort -u)
  done < <(grep -rl --include='*_test.go' '^func Fuzz' "$svc" 2>/dev/null | xargs -n1 dirname 2>/dev/null | sort -u)
done

if [ $FOUND -eq 0 ]; then
  echo "No fuzz targets found."
fi

exit $FAIL
