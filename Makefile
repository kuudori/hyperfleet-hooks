# ==============================================================================
# Makefile for hyperfleet-hooks
# ==============================================================================

TOOL_MOD := tools/go.mod
gotool = go tool -modfile=$(TOOL_MOD) $(1)

.PHONY: help build build-all test test-coverage lint clean install validate-commits \
       check-container-tool image image-push

# ==============================================================================
# Configuration
# ==============================================================================

# Binary configuration
BINARY_NAME := hyperfleet-hooks
BIN_DIR := bin

# Version information (auto-detected from git)
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
GIT_SHA     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_DIRTY   ?= $(shell [ -z "$$(git status --porcelain 2>/dev/null)" ] || echo "-modified")
BUILD_DATE  ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Container configuration
CONTAINER_TOOL ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
PLATFORM       ?= linux/amd64
IMAGE_REGISTRY ?= quay.io/openshift-hyperfleet
IMAGE_NAME     ?= hyperfleet-git-hooks
IMAGE_TAG      ?= $(APP_VERSION)

# Build flags
GOFLAGS ?= -trimpath
LDFLAGS := -ldflags "\
	-s -w \
	-X github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/version.Version=$(APP_VERSION) \
	-X github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/version.GitCommit=$(GIT_SHA)$(GIT_DIRTY) \
	-X github.com/openshift-hyperfleet/hyperfleet-hooks/pkg/version.BuildDate=$(BUILD_DATE)"

# ==============================================================================
# Targets
# ==============================================================================

.DEFAULT_GOAL := help

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ------------------------------------------------------------------------------
# Build
# ------------------------------------------------------------------------------

build: ## Build the binary
	@echo "Building $(BINARY_NAME) $(APP_VERSION)..."
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/hyperfleet-hooks
	@echo "✓ Built $(BIN_DIR)/$(BINARY_NAME)"

build-all: ## Build binaries for all platforms
	@echo "Building for all platforms..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/hyperfleet-hooks
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/hyperfleet-hooks
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/hyperfleet-hooks
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o $(BIN_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/hyperfleet-hooks
	@echo "✓ Built all platform binaries"

# ------------------------------------------------------------------------------
# Test
# ------------------------------------------------------------------------------

test: ## Run tests
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...
	@echo "✓ Tests passed"

test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report generated: coverage.html"

# ------------------------------------------------------------------------------
# Quality
# ------------------------------------------------------------------------------

lint: ## Run linters
	@echo "Running linters..."
	$(call gotool,golangci-lint) run --timeout=5m
	@echo "✓ Linting passed"

.PHONY: tools
tools: ## Ensure tool dependencies are up to date
	cd tools && go mod tidy

.PHONY: verify-tools
verify-tools: tools ## Fail in CI if tool module drifted
	@git diff --exit-code tools/go.mod tools/go.sum || (echo "tool modules out of date; run 'make tools'" && exit 1)

# ------------------------------------------------------------------------------
# Utility
# ------------------------------------------------------------------------------

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(BIN_DIR)
	rm -f coverage.out coverage.html
	@echo "✓ Cleaned"

install: build ## Install binary to /usr/local/bin
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	@cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "✓ Installed $(BINARY_NAME)"

# ------------------------------------------------------------------------------
# Container
# ------------------------------------------------------------------------------

check-container-tool:
ifndef CONTAINER_TOOL
	@echo "Error: No container tool found (podman or docker)"
	@echo ""
	@echo "Please install one of:"
	@echo "  brew install podman   # macOS"
	@echo "  brew install docker   # macOS"
	@echo "  dnf install podman    # Fedora/RHEL"
	@exit 1
endif

image: check-container-tool ## Build container image
	@echo "Building container image..."
	$(CONTAINER_TOOL) build \
		--platform $(PLATFORM) \
		--build-arg GIT_SHA=$(GIT_SHA) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg APP_VERSION=$(APP_VERSION) \
		-t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_REGISTRY)/$(IMAGE_NAME):latest .
	@echo "✓ Built $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)"
	@echo "✓ Built $(IMAGE_REGISTRY)/$(IMAGE_NAME):latest"

image-push: check-container-tool image ## Build and push container image
	@echo "Pushing container image..."
	$(CONTAINER_TOOL) push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	$(CONTAINER_TOOL) push $(IMAGE_REGISTRY)/$(IMAGE_NAME):latest
	@echo "✓ Pushed $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)"
	@echo "✓ Pushed $(IMAGE_REGISTRY)/$(IMAGE_NAME):latest"

# ------------------------------------------------------------------------------
# CI
# ------------------------------------------------------------------------------

validate-commits: build ## Validate commits in current branch (CI mode)
	@echo "Validating commits..."
	@./bin/hyperfleet-hooks commitlint --pr
