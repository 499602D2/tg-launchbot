#!/bin/bash

SHA=$(git rev-parse --short HEAD)

LDFLAGS=(
	"-X 'main.GitSHA=$SHA'"
)

echo "Current head at $SHA"
echo "Building LaunchBot..."

go build -o ../launchbot -ldflags="${LDFLAGS[*]}"

echo "Build finished"