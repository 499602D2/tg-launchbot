#!/bin/bash

SHA=$(git rev-parse --short HEAD)

LDFLAGS=(
	"-X 'main.GitSHA=$SHA'"
)

echo "Current head at $SHA"

echo "Ensuring packages are up to date..."
go get all
echo "Packages updated"

echo "Building LaunchBot..."
go build -o ../launchbot -ldflags="${LDFLAGS[*]}"

echo "Build finished!"