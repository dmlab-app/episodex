# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application (CGO disabled for pure Go SQLite)
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o episodex ./cmd/server

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    mkvtoolnix \
    ffmpeg \
    tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/episodex .

# Copy web assets
COPY --from=builder /app/web ./web

# Create data directory
RUN mkdir -p /app/data/backups

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

# Run as non-root user
RUN adduser -D -u 1000 episodex && \
    chown -R episodex:episodex /app
USER episodex

CMD ["./episodex"]
