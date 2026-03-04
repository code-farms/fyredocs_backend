#!/usr/bin/env bash
# Run Go tests for esydocs_backend services.
#
# Usage:
#   ./test.sh              # run all services
#   ./test.sh -v           # run all services (verbose)
#   ./test.sh shared       # run only the shared package tests
#   ./test.sh api-gateway  # run only the api-gateway tests
#   ./test.sh -v job-service auth-service   # verbose, multiple services

set -euo pipefail
cd "$(dirname "$0")"

ALL_SERVICES=(
  shared
  api-gateway
  auth-service
  job-service
  convert-to-pdf
  convert-from-pdf
  organize-pdf
  optimize-pdf
  cleanup-worker
)

VERBOSE=""
TARGETS=()

for arg in "$@"; do
  case "$arg" in
    -v|--verbose) VERBOSE="-v" ;;
    -h|--help)
      echo "Usage: $0 [-v|--verbose] [service ...]"
      echo ""
      echo "Services: ${ALL_SERVICES[*]}"
      echo ""
      echo "Examples:"
      echo "  $0                  # test everything"
      echo "  $0 shared           # test shared only"
      echo "  $0 -v api-gateway   # verbose, api-gateway only"
      exit 0
      ;;
    *) TARGETS+=("$arg") ;;
  esac
done

if [ ${#TARGETS[@]} -eq 0 ]; then
  TARGETS=("${ALL_SERVICES[@]}")
fi

FAIL=0
for svc in "${TARGETS[@]}"; do
  echo "--- $svc ---"
  if ! go test $VERBOSE ./"$svc"/...; then
    FAIL=1
  fi
done

if [ $FAIL -ne 0 ]; then
  echo ""
  echo "SOME TESTS FAILED"
  exit 1
fi

echo ""
echo "ALL TESTS PASSED"
