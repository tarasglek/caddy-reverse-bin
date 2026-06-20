#!/bin/bash
set -ex

# Change to the directory of this script.
export CONFIG_DIR=$(realpath $(dirname "$0"))
pushd "$CONFIG_DIR/../.."

# Build the example backend and detector spawned by reverse-bin.
go build -o "$CONFIG_DIR/apps/go-echo/go-echo" "$CONFIG_DIR/apps/go-echo"
go build -o "$CONFIG_DIR/detector/example-detector" "$CONFIG_DIR/detector"

# Build and run the Caddy binary with this plugin. The example Caddyfile also
# spawns ./tmp/caddy as a child static file server to demonstrate reverse-bin
# managing a Caddy subprocess.
go build -o ./tmp/caddy ./cmd/caddy
exec ./tmp/caddy run --adapter caddyfile --config "$CONFIG_DIR/Caddyfile"
