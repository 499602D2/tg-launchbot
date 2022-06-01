#!/bin/bash

SHA=$(git rev-parse --short HEAD)

LDFLAGS=(
	"-X 'main.GitSHA=$SHA'"
)

go build -x -o ../launchbot -ldflags="${LDFLAGS[*]}"
