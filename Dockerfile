# Stage 1: Build
FROM golang:alpine AS builder

WORKDIR /app

# Install build dependencies (if needed, e.g., git, make)
RUN apk add --no-cache git make

# Download dependencies
COPY go.mod go.sum ./
ENV GOPROXY=https://proxy.golang.org,direct
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api

# Stage 2: Run
FROM alpine:3.19

WORKDIR /root/

# Install runtime dependencies (e.g., ca-certificates for HTTPS)
RUN apk --no-cache add ca-certificates

# Copy the binary from the builder stage
COPY --from=builder /app/main .

# Expose the application port
EXPOSE 8080

# Command to run the application
CMD ["./main"]
