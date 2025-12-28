FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bin/convert-to-pdf .

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/bin/convert-to-pdf ./convert-to-pdf
COPY .env ./

RUN mkdir -p uploads outputs
VOLUME /root/uploads
VOLUME /root/outputs

EXPOSE 8083

CMD ["./convert-to-pdf"]
