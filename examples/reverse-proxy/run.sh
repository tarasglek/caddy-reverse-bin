#!/bin/bash
set -ex

# Call the central run script
"$(dirname "$0")/../../cmd/caddy/run.sh" caddy.config
