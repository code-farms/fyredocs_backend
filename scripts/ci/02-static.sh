#!/usr/bin/env bash
# Stage 2 — static analysis: go vet per module, then golangci-lint per module
# when the binary is present (config: .golangci.yml). The vet loop is the
# non-negotiable floor; golangci-lint is additive so a runner without it still
# gets a meaningful stage rather than a false green — it WARNS loudly when
# skipped.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

static_fail=0
for_each_module go vet || static_fail=1

# Container hardening must survive every compose variant (x-hardened anchor in
# deployment/docker-compose.yml). Needs the docker CLI, not a running daemon.
if command -v docker >/dev/null 2>&1; then
  "$(dirname "${BASH_SOURCE[0]}")/check-compose-hardening.sh" || static_fail=1
else
  echo "WARNING: docker CLI not installed — compose hardening was NOT checked."
fi

if command -v golangci-lint >/dev/null 2>&1; then
  fail=0
  for svc in "${SERVICES[@]}"; do
    echo "--- $svc (golangci-lint) ---"
    (cd "$svc" && golangci-lint run ./...) || fail=1
  done
  exit $(( fail | static_fail ))
else
  echo "WARNING: golangci-lint not installed — staticcheck/errcheck/unused did NOT run."
  echo "Install: https://golangci-lint.run/usage/install/ (pin the version in the CI image)."
fi
exit $static_fail
