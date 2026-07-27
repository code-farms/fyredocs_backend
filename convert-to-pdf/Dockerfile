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
COPY convert-to-pdf/ ./convert-to-pdf/

RUN cd convert-to-pdf && \
    CGO_ENABLED=0 GOOS=linux go build -p ${GO_BUILD_PARALLELISM} -trimpath -ldflags="-s -w" -o /app/bin/convert-to-pdf .

# ---- Runtime Stage ----
FROM fyredocs-base:latest

RUN adduser -D -u 10001 appuser

WORKDIR /app

RUN mkdir -p uploads outputs && \
    chown -R appuser:appuser /app

COPY --from=builder /app/bin/convert-to-pdf .
COPY convert-to-pdf/entrypoint.sh .
RUN chown appuser:appuser convert-to-pdf entrypoint.sh && \
    chmod +x entrypoint.sh

USER appuser

EXPOSE 8083

VOLUME /app/uploads /app/outputs

CMD ["sh", "entrypoint.sh"]