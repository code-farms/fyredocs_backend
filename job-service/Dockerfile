# ---- Builder Stage ----
FROM golang:1.25-alpine AS builder

# Prepare system files and non-root user for the scratch image
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

# 2. Download dependencies (cached via BuildKit)
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 3. Copy source code
COPY shared/ ./shared/
COPY job-service/ ./job-service/

# 4. Build binary with Go build cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd job-service && \
    CGO_ENABLED=0 GOOS=linux go build -p 2 -trimpath -ldflags="-s -w" -o /app/bin/job-service .

# ---- Runtime Stage ----
# 5. Using the empty base for a tiny, secure image
FROM scratch

# 6. Bring over essential runtime metadata
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

WORKDIR /app

# 7. Create volumes (Note: Scratch doesn't have 'mkdir', but defining VOLUME works)
VOLUME /app/uploads /app/outputs

# 8. Copy the binary
COPY --from=builder /app/bin/job-service .

# 9. Run as the secure unprivileged user
USER appuser

EXPOSE 8081

CMD ["./job-service"]