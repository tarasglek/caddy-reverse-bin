#!/usr/bin/env bash
set -euo pipefail

BIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_PATH="$BIN_DIR/../.config/Caddyfile"

export PATH="$BIN_DIR:$PATH"

: "${OPS_EMAIL:?OPS_EMAIL must be set (example: ops@example.com)}"
: "${DOMAIN_SUFFIX:?DOMAIN_SUFFIX must be set (example: example.com)}"

if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
  echo "error: do not run as root; run as a normal user with cap_net_bind_service on caddy" >&2
  echo "fix with: sudo setcap 'cap_net_bind_service=+ep' $BIN_DIR/caddy" >&2
  exit 1
fi

if ! command -v getcap >/dev/null 2>&1; then
  echo "error: getcap is required to verify privileged port binding capability" >&2
  echo "install libcap tools and run: sudo setcap 'cap_net_bind_service=+ep' $BIN_DIR/caddy" >&2
  exit 1
fi

if ! getcap "$BIN_DIR/caddy" 2>/dev/null | grep -q 'cap_net_bind_service=ep'; then
  echo "error: caddy is missing cap_net_bind_service; binding :80/:443 as non-root will fail" >&2
  echo "fix with: sudo setcap 'cap_net_bind_service=+ep' $BIN_DIR/caddy" >&2
  echo "verify with: getcap $BIN_DIR/caddy" >&2
  exit 1
fi

mkdir -p "$BIN_DIR/../.run/allow-domain"

# Prime discover-app (uv/python deps) during startup for predictable first request behavior.
"$BIN_DIR/discover-app.py" --help >/dev/null

exec "$BIN_DIR/caddy" run --config "$CONFIG_PATH" --adapter caddyfile "$@"
