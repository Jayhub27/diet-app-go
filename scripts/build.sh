#!/bin/bash
set -e

VERSION=${VERSION:-"dev"}
BUILD_DIR="build"
BINARY_NAME="dietapp"

rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

echo "Building DietApp v${VERSION}..."

build() {
    local goos=$1
    local goarch=$2
    local output=$3
    local ext=""
    
    if [ "$goos" = "windows" ]; then
        ext=".exe"
    fi
    
    echo "  Building ${goos}/${goarch}..."
    GOOS=$goos GOARCH=$goarch CGO_ENABLED=0 go build \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o "${BUILD_DIR}/${output}${ext}" \
        ./cmd/server
    
    echo "    ✓ ${BUILD_DIR}/${output}${ext}"
}

build "darwin" "amd64" "dietapp-darwin-amd64"
build "darwin" "arm64" "dietapp-darwin-arm64"
build "linux" "amd64" "dietapp-linux-amd64"
build "linux" "arm64" "dietapp-linux-arm64"
build "windows" "amd64" "dietapp-windows-amd64"

echo ""
echo "Build complete! Binaries in ${BUILD_DIR}/"
ls -lh "$BUILD_DIR"
