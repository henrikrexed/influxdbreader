.PHONY: help build test test-verbose lint clean deps docker-build docker-push install-tools ocb-build

# Variables
BINARY_NAME := otelcol-influxdbreader
DOCKER_IMAGE := hrexed/collector-influxdbreader
DOCKER_TAG := latest
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Container runtime (docker or podman)
CONTAINER_RUNTIME ?= docker

# Platform variables
PLATFORM ?= linux/amd64
PLATFORM_SHORT ?= amd64
BUILDX_PLATFORMS ?= linux/amd64,linux/arm64

# Default target
help:
	@echo "Available targets:"
	@echo "  build        - Build the receiver for testing"
	@echo "  test         - Run unit tests"
	@echo "  test-verbose - Run unit tests with verbose output"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  lint         - Run linter"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Download and tidy dependencies"
	@echo "  install-tools - Install development tools (golangci-lint)"
	@echo "  docker-build - Build container image using OCB (default: linux/amd64)"
	@echo "  docker-build-amd64 - Build container image for linux/amd64"
	@echo "  docker-build-arm64 - Build container image for linux/arm64"
	@echo "  docker-build-arm32 - Build container image for linux/arm/v7"
	@echo "  docker-build-multi - Build multi-platform container image"
	@echo "  docker-build-all - Build, test, and create multi-platform container image"
	@echo "  docker-push  - Push container image to registry"
	@echo "  build-version - Build with specific version (requires VERSION=tag)"
	@echo "  build-version-podman - Build with Podman and specific version"
	@echo "  generate-status-table - Generate status table from metadata"
	@echo "  all          - Build, test, and create container image"
	@echo ""
	@echo "Container runtime options:"
	@echo "  CONTAINER_RUNTIME=docker  - Use Docker (default)"
	@echo "  CONTAINER_RUNTIME=podman  - Use Podman"
	@echo ""
	@echo "Version options:"
	@echo "  VERSION=v1.0.0           - Set custom version tag"
	@echo "  VERSION=latest           - Use 'latest' tag"
	@echo "  VERSION=dev              - Use 'dev' tag"
	@echo ""
	@echo "Examples:"
	@echo "  make docker-build                                    # Use Docker with auto version"
	@echo "  make docker-build CONTAINER_RUNTIME=podman          # Use Podman with auto version"
	@echo "  make docker-build VERSION=v1.0.0                    # Build with specific version"
	@echo "  make docker-build CONTAINER_RUNTIME=podman VERSION=v1.0.0  # Podman with specific version"

# Build the receiver binary (for testing only)
build: deps
	@echo "Building receiver for testing..."
	@mkdir -p bin
	go build -o bin/$(BINARY_NAME) ./receiver/influxdbreaderreceiver

# Run unit tests
test: deps
	@echo "Running unit tests..."
	go test ./...

# Run unit tests with verbose output
test-verbose: deps
	@echo "Running unit tests with verbose output..."
	go test -v ./...

# Run tests with coverage
test-coverage: deps
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run linter
lint: deps
	@echo "Running linter..."
	~/go/bin/golangci-lint run

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	rm -rf dist/

# Download and tidy dependencies
deps:
	@echo "Downloading and tidying dependencies..."
	go mod download
	go mod tidy

# Install development tools
install-tools:
	@echo "Installing development tools..."
	@if ! command -v golangci-lint &> /dev/null; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi



# Build using OpenTelemetry Collector Builder (Container only)
ocb-build:
	@echo "OCB build is only available through containers. Use 'make docker-build' instead."
	@echo "Or run: $(CONTAINER_RUNTIME) build -t $(DOCKER_IMAGE):$(DOCKER_TAG) ."

# Build container image using OCB (single platform)
docker-build:
	@echo "Building $(CONTAINER_RUNTIME) image with OCB for platform $(PLATFORM)..."
	@echo "Version: $(VERSION)"
	@echo "Image: $(DOCKER_IMAGE)"
	$(CONTAINER_RUNTIME) build --load -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):$(VERSION) .
	@echo "$(CONTAINER_RUNTIME) image built: $(DOCKER_IMAGE):$(DOCKER_TAG) and $(DOCKER_IMAGE):$(VERSION) for $(PLATFORM)"

# Build container image using OCB (multi-platform)
docker-build-multi:
	@echo "Building multi-platform $(CONTAINER_RUNTIME) image with OCB..."
	@echo "Version: $(VERSION)"
	@if [ "$(CONTAINER_RUNTIME)" = "docker" ]; then \
		echo "Note: This requires docker buildx and may need additional setup for cross-platform builds"; \
		$(CONTAINER_RUNTIME) buildx build --platform $(BUILDX_PLATFORMS) -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):$(VERSION) --load .; \
	else \
		echo "Note: Podman multi-platform builds may have limitations"; \
		$(CONTAINER_RUNTIME) build --platform $(BUILDX_PLATFORMS) -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):$(VERSION) .; \
	fi
	@echo "Multi-platform $(CONTAINER_RUNTIME) image built: $(DOCKER_IMAGE):$(DOCKER_TAG) and $(DOCKER_IMAGE):$(VERSION) for $(BUILDX_PLATFORMS)"

# Build development container image
docker-build-dev:
	@echo "Building development $(CONTAINER_RUNTIME) image with OCB for platform $(PLATFORM)..."
	$(CONTAINER_RUNTIME) build -f Dockerfile.dev -t $(DOCKER_IMAGE):dev .
	@echo "Development $(CONTAINER_RUNTIME) image built: $(DOCKER_IMAGE):dev for $(PLATFORM)"

# Push container image
docker-push: docker-build
	@echo "Pushing $(CONTAINER_RUNTIME) image..."
	$(CONTAINER_RUNTIME) push $(DOCKER_IMAGE):$(DOCKER_TAG)
	$(CONTAINER_RUNTIME) push $(DOCKER_IMAGE):$(VERSION)
	@echo "$(CONTAINER_RUNTIME) image pushed: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Run all checks
check: lint test

# Build and test
all: deps lint test docker-build

# Convenience targets for specific versions
build-version:
	@echo "Building version $(VERSION)..."
	@$(MAKE) docker-build VERSION=$(VERSION)

build-version-podman:
	@echo "Building version $(VERSION) with Podman..."
	@$(MAKE) docker-build CONTAINER_RUNTIME=podman VERSION=$(VERSION)

# Platform-specific build targets
docker-build-amd64:
	@$(MAKE) docker-build PLATFORM=linux/amd64 PLATFORM_SHORT=amd64

docker-build-arm64:
	@$(MAKE) docker-build PLATFORM=linux/arm64 PLATFORM_SHORT=arm64

docker-build-arm32:
	@$(MAKE) docker-build PLATFORM=linux/arm/v7 PLATFORM_SHORT=arm32

# Multi-platform build
docker-build-all: deps lint test docker-build-multi

# Development helpers
dev-setup: install-tools deps
	@echo "Development environment setup complete"

# Run with container (test configuration)
run-test: docker-build
	@echo "Running with test configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/testdata/config.yaml:/etc/otelcol/config.yaml $(DOCKER_IMAGE):$(DOCKER_TAG)

# Run with container (test configuration v2)
run-test-v2: docker-build
	@echo "Running with test configuration (v2)..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/testdata/config_v2.yaml:/etc/otelcol/config.yaml $(DOCKER_IMAGE):$(DOCKER_TAG)

# Run with container (simple configuration)
run-simple: docker-build
	@echo "Running with simple configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/config-simple.yaml:/etc/otelcol/config.yaml $(DOCKER_IMAGE):$(DOCKER_TAG)

# Run with container (full configuration)
run-full: docker-build
	@echo "Running with full configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/config-full.yaml:/etc/otelcol/config.yaml $(DOCKER_IMAGE):$(DOCKER_TAG)

# Container run with test configuration
docker-run-test: docker-build
	@echo "Running $(CONTAINER_RUNTIME) container with test configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/testdata:/etc/otelcol \
		$(DOCKER_IMAGE):$(DOCKER_TAG) --config /etc/otelcol/config.yaml

# Container run with test configuration (v2)
docker-run-test-v2: docker-build
	@echo "Running $(CONTAINER_RUNTIME) container with test configuration (v2)..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/testdata:/etc/otelcol \
		$(DOCKER_IMAGE):$(DOCKER_TAG) --config /etc/otelcol/config_v2.yaml

# Container run with simple configuration
docker-run-simple: docker-build
	@echo "Running $(CONTAINER_RUNTIME) container with simple configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/config-simple.yaml:/etc/otelcol/config.yaml \
		$(DOCKER_IMAGE):$(DOCKER_TAG) --config /etc/otelcol/config.yaml

# Container run with full configuration
docker-run-full: docker-build
	@echo "Running $(CONTAINER_RUNTIME) container with full configuration..."
	$(CONTAINER_RUNTIME) run --rm -v $(PWD)/config-full.yaml:/etc/otelcol/config.yaml \
		$(DOCKER_IMAGE):$(DOCKER_TAG) --config /etc/otelcol/config.yaml

# Container run with interactive shell (development)
docker-run-dev: docker-build-dev
	@echo "Running development $(CONTAINER_RUNTIME) container with shell..."
	$(CONTAINER_RUNTIME) run --rm -it -v $(PWD)/config-simple.yaml:/etc/otelcol/config.yaml \
		$(DOCKER_IMAGE):dev /bin/sh

# Show version information
version:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Git Commit: $(GIT_COMMIT)"

# Integration tests (requires InfluxDB)
integration: deps
	@echo "Running integration tests..."
	@echo "Note: This requires a running InfluxDB instance"
	go test -tags=integration ./...

# Performance testing
bench: deps
	@echo "Running benchmarks..."
	go test -bench=. ./...

# Security scanning
security-scan: docker-build
	@echo "Running security scan..."
	@if command -v trivy &> /dev/null; then \
		trivy image $(DOCKER_IMAGE):$(DOCKER_TAG); \
	else \
		echo "Trivy not found. Install with: go install github.com/aquasecurity/trivy/cmd/trivy@latest"; \
	fi

# Generate status table from metadata
generate-status-table:
	@echo "Generating status table from metadata..."
	@python3 scripts/generate-status-table.py
