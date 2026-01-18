FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/convert-from-pdf .

# Use pre-built base image with LibreOffice (must be built first)
# To use Docker Hub image: --build-arg BASE_IMAGE=your-dockerhub-username/alpine-libreoffice:3.19
# To use local image: Build base image first using docker/base-alpine-libreoffice.Dockerfile
ARG BASE_IMAGE=alpine:3.19

FROM ${BASE_IMAGE}

# If using plain alpine (fallback), install dependencies
# When using pre-built base image, these will already be present (no-op)
RUN if ! command -v libreoffice >/dev/null 2>&1; then \
        apk add --no-cache \
            ca-certificates \
            poppler-utils \
            libreoffice \
            openjdk17-jre-headless \
            ttf-liberation; \
    fi && \
    adduser -D -u 10001 appuser 2>/dev/null || true

WORKDIR /app

COPY --from=builder /app/bin/convert-from-pdf ./convert-from-pdf
COPY .env ./

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8082

CMD ["./convert-from-pdf"]
