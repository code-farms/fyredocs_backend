#!/usr/bin/env bash
# Shared plumbing for the CI stage scripts. Each numbered stage is independent
# and exit-code-clean so any CI system (Argo Workflows, Woodpecker, Jenkins, a
# laptop) can run them as steps — see docs/developer/architecture/ci-pipeline.md.
#
# Not a stage itself: source it.

set -euo pipefail

# Stages may be invoked from anywhere; all commands run from the repo root.
CI_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$CI_ROOT"

# The 12 workspace modules. Root-relative ./... does not work in a go.work
# workspace (CLAUDE.md §10) — every stage loops this list.
SERVICES=(
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

# for_each_module <cmd...> — runs the command with the module path appended
# (as ./<svc>/...) for every module, aggregating failures so one broken
# module doesn't hide the rest.
for_each_module() {
  local fail=0 svc
  for svc in "${SERVICES[@]}"; do
    echo "--- $svc ---"
    if ! "$@" "./$svc/..."; then
      fail=1
    fi
  done
  return $fail
}
