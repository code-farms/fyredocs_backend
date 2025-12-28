FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/convert-from-pdf .

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/bin/convert-from-pdf ./convert-from-pdf
COPY .env ./

RUN mkdir -p uploads outputs
VOLUME /root/uploads
VOLUME /root/outputs

EXPOSE 8082

CMD ["./convert-from-pdf"]
