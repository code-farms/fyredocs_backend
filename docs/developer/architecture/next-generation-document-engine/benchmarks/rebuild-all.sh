#!/bin/bash
# Rebuild every Fyredocs image from HEAD and record per-image build time.
# Output: build log + a TSV of (image, seconds, status).
set -u
cd /Users/shivam/Desktop/projects/fyredocs/fyredocs_backend
LOG="${1:-raw/build.log}"; TSV="${2:-raw/build-times.tsv}"
: > "$TSV"
export GO_BUILD_PARALLELISM=4   # 4 GB Docker VM: leave headroom
export DOCKER_BUILDKIT=1
t() { # name, cmd...
  local name="$1"; shift
  local s=$(date +%s)
  echo "### [$(date '+%H:%M:%S')] BUILD $name: $*" >> "$LOG"
  if "$@" >> "$LOG" 2>&1; then st=ok; else st=FAIL; fi
  local e=$(date +%s)
  printf '%s\t%s\t%s\n' "$name" "$((e-s))" "$st" >> "$TSV"
  echo "### [$(date '+%H:%M:%S')] DONE $name ($((e-s))s, $st)" >> "$LOG"
}
source deployment/scripts/gomod-fingerprint.sh
GOMOD_HASH="$(gomod_fingerprint)"
t fyredocs-base docker build -f deployment/base-alpine-libreoffice.Dockerfile -t fyredocs-base:latest .
t fyredocs-go-builder docker build -f deployment/base-go-builder.Dockerfile \
    --build-arg GO_BUILD_PARALLELISM="$GO_BUILD_PARALLELISM" --build-arg GOMOD_HASH="$GOMOD_HASH" \
    -t fyredocs-go-builder:latest .
for svc in api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf analytics-service document-service user-service notification-service ai-service; do
  t "$svc" docker compose -f deployment/docker-compose.yml --env-file .env build --build-arg GO_BUILD_PARALLELISM="$GO_BUILD_PARALLELISM" "$svc"
done
t test-binaries docker build -f deployment/test-binaries.Dockerfile -t fyredocs-test-binaries:latest .
# Tag the fresh set as the baseline
for img in fyredocs-base fyredocs-go-builder fyredocs-api-gateway fyredocs-auth-service fyredocs-job-service fyredocs-convert-from-pdf fyredocs-convert-to-pdf fyredocs-organize-pdf fyredocs-optimize-pdf fyredocs-analytics-service fyredocs-document-service fyredocs-user-service fyredocs-notification-service fyredocs-ai-service fyredocs-test-binaries; do
  docker tag "$img:latest" "$img:baseline-2026-11" >> "$LOG" 2>&1 || true
done
echo "### ALL DONE $(date)" >> "$LOG"
