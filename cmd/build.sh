#!/bin/bash
set -e  # Exit on any error

# Get git SHA for version info
SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Get the latest git tag, or use "dev" if no tags exist
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")

# If we're not exactly on a tag, append the commit count and SHA
if [ "$(git describe --tags --exact-match 2>/dev/null)" != "$VERSION" ]; then
    # Get number of commits since last tag
    COMMITS=$(git rev-list ${VERSION}..HEAD --count 2>/dev/null || echo "0")
    if [ "$COMMITS" != "0" ]; then
        VERSION="${VERSION}+${COMMITS}"
    fi
fi

LDFLAGS=(
	"-X 'main.GitSHA=$SHA'"
	"-X 'main.Version=$VERSION'"
)

echo "Building version: $VERSION"
echo "Current head at: $SHA"

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