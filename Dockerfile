# Stage 1: Build the custom collector with OCB
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG BUILDPLATFORM
ENV CGO_ENABLED=0
ENV GOPRIVATE=github.com/henrikrexed/influxdbreader

# Install build dependencies
RUN apk add --no-cache \
    git \
    make \
    curl \
    tar \
    ca-certificates

WORKDIR /workspace

# Install OCB (OpenTelemetry Collector Builder)
RUN go install go.opentelemetry.io/collector/cmd/builder@latest

# Copy OCB manifest
ARG MANIFEST=ocb.yaml
COPY ${MANIFEST} ./manifest.yaml

# Copy Go module files first for better layer caching
COPY go.mod go.sum ./

# Copy source code files to the workspace (module root)
COPY . .

# Make git tags available for Go module resolution (if needed)
RUN git config --global --add safe.directory /workspace || true

# Generate vendor directory for reproducible builds and OCB compatibility
RUN go mod vendor

# Ensure replace directives are properly applied
RUN go mod tidy

# Add replace directive for local development
RUN go mod edit -replace=github.com/henrikrexed/influxdbreader=/workspace

# Build the custom collector (compile for the requested target platform)
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} /go/bin/builder --config manifest.yaml --output-path /workspace/dist
RUN echo "=== DIST FOLDER CONTENTS ===" && ls -la /workspace/dist || true
RUN echo "=== ALL FILES IN DIST ===" && find /workspace/dist -type f 2>/dev/null || true
RUN echo "=== ALL DIRECTORIES IN DIST ===" && find /workspace/dist -type d 2>/dev/null || true
RUN echo "=== WORKSPACE CONTENTS ===" && ls -la /workspace || true
RUN echo "=== ALL OTELCOL BINARIES IN WORKSPACE ===" && find /workspace -name "*otelcol*" -type f 2>/dev/null || true
RUN echo "=== ALL BINARIES IN WORKSPACE ===" && find /workspace -name "*" -type f -executable 2>/dev/null || true
RUN echo "=== DEBUG: CHECKING IF BINARY EXISTS ===" && ls -la /workspace/dist/otelcol-influxdbreader || echo "BINARY NOT FOUND"

# Production stage
FROM alpine:3.18

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 otel && \
    adduser -u 1000 -G otel -s /bin/sh -D otel

# Copy the binary from builder stage (OCB creates it as 'builder')
COPY --from=builder /workspace/dist/builder /otelcol-influxdbreader

# Set ownership
RUN chown otel:otel /otelcol-influxdbreader

# Create config directory
RUN mkdir -p /etc/otelcol && \
    chown otel:otel /etc/otelcol

# Switch to non-root user
USER otel

# Expose ports
EXPOSE 4317 4318 13133 1777 55679 8888

# Health check
HEALTHCHECK --interval=30s --timeout=30s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:13133/ || exit 1

# Set entrypoint
ENTRYPOINT ["/otelcol-influxdbreader"]
CMD ["--config", "/etc/otelcol/config.yaml"]
