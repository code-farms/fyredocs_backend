#!/usr/bin/env bash
# run.sh — runs the bench-engine harness inside fyredocs-test-binaries, where the
# oracle tools live (Poppler, LibreOffice, veraPDF), attached to the stack's
# Docker network so it reaches the edge as a browser would.
#
#   scripts/bench-engine/run.sh gen                 # build the corpus (once)
#   scripts/bench-engine/run.sh verify              # check it is unchanged
#   scripts/bench-engine/run.sh run [-runs 3] [-series ...] [-tools ...]
#   scripts/bench-engine/run.sh compare a.json b.json
#
# The stack must be up with scripts/bench-engine/compose.bench.yml (raised rate
# limits). The Docker socket is mounted so the harness can sample worker
# container stats; with -startup it also restarts the workers to time cold
# starts. Set BENCH_NETWORK if the stack's network is not fyredocs_net.
# Only the repo is mounted, so -results must be a path inside it.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

docker build -q -f deployment/test-binaries.Dockerfile -t fyredocs-test-binaries deployment >/dev/null

net_args=()
if [[ "${1:-}" == "run" ]]; then
  net_args=(--network "${BENCH_NETWORK:-fyredocs_net}" -v /var/run/docker.sock:/var/run/docker.sock)
  # Label the report with this machine, not the container's ID; a -host the
  # caller passes comes later and wins.
  set -- run -host "$(hostname -s)" "${@:2}"
fi

exec docker run --rm ${net_args[@]+"${net_args[@]}"} \
  -v "$PWD":/src -w /src \
  -v fyredocs-test-gomod:/go/pkg/mod -v fyredocs-test-gocache:/root/.cache/go-build \
  -e HOME=/tmp \
  fyredocs-test-binaries go run ./scripts/bench-engine "$@"
