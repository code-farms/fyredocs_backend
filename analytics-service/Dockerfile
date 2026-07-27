# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# The workspace blueprint must match the fresh source below, not the older snapshot
# the base image baked in — otherwise a bumped `go` directive or a new module in a
# go.mod fails against a stale go.work ("go.work lists go 1.25.0").
COPY go.work go.work.sum* ./

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY analytics-service/ ./analytics-service/

RUN cd analytics-service && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/analytics-service . && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/healthcheck ../shared/healthcheck

# ---- Runtime Stage ----
# 6. Use the ultimate empty base
FROM scratch

# 7. Bring over essential runtime metadata from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

WORKDIR /app

# 8. Copy the binary
COPY --from=builder /app/bin/healthcheck .
COPY --from=builder /app/bin/analytics-service .

# 9. Run as the secure unprivileged user
USER appuser

EXPOSE 8087

CMD ["./analytics-service"]
