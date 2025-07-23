# Multi-stage build for txplain: frontend + Go server

# Stage 1: Build the frontend
FROM node:20-alpine AS frontend-builder

WORKDIR /app/web

# Copy package files
COPY web/package*.json ./

# Install dependencies
RUN npm install

# Copy frontend source
COPY web/ ./

# Build the frontend
RUN npm run build

# Stage 2: Build the Go application
FROM golang:1.23-alpine AS go-builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/main.go

# Final stage - minimal runtime image
FROM alpine:latest

# Install ca-certificates for HTTPS requests and curl for health checks
RUN apk --no-cache add ca-certificates tzdata curl

# Create non-root user for security
RUN addgroup -g 1001 -S txplain && \
    adduser -u 1001 -S txplain -G txplain

# Set working directory
WORKDIR /app

# Copy binary from Go builder stage
COPY --from=go-builder /app/main .

# Copy data files (CSV and JSON configuration files)
COPY --from=go-builder /app/data/ ./data/

# Copy built frontend from frontend builder stage
COPY --from=frontend-builder /app/web/dist/ ./web/dist/

# Copy example.env as reference (users should mount their own .env)
COPY --from=go-builder /app/example.env .

# Change ownership of files to non-root user
RUN chown -R txplain:txplain /app

# Switch to non-root user
USER txplain

# Expose port 8080 (the default HTTP server port)
EXPOSE 8080

# Set default environment variables
ENV HTTP_ADDR=:8080
ENV ENV=production

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Default command - start the HTTP server
CMD ["./main", "-http"]
