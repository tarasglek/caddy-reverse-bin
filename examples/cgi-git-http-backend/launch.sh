#!/bin/sh
set -eu
exec ./bin/caddy run --config ./Caddyfile --adapter caddyfile
