#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_DIR/build"

cd "$PROJECT_DIR"

VERSION="${VERSION:-$(tr -d '[:space:]' < VERSION)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo "none")}"
DATE="${DATE:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}"

LDFLAGS="-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

echo "Revyl CLIs - Cross-Platform Build"
echo "================================="
echo "Version: $VERSION"
echo "Commit: $COMMIT"
echo "Date: $DATE"
echo ""

rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

PLATFORMS=(
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
    "windows/amd64"
    "windows/arm64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"

    for BINARY in revyl revyl-computer; do
        OUTPUT_NAME="${BINARY}-${GOOS}-${GOARCH}"
        if [ "$GOOS" = "windows" ]; then
            OUTPUT_NAME="${OUTPUT_NAME}.exe"
        fi

        echo "Building $BINARY for $GOOS/$GOARCH..."

        GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=0 go build \
            -ldflags "$LDFLAGS" \
            -o "$BUILD_DIR/$OUTPUT_NAME" \
            "./cmd/$BINARY"

        echo "  ✓ $OUTPUT_NAME"
    done
done

echo ""
echo "Build complete! Binaries in $BUILD_DIR:"
ls -la "$BUILD_DIR"

echo ""
echo "Generating checksums..."
cd "$BUILD_DIR"
shasum -a 256 revyl-* > checksums.txt
echo "  ✓ checksums.txt"

echo ""
echo "Done!"
