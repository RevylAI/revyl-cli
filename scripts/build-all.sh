#!/bin/bash
# Cross-platform build script for Revyl CLI
# Builds binaries for Linux, macOS, and Windows

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_DIR/build"
CMD_DIR="$PROJECT_DIR/cmd/revyl"

# Version info
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "none")}"
DATE="${DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"

LDFLAGS="-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

echo "Revyl CLI - Cross-Platform Build"
echo "================================="
echo "Version: $VERSION"
echo "Commit: $COMMIT"
echo "Date: $DATE"
echo ""

# Clean build directory
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Platforms to build for
PLATFORMS=(
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
    "windows/amd64"
)

cd "$PROJECT_DIR"

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    
    OUTPUT_NAME="revyl-${GOOS}-${GOARCH}"
    if [ "$GOOS" = "windows" ]; then
        OUTPUT_NAME="${OUTPUT_NAME}.exe"
    fi
    
    echo "Building $GOOS/$GOARCH..."
    
    GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags "$LDFLAGS" \
        -o "$BUILD_DIR/$OUTPUT_NAME" \
        "$CMD_DIR"
    
    echo "  ✓ $OUTPUT_NAME"
done

echo ""
echo "Build complete! Binaries in $BUILD_DIR:"
ls -la "$BUILD_DIR"

# Generate checksums
echo ""
echo "Generating checksums..."
cd "$BUILD_DIR"
shasum -a 256 revyl-* > checksums.txt
echo "  ✓ checksums.txt"

echo ""
echo "Done!"
