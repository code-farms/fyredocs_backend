# ---- Builder Stage ----
# Uses the shared fyredocs-go-builder base (deps + warm Go build cache baked in).
# Build/refresh it with ./deployment/build-base-image.sh when go.mod deps change.
FROM fyredocs-go-builder:latest AS builder

ARG GO_BUILD_PARALLELISM=6

WORKDIR /app

# Fresh source on top of the base's warm cache — only these packages recompile.
COPY shared/ ./shared/
COPY job-service/ ./job-service/

RUN cd job-service && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/job-service . && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/healthcheck ../shared/healthcheck

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
COPY --from=builder /app/bin/healthcheck .
COPY --from=builder /app/bin/job-service .

# 9. Run as the secure unprivileged user
USER appuser

EXPOSE 8081

CMD ["./job-service"]