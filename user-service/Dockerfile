# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY user-service/ ./user-service/

RUN cd user-service && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/user-service . && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/healthcheck ../shared/healthcheck

# ---- Runtime Stage ----
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

WORKDIR /app

COPY --from=builder /app/bin/healthcheck .
COPY --from=builder /app/bin/user-service .

USER appuser

EXPOSE 8090

CMD ["./user-service"]
