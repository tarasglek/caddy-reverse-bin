#!/bin/bash
set -ex

# Change to the directory of this script
export CONFIG_DIR=$(realpath $(dirname "$0"))
pushd $CONFIG_DIR/../..

air="go run github.com/air-verse/air@v1.64.4"

# Call the central run script
$air --build.entrypoint "./tmp/caddy"  --build.cmd "go build -o ./tmp/caddy cmd/caddy/main.go" -build.include_ext "go,config,json" --build.include_file /home/taras/Documents/caddy-reverse-bin/examples/reverse-proxy/Caddyfile -build.args_bin "run --adapter caddyfile --config $CONFIG_DIR/Caddyfile" -d
