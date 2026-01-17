FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/upload-service .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/upload-service ./upload-service
COPY .env ./

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8081

CMD ["./upload-service"]
