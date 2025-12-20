#!/bin/bash
set -ex

# Start python backend in background
python3 -m http.server 8001 &
BACKEND_PID=$!

# Ensure backend is killed when script exits
trap "kill $BACKEND_PID" EXIT

# Go to the root of the repository and build Caddy
pushd "$(dirname "$0")/../.." > /dev/null
go build -o caddy ./cmd/caddy
popd > /dev/null

# Go to the example directory
pushd "$(dirname "$0")" > /dev/null

# Run Caddy with the proxy config
../../caddy run --config caddy.config --adapter caddyfile

popd > /dev/null
