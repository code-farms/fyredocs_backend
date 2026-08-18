#!/usr/bin/env bash
# Stage 2 — static analysis: go vet per module, then golangci-lint per module
# when the binary is present (config: .golangci.yml). The vet loop is the
# non-negotiable floor; golangci-lint is additive so a runner without it still
# gets a meaningful stage rather than a false green — it WARNS loudly when
# skipped.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

for_each_module go vet

if command -v golangci-lint >/dev/null 2>&1; then
  fail=0
  for svc in "${SERVICES[@]}"; do
    echo "--- $svc (golangci-lint) ---"
    (cd "$svc" && golangci-lint run ./...) || fail=1
  done
  exit $fail
else
  echo "WARNING: golangci-lint not installed — staticcheck/errcheck/unused did NOT run."
  echo "Install: https://golangci-lint.run/usage/install/ (pin the version in the CI image)."
fi
