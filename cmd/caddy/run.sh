#!/bin/bash
set -ex

# Get the directory of this script (cmd/caddy)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Root of repo
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Build Caddy in the root
pushd "$REPO_ROOT" > /dev/null
go build -o caddy ./cmd/caddy
popd > /dev/null

# Run Caddy using the config in the current working directory
CONFIG_FILE="${1:-caddy.config}"
"$REPO_ROOT/caddy" run --config "$CONFIG_FILE" --adapter caddyfile
