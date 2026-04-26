#!/usr/bin/env sh
set -eu

usage() {
  echo "Usage: $0 APP_DIR [HTTP_PORT]" >&2
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  usage
  exit 2
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
APP_DIR_INPUT=$1
HTTP_PORT=${2:-9080}

case "$APP_DIR_INPUT" in
  /*) APP_DIR=$APP_DIR_INPUT ;;
  *) APP_DIR=$(CDPATH= cd -- "$APP_DIR_INPUT" && pwd) ;;
esac

if [ ! -d "$APP_DIR" ]; then
  echo "APP_DIR does not exist: $APP_DIR" >&2
  exit 1
fi

case "$HTTP_PORT" in
  ''|*[!0-9]*)
    echo "HTTP_PORT must be numeric: $HTTP_PORT" >&2
    exit 2
    ;;
esac

TEMP_CADDYFILE=$(mktemp "${TMPDIR:-/tmp}/reverse-bin-app.XXXXXX.caddy")
cleanup() {
  rm -f "$TEMP_CADDYFILE"
}
trap cleanup EXIT INT TERM

cat > "$TEMP_CADDYFILE" <<EOF
{
	admin off
	http_port $HTTP_PORT
}

http://127.0.0.1:$HTTP_PORT {
	reverse-bin {
		dynamic_proxy_detector $REPO_ROOT/utils/discover-app/discover-app.py $APP_DIR
		health_check HEAD /
		idle_timeout_ms 300000
		health_timeout_ms 15000
		termination_grace_ms 5000
		termination_kill_wait_ms 1000
	}
}
EOF

printf 'reverse-bin app runner\n  app: %s\n  url: http://127.0.0.1:%s\n  caddyfile: %s\n' "$APP_DIR" "$HTTP_PORT" "$TEMP_CADDYFILE" >&2

cd "$REPO_ROOT"
exec go run ./cmd/caddy run --adapter caddyfile --config "$TEMP_CADDYFILE"
