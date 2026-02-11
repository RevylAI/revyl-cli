# Revyl CLI Makefile
# Build, test, and development commands

# Version info (set via ldflags during build)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build flags
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Binary name
BINARY := revyl

# Go commands
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOGET := $(GOCMD) get
GOMOD := $(GOCMD) mod
GOFMT := gofmt

# Directories
CMD_DIR := ./cmd/revyl
BUILD_DIR := ./build
SCRIPTS_DIR := ./scripts

.PHONY: all build clean test lint fmt deps dev generate install help check

## help: Show this help message
help:
	@echo "Revyl CLI - Development Commands"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## all: Build the CLI
all: build

## check: Quick compile and vet check (used by pre-commit)
check:
	@echo "Checking Go code..."
	@$(GOBUILD) ./cmd/revyl/...
	@$(GOCMD) vet ./...
	@echo "✅ Go checks passed"

## build: Build the CLI binary
build:
	@echo "Building $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

## build-all: Build for all platforms
build-all:
	@echo "Building for all platforms..."
	@$(SCRIPTS_DIR)/build-all.sh

## install: Install the CLI to $GOPATH/bin
install:
	@echo "Installing $(BINARY)..."
	$(GOBUILD) $(LDFLAGS) -o $(GOPATH)/bin/$(BINARY) $(CMD_DIR)
	@echo "Installed to $(GOPATH)/bin/$(BINARY)"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f $(BINARY)

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: Run linters
lint:
	@echo "Running linters..."
	@if command -v golangci-lint &> /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) -s -w .

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

## generate: Generate types from cached OpenAPI spec (for CI/contributors)
generate:
	@echo "Generating types from cached OpenAPI spec..."
	@$(SCRIPTS_DIR)/generate-types.sh

## generate-fetch: Fetch fresh OpenAPI spec and generate types (for internal devs)
generate-fetch:
	@echo "Fetching fresh OpenAPI spec and generating types..."
	@$(SCRIPTS_DIR)/generate-types.sh --fetch

## dev: Run with hot reload (uses air)
dev:
	@if command -v air &> /dev/null; then \
		echo "Starting hot reload with air..."; \
		air; \
	elif command -v watchexec &> /dev/null; then \
		$(MAKE) watch; \
	else \
		echo "Neither air nor watchexec installed."; \
		echo "Install air: go install github.com/air-verse/air@latest"; \
		echo "Or watchexec: brew install watchexec"; \
		echo "Running single build instead..."; \
		$(MAKE) build; \
	fi

## watch: Watch for changes and rebuild (requires watchexec)
watch:
	@if command -v watchexec &> /dev/null; then \
		echo "Watching for changes... (Ctrl+C to stop)"; \
		watchexec -e go -- $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR); \
	else \
		echo "watchexec not installed. Run: brew install watchexec"; \
		exit 1; \
	fi

## setup: Install development tools
setup:
	@echo "Installing development tools..."
	go install github.com/air-verse/air@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	brew install watchexec || true
	@echo "Done! Run 'make dev' to start development with hot reload."

## run: Run the CLI (pass ARGS for arguments)
run:
	@$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)
	@$(BUILD_DIR)/$(BINARY) $(ARGS)

# Development shortcuts
.PHONY: r b t

## r: Shortcut for 'make run'
r: run

## b: Shortcut for 'make build'
b: build

## t: Shortcut for 'make test'
t: test
