#!/usr/bin/env bash
# Stage 8 — vulnerability scanning.
#
#   1. govulncheck per module (always). Reports only vulnerabilities whose
#      vulnerable code is reachable from our call graph, so a finding here is
#      actionable, and it fails the stage.
#   2. When syft/trivy are installed and the images exist locally: an SBOM per
#      image (written to $SBOM_DIR) and a Trivy scan failing on HIGH/CRITICAL
#      that have a fixed version (--ignore-unfixed): only findings we can act on
#      gate; unfixed ones (e.g. Debian packages in convert-from-pdf awaiting a
#      fix) stay visible in the SBOM and a full scan. Decided 2026-11-16.
#      Runners without them get a loud WARNING instead of a false green, the
#      same posture as golangci-lint in 02-static.sh.
#
# govulncheck is pinned and run through `go run`, so no global install is
# needed and every runner uses the same version. It queries vuln.go.dev, so the
# stage needs network access.
source "$(dirname "${BASH_SOURCE[0]}")/00-lib.sh"

GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.9.0}"
SBOM_DIR="${SBOM_DIR:-$CI_ROOT/sbom}"

fail=0

echo "=== govulncheck ${GOVULNCHECK_VERSION} ==="
# Not for_each_module: govulncheck refuses to run outside a module directory
# ("no go.mod file") when given ./<svc>/... from the workspace root, so each
# module is scanned from inside it.
for svc in "${SERVICES[@]}"; do
  echo "--- $svc ---"
  (cd "$svc" && go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./...) || fail=1
done

# Images built by 07-build.sh / docker compose, named fyredocs-<service>.
images=()
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  for svc in "${SERVICES[@]}"; do
    [[ "$svc" == shared ]] && continue
    if docker image inspect "fyredocs-$svc:latest" >/dev/null 2>&1; then
      images+=("fyredocs-$svc:latest")
    fi
  done
fi

if [[ ${#images[@]} -eq 0 ]]; then
  echo "WARNING: no fyredocs-* images found (or Docker unavailable) — SBOM and image scan did NOT run."
else
  if command -v syft >/dev/null 2>&1; then
    mkdir -p "$SBOM_DIR"
    for img in "${images[@]}"; do
      echo "--- SBOM $img ---"
      syft "$img" -o "spdx-json=$SBOM_DIR/${img%%:*}.spdx.json" || fail=1
    done
  else
    echo "WARNING: syft not installed — no SBOMs written. Install: https://github.com/anchore/syft (pin the version in the CI image)."
  fi
  if command -v trivy >/dev/null 2>&1; then
    for img in "${images[@]}"; do
      echo "--- trivy $img ---"
      trivy image --quiet --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 "$img" || fail=1
    done
  else
    echo "WARNING: trivy not installed — images were NOT scanned. Install: https://github.com/aquasecurity/trivy (pin the version in the CI image)."
  fi
fi

exit $fail
