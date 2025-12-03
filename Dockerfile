# Build stage
FROM golang:1.22-alpine AS builder

# Install git (needed for go mod download)
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gitgost ./cmd/server

# Final stage
FROM alpine:latest

# Install runtime dependencies (git required for receive-pack execution)
RUN apk --no-cache add ca-certificates wget git

# Create a non-root user
RUN adduser -D -s /bin/sh gitgost

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/gitgost .

# Copy web assets
COPY --from=builder /app/web ./web

# Change ownership to non-root user
RUN chown -R gitgost:gitgost /app

# Switch to non-root user
USER gitgost

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/ || exit 1

# Run the binary
CMD ["./gitgost"]