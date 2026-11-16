#!/usr/bin/env bash
# Stage 9 — engine benchmark gate (nightly / release tags only, never on PRs:
# it needs the full stack and the oracles image, and a full run takes long).
#
#   1. corpus: generate if there is no lock, then verify it is unchanged;
#   2. run a quick slice of the corpus through the stack (override with
#      BENCH_ARGS), writing a report;
#   3. compare it with the newest committed baseline report, failing on any
#      regression (slower p50 beyond 10 %, a differential check that now fails,
#      an SSIM drop over 0.02, a job that now fails).
#
# The stack must already be up with scripts/bench-engine/compose.bench.yml.
# Design: docs/developer/architecture/next-generation-document-engine/
# 16-benchmarking-and-testing.md §5.8.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

RESULTS=docs/developer/architecture/next-generation-document-engine/benchmarks/results
BENCH_ARGS="${BENCH_ARGS:--limit 2}"

if [[ ! -f scripts/bench-engine/corpus/out/corpus.lock ]]; then
  bash scripts/bench-engine/run.sh gen
fi
bash scripts/bench-engine/run.sh verify

# shellcheck disable=SC2086  # BENCH_ARGS is a flag list on purpose
bash scripts/bench-engine/run.sh run $BENCH_ARGS

latest=$(ls -t "$RESULTS"/bench-engine-*.json | head -1)
baseline=$(ls -t "$RESULTS"/baseline-*.json 2>/dev/null | head -1 || true)
if [[ -z "$baseline" ]]; then
  echo "WARNING: no committed baseline report (results/baseline-*.json); nothing to compare against."
  exit 0
fi
bash scripts/bench-engine/run.sh compare "$baseline" "$latest"
