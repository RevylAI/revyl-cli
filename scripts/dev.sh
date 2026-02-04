#!/bin/bash
# Development setup script for Revyl CLI
# Run this once to set up your development environment

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "Revyl CLI - Development Setup"
echo "=============================="
echo ""

cd "$PROJECT_DIR"

# Check Go version
echo "Checking Go installation..."
if ! command -v go &> /dev/null; then
    echo "✗ Go not found. Please install Go 1.22 or later."
    exit 1
fi

GO_VERSION=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | sed 's/go//')
echo "✓ Go $GO_VERSION installed"

# Install development tools
echo ""
echo "Installing development tools..."

echo "  - air (hot reload)..."
go install github.com/air-verse/air@latest

echo "  - oapi-codegen (type generation)..."
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

echo "  - golangci-lint (linting)..."
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

echo "✓ Development tools installed"

# Download dependencies
echo ""
echo "Downloading Go dependencies..."
go mod download
go mod tidy
echo "✓ Dependencies downloaded"

# Try to generate types if backend is running
echo ""
echo "Checking if backend is running for type generation..."
if curl -s --fail "http://127.0.0.1:8000/openapi.json" > /dev/null 2>&1; then
    echo "✓ Backend is running, generating types..."
    ./scripts/generate-types.sh
else
    echo "⚠ Backend not running, skipping type generation"
    echo "  Run 'make generate' after starting the backend"
fi

# Build the CLI
echo ""
echo "Building CLI..."
make build
echo "✓ CLI built: ./build/revyl"

echo ""
echo "=============================="
echo "Setup complete!"
echo ""
echo "Quick start:"
echo "  make dev      # Start with hot reload"
echo "  make build    # Build binary"
echo "  make test     # Run tests"
echo "  make generate # Regenerate types from OpenAPI"
echo ""
echo "Run './build/revyl --help' to see available commands"
