#!/bin/bash
set -ex

# Change to the directory of this script
export CONFIG_DIR=$(realpath $(dirname "$0"))
pushd $CONFIG_DIR/../..

air="go run github.com/air-verse/air@v1.64.4"

# Build the example backend spawned by reverse-bin.
go build -o "$CONFIG_DIR/apps/go-echo/go-echo" "$CONFIG_DIR/apps/go-echo"

# Call the central run script. The example Caddyfile also spawns ./tmp/caddy as
# a child static file server to demonstrate reverse-bin managing a Caddy subprocess.
$air --build.entrypoint "./tmp/caddy"  --build.cmd "go build -o ./tmp/caddy cmd/caddy/main.go" -build.include_ext "go,config,json" --build.include_file "$CONFIG_DIR/Caddyfile" -build.args_bin "run --adapter caddyfile --config $CONFIG_DIR/Caddyfile" -d
