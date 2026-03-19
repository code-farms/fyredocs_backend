# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

# Prepare system files and non-root user
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

# 1. Copy workspace blueprints for global caching
COPY go.work go.work.sum* ./
COPY shared/go.mod shared/go.sum* ./shared/
COPY api-gateway/go.mod* ./api-gateway/
COPY auth-service/go.mod* ./auth-service/
COPY cleanup-worker/go.mod* ./cleanup-worker/
COPY convert-from-pdf/go.mod* ./convert-from-pdf/
COPY convert-to-pdf/go.mod* ./convert-to-pdf/
COPY optimize-pdf/go.mod* ./optimize-pdf/
COPY organize-pdf/go.mod* ./organize-pdf/
COPY job-service/go.mod* ./job-service/
COPY analytics-service/go.mod* ./analytics-service/

# 2. Download dependencies (cached via BuildKit)
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 3. Copy source code
COPY shared/ ./shared/
COPY cleanup-worker/ ./cleanup-worker/

# 4. Build binary with Go build cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd cleanup-worker && \
    CGO_ENABLED=0 GOOS=linux go build -p 2 -trimpath -ldflags="-s -w" -o /app/bin/cleanup-worker .

# ---- Runtime Stage ----
# We use Alpine instead of Scratch so the worker can manage the filesystem
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

RUN adduser -D -u 10001 appuser

WORKDIR /app

# 5. Copy the binary from builder
COPY --from=builder /app/bin/cleanup-worker .

# 6. Ensure the binary has correct ownership
RUN chown appuser:appuser cleanup-worker

USER appuser

# Cleanup workers usually don't need to EXPOSE a port
CMD ["./cleanup-worker"]