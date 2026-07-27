#!/bin/bash

# Build the shared base images used by every service:
#   - fyredocs-base       : Alpine + LibreOffice/poppler/unoserver (runtime for convert-to-pdf)
#   - fyredocs-go-builder : golang toolchain + all module deps + a WARM go-build cache
#                           (build stage every service FROM-s)
#
# Rebuild these ONLY when their inputs change:
#   - fyredocs-base       : Alpine / LibreOffice / unoserver versions
#   - fyredocs-go-builder : any service go.mod/go.sum, or the Go toolchain version
#
# Day-to-day deploys reuse the warm cache and do NOT rebuild these. deploy.sh builds
# fyredocs-base only when it's missing, and fyredocs-go-builder when it's missing OR
# when its baked `fyredocs.gomod-hash` label no longer matches the repo's go.work /
# go.mod / go.sum files. So a dependency change needs no manual step — run this script
# for the things the hash can't see, i.e. a Go toolchain or Alpine/LibreOffice bump.

set -e

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
cd "$ROOT_DIR"

# Match the compile parallelism used by the service builds (leave cores for the
# daemon/OS). Override: GO_BUILD_PARALLELISM=4 ./deployment/build-base-image.sh
GO_BUILD_PARALLELISM="${GO_BUILD_PARALLELISM:-6}"

# gomod_fingerprint(): hash of go.work + every go.mod/go.sum, stamped onto the
# go-builder image as the `fyredocs.gomod-hash` label. deploy.sh recomputes it and
# rebuilds the base automatically when it drifts, so this MUST be passed here too —
# otherwise the image would be labelled "unknown" and every deploy would consider it
# stale and rebuild it again.
# shellcheck source=scripts/gomod-fingerprint.sh
source "$SCRIPT_DIR/scripts/gomod-fingerprint.sh"
GOMOD_HASH="$(gomod_fingerprint)"

echo "Building fyredocs-base image with LibreOffice..."
echo ""
docker build \
    -f deployment/base-alpine-libreoffice.Dockerfile \
    -t fyredocs-base:latest \
    .

echo ""
echo "Building fyredocs-go-builder image (Go deps + warm build cache, -p ${GO_BUILD_PARALLELISM})..."
echo "This is the slow one-time step; day-to-day service builds reuse its warm cache."
echo ""
docker build \
    -f deployment/base-go-builder.Dockerfile \
    --build-arg GO_BUILD_PARALLELISM="${GO_BUILD_PARALLELISM}" \
    --build-arg GOMOD_HASH="${GOMOD_HASH}" \
    -t fyredocs-go-builder:latest \
    .

echo ""
echo "Base images built successfully!"
echo ""
echo "To test them locally:"
echo "  docker run --rm fyredocs-base:latest libreoffice --version"
echo "  docker run --rm fyredocs-go-builder:latest ls /root/.cache/go-build"
echo ""
echo "Now you can run ./deployment/deploy.sh to build all services."
echo ""
