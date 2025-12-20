#!/bin/bash
set -ex

# Ensure the script is executable
chmod +x hello.sh

# Call the central run script
../../cmd/caddy/run.sh
