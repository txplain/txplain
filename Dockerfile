# Single-stage build optimized for Railway
FROM node:20-alpine

# Install system dependencies for Go and runtime
RUN apk add --no-cache git ca-certificates tzdata go curl

# Set Go environment
ENV GOOS=linux
ENV CGO_ENABLED=0
ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH

# Create app directory
WORKDIR /app

# Copy and build frontend first (smaller memory footprint)
COPY web/package*.json ./web/
WORKDIR /app/web
RUN npm install --no-audit --no-fund

COPY web/ ./
RUN npm run build

# Switch back to app root and handle Go build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

# Copy Go source
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY data/ ./data/

# Build Go application
RUN go build -ldflags="-w -s" -o main ./cmd/main.go

# Create non-root user
RUN addgroup -g 1001 -S txplain && \
    adduser -u 1001 -S txplain -G txplain

# Set permissions
RUN chown -R txplain:txplain /app

# Switch to non-root user
USER txplain

# Expose port
EXPOSE 8080

# Set environment variables
ENV HTTP_ADDR=:8080
ENV ENV=production

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD sh -c 'curl -f http://localhost:${PORT:-8080}/health || exit 1'

# Start the application
CMD ["./main", "-http"]
