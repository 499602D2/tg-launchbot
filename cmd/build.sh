#!/bin/bash
set -e  # Exit on any error

# Get git SHA for version info
SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS=(
	"-X 'main.GitSHA=$SHA'"
)

echo "Current head at $SHA"

echo "Downloading dependencies..."
if go mod download; then
    echo "Dependencies downloaded"
else
    echo "Failed to download dependencies"
    exit 1
fi

echo "Building LaunchBot..."
if go build -o ../launchbot -ldflags="${LDFLAGS[*]}"; then
    echo "Build finished successfully!"
    echo "Binary created at: ../launchbot"
else
    echo "Build failed!"
    exit 1
fi