#!/usr/bin/env bash
# Run a Fyredocs k6 scenario with sane defaults + JSON/HTML output.
#
# Usage:
#   ./run.sh <scenario> [profile] [-- extra k6 args/-e overrides]
#
#   scenario : smoke | browse | auth-churn | mixed-realistic | spike | soak
#              convert-to-pdf | convert-from-pdf | organize-pdf | optimize-pdf
#              upload-heavy
#   profile  : vps40 (default) | laptop
#
# Env:
#   BASE_URL   target gateway origin (default http://localhost:8080)
#   PROFILE    overrides the positional profile
# Examples:
#   BASE_URL=https://app.example.com ./run.sh smoke
#   ./run.sh mixed-realistic vps40
#   ./run.sh optimize-pdf vps40 -- -e JOB_RATE=40        # push a tool's rate
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SCENARIO="${1:?usage: run.sh <scenario> [profile] [-- extra k6 args]}"
shift || true
PROFILE_ARG="${PROFILE:-vps40}"
if [ "${1:-}" != "" ] && [ "${1:-}" != "--" ]; then PROFILE_ARG="$1"; shift || true; fi
[ "${1:-}" = "--" ] && shift || true   # drop the -- separator

FILE="$DIR/scenarios/${SCENARIO}.js"
if [ ! -f "$FILE" ]; then
  echo "unknown scenario '$SCENARIO'. available:"; ls "$DIR/scenarios" | sed 's/\.js$//' | sed 's/^/  /'
  exit 1
fi

if ! command -v k6 >/dev/null 2>&1; then
  echo "k6 not found. Install: https://grafana.com/docs/k6/latest/set-up/install-k6/"
  echo "  macOS: brew install k6   |   Debian/Ubuntu: see docs (apt repo)"
  exit 1
fi

# Ensure fixtures exist
if [ ! -d "$DIR/fixtures/out" ]; then
  echo "fixtures missing — generating..."; bash "$DIR/fixtures/generate.sh" all
fi

BASE_URL="${BASE_URL:-http://localhost:8080}"
mkdir -p "$DIR/results"
TS="$(date +%Y%m%d-%H%M%S)"
JSON="$DIR/results/${SCENARIO}-${TS}.json"
HTML="$DIR/results/${SCENARIO}-${TS}.html"

echo "scenario=$SCENARIO profile=$PROFILE_ARG base=$BASE_URL"
K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT="$HTML" \
  k6 run -e BASE_URL="$BASE_URL" -e PROFILE="$PROFILE_ARG" \
  --summary-export "$JSON" "$@" "$FILE"

echo ""
echo "JSON summary: $JSON"
echo "HTML report:  $HTML"
