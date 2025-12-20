#!/bin/bash
set -ex

# Go to the root of the repository
pushd "$(dirname "$0")/../.." > /dev/null

# Build Caddy with the local version of the cgi module
go build -o caddy ./cmd/caddy

# Go back to the example directory
popd > /dev/null
pushd "$(dirname "$0")" > /dev/null

# Ensure the script is executable
chmod +x hello.sh

# Run Caddy with the example configuration
../../caddy run --config caddy.config --adapter caddyfile
popd > /dev/null
