FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/bin/convert-to-pdf .

FROM alpine:3.19

# Install system dependencies for PDF processing
# - poppler-utils: for pdftoppm (PDF to images)
# - libreoffice: for Office document conversions
# - ttf-liberation: for better font support
RUN apk add --no-cache ca-certificates \
    poppler-utils \
    libreoffice \
    ttf-liberation \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/bin/convert-to-pdf ./convert-to-pdf
COPY .env ./

RUN mkdir -p uploads outputs \
    && chown -R appuser:appuser /app

VOLUME /app/uploads
VOLUME /app/outputs

USER appuser

EXPOSE 8083

CMD ["./convert-to-pdf"]
