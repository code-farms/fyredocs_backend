# ---- Build Stage ----
# Use an official Golang image to build the application
FROM golang:1.21-alpine AS builder

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go app
# -o main: specifies the output file name
# CGO_ENABLED=0: disables Cgo to create a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# ---- Final Stage ----
# Use a lightweight Alpine image for the final container
FROM alpine:latest

# Set the Current Working Directory inside the container
WORKDIR /root/

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/main .

# Copy the .env file. Note: For production, it's better to use Docker secrets or environment variables.
COPY .env .

# Create directories for file uploads and outputs
# These should ideally be managed as Docker volumes in a real deployment
RUN mkdir -p uploads outputs
VOLUME /root/uploads
VOLUME /root/outputs

# Expose port 8080 to the outside world
EXPOSE 8080

# Command to run the executable
CMD ["./main"]
