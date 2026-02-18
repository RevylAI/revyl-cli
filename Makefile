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

.PHONY: all build clean test lint fmt deps dev generate install help check setup-merge-drivers version bump-patch bump-minor bump-major

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

## test: Run tests with summary
test:
	@echo "Running tests..."
	@JSONFILE=$$(mktemp /tmp/revyl-test-XXXXXX.json) ; \
	if command -v gotestsum &> /dev/null; then \
		gotestsum --format testdox --jsonfile "$$JSONFILE" ./... ; \
		TEST_EXIT=$$? ; \
	else \
		$(GOTEST) -json ./... > "$$JSONFILE" ; \
		TEST_EXIT=$$? ; \
	fi ; \
	PASSED=$$(grep '"Action":"pass"' "$$JSONFILE" | grep -c '"Test":' || true) ; \
	FAILED=$$(grep '"Action":"fail"' "$$JSONFILE" | grep -c '"Test":' || true) ; \
	SKIPPED=$$(grep '"Action":"skip"' "$$JSONFILE" | grep -c '"Test":' || true) ; \
	TOTAL=$$((PASSED + FAILED + SKIPPED)) ; \
	echo "" ; \
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" ; \
	if [ "$$FAILED" -gt 0 ]; then \
		echo "  FAIL: $$PASSED passed, $$FAILED failed, $$SKIPPED skipped ($$TOTAL total)" ; \
	else \
		echo "  OK: $$PASSED passed, $$SKIPPED skipped ($$TOTAL total)" ; \
	fi ; \
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" ; \
	rm -f "$$JSONFILE" ; \
	exit $$TEST_EXIT

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

## setup-merge-drivers: Register custom merge drivers for generated files
setup-merge-drivers:
	@echo "Registering custom merge drivers..."
	git config merge.gen-ours.name "Auto-accept ours for generated files"
	git config merge.gen-ours.driver true
	@echo "✓ Merge driver 'gen-ours' registered (accepts ours for generated files on merge)"

## setup: Install development tools and configure merge drivers
setup: setup-merge-drivers
	@echo "Installing development tools..."
	go install github.com/air-verse/air@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install gotest.tools/gotestsum@latest
	brew install watchexec || true
	@echo "Done! Run 'make dev' to start development with hot reload."

## run: Run the CLI (pass ARGS for arguments)
run:
	@$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)
	@$(BUILD_DIR)/$(BINARY) $(ARGS)

# ---------- Version management ----------

# Read the current version from the VERSION file
CURRENT_VERSION := $(shell cat VERSION 2>/dev/null | tr -d '[:space:]')

## version: Print the current version from the VERSION file
version:
	@echo "$(CURRENT_VERSION)"

## bump-patch: Bump patch version (e.g. 0.1.1 -> 0.1.2) and sync all version files
bump-patch:
	@OLD="$(CURRENT_VERSION)" ; \
	MAJOR=$$(echo "$$OLD" | cut -d. -f1) ; \
	MINOR=$$(echo "$$OLD" | cut -d. -f2) ; \
	PATCH=$$(echo "$$OLD" | cut -d. -f3) ; \
	NEW="$$MAJOR.$$MINOR.$$((PATCH + 1))" ; \
	$(MAKE) _set-version OLD="$$OLD" NEW="$$NEW"

## bump-minor: Bump minor version (e.g. 0.1.1 -> 0.2.0) and sync all version files
bump-minor:
	@OLD="$(CURRENT_VERSION)" ; \
	MAJOR=$$(echo "$$OLD" | cut -d. -f1) ; \
	MINOR=$$(echo "$$OLD" | cut -d. -f2) ; \
	NEW="$$MAJOR.$$((MINOR + 1)).0" ; \
	$(MAKE) _set-version OLD="$$OLD" NEW="$$NEW"

## bump-major: Bump major version (e.g. 0.1.1 -> 1.0.0) and sync all version files
bump-major:
	@OLD="$(CURRENT_VERSION)" ; \
	MAJOR=$$(echo "$$OLD" | cut -d. -f1) ; \
	NEW="$$((MAJOR + 1)).0.0" ; \
	$(MAKE) _set-version OLD="$$OLD" NEW="$$NEW"

# Internal target: write the new version to all version files.
# Called by bump-patch, bump-minor, bump-major with OLD and NEW variables.
_set-version:
	@echo "Bumping version: $(OLD) -> $(NEW)"
	@printf "$(NEW)\n" > VERSION
	@sed -i.bak 's/"version": "$(OLD)"/"version": "$(NEW)"/' npm/package.json && rm -f npm/package.json.bak
	@sed -i.bak 's/version = "$(OLD)"/version = "$(NEW)"/' python/pyproject.toml && rm -f python/pyproject.toml.bak
	@sed -i.bak 's/__version__ = "$(OLD)"/__version__ = "$(NEW)"/' python/revyl/__init__.py && rm -f python/revyl/__init__.py.bak
	@echo "Updated files:"
	@echo "  VERSION                    $(NEW)"
	@echo "  npm/package.json           $(NEW)"
	@echo "  python/pyproject.toml      $(NEW)"
	@echo "  python/revyl/__init__.py   $(NEW)"
	@echo ""
	@echo "Next steps:"
	@echo "  git add -A && git commit -m 'chore: bump version to $(NEW)'"
	@echo "  Then merge to main to trigger a release."

# Development shortcuts
.PHONY: r b t

## r: Shortcut for 'make run'
r: run

## b: Shortcut for 'make build'
b: build

## t: Shortcut for 'make test'
t: test
