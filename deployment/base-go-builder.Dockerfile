# fyredocs-go-builder: shared Go build base for every service.
#
# Bakes into the IMAGE LAYERS (not a BuildKit cache mount, so it survives builder
# prunes and fresh machines):
#   - the full module cache (/go/pkg/mod)
#   - a warm Go build cache (/root/.cache/go-build) for the whole dependency graph
#   - the workspace blueprint (go.work + every service go.mod) and shared/ source
#
# Each service Dockerfile does `FROM fyredocs-go-builder:latest AS builder`, copies its
# own (fresh) source on top, and recompiles only its own packages against the warm cache
# — turning a cold ~20-min compile into tens of seconds.
#
# Rebuild ONLY when go.mod/go.sum or the Go toolchain version changes:
#   ./deployment/build-base-image.sh
FROM golang:1.25-alpine AS gobuilder

# Kept in sync with the service builds (deploy.sh passes --build-arg). Leave 2 of 8
# cores for the daemon/OS during the warm compile.
ARG GO_BUILD_PARALLELISM=6

# Certs, timezones, and the uid 10001 appuser. The scratch-based services COPY
# /etc/ssl/certs, /usr/share/zoneinfo, /etc/passwd and /etc/group from this stage,
# so these MUST exist here.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

# 1. Dependency blueprints first — this layer only changes when a go.mod/go.sum does,
#    keeping `go mod download` cached across base rebuilds.
COPY go.work go.work.sum* ./
COPY shared/go.mod shared/go.sum* ./shared/
COPY api-gateway/go.mod* ./api-gateway/
COPY auth-service/go.mod* ./auth-service/
COPY convert-from-pdf/go.mod* ./convert-from-pdf/
COPY convert-to-pdf/go.mod* ./convert-to-pdf/
COPY optimize-pdf/go.mod* ./optimize-pdf/
COPY organize-pdf/go.mod* ./organize-pdf/
COPY job-service/go.mod* ./job-service/
COPY analytics-service/go.mod* ./analytics-service/
COPY document-service/go.mod* ./document-service/
COPY user-service/go.mod* ./user-service/
COPY notification-service/go.mod* ./notification-service/

# 2. Download every module's deps into /go/pkg/mod (baked into the image).
RUN go mod download

# 3. Copy all source so the warm compile below can reach every package.
COPY . .

# 4. Warm /root/.cache/go-build for the whole graph. Compile each module the SAME way
#    its service does (cd <module> && go build with -trimpath, CGO_ENABLED=0, GOOS=linux)
#    so the cache keys match and service builds reuse the compiled dependency archives.
#    `|| true` keeps a module that doesn't cleanly build in isolation from failing the
#    base; the deps it did compile are still cached.
RUN set -eux; \
    for m in shared api-gateway auth-service convert-from-pdf convert-to-pdf \
             optimize-pdf organize-pdf job-service analytics-service \
             document-service user-service notification-service; do \
      ( cd "$m" && CGO_ENABLED=0 GOOS=linux \
          go build -p "${GO_BUILD_PARALLELISM}" -trimpath ./... ) || true; \
    done
