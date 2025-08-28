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
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} /go/bin/builder --config manifest.yaml --output-path /workspace/dist
RUN echo "=== DIST FOLDER CONTENTS ===" && ls -la /workspace/dist || true
RUN echo "=== ALL FILES IN DIST ===" && find /workspace/dist -type f 2>/dev/null || true
RUN echo "=== ALL DIRECTORIES IN DIST ===" && find /workspace/dist -type d 2>/dev/null || true
RUN echo "=== WORKSPACE CONTENTS ===" && ls -la /workspace || true
RUN echo "=== ALL OTELCOL BINARIES IN WORKSPACE ===" && find /workspace -name "*otelcol*" -type f 2>/dev/null || true
RUN echo "=== ALL BINARIES IN WORKSPACE ===" && find /workspace -name "*" -type f -executable 2>/dev/null || true
RUN echo "=== DEBUG: CHECKING IF BINARY EXISTS ===" && ls -la /workspace/dist/otelcol-influxdbreader || echo "BINARY NOT FOUND"

# Stage 2: Create a minimal runtime image
FROM gcr.io/distroless/base-debian11

ARG TARGETOS
ARG TARGETARCH

WORKDIR /otel
COPY --from=builder /workspace/dist/builder ./otelcol-influxdbreader

# Copy configuration files (optional)
# COPY config.yaml ./config.yaml

EXPOSE 4317 4318 13133 1777 55679 8888

ENTRYPOINT ["./otelcol-influxdbreader", "--config", "/etc/otelcol/config.yaml"]
