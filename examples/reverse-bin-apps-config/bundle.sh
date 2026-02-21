#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$ROOT_DIR/../.." && pwd)"
OUTPUT_TAR="${1:-$ROOT_DIR/reverse-bin.tar.gz}"
SAMPLE_APP="python3-unix-echo"
SAMPLE_APP_SOURCE="$REPO_ROOT/examples/reverse-proxy/apps/$SAMPLE_APP"

find_from_path() {
  local bin_name="$1"
  local resolved
  resolved="$(which "$bin_name" 2>/dev/null || true)"
  if [[ -z "$resolved" ]]; then
    echo "error: $bin_name binary not found in PATH" >&2
    exit 1
  fi
  echo "$resolved"
}

if [[ -n "${CADDY_BIN:-}" ]]; then
  CADDY_PATH="$CADDY_BIN"
else
  (
    cd "$REPO_ROOT"
    make build CADDY_BIN=./caddy
  )
  CADDY_PATH="$REPO_ROOT/caddy"
fi
UV_PATH="$(find_from_path uv)"
LANDRUN_PATH="$(find_from_path landrun)"

STAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$STAGE_DIR"' EXIT
STAGE_ROOT="$STAGE_DIR/reverse-bin"

mkdir -p "$STAGE_ROOT/.config" "$STAGE_ROOT/.bin" "$STAGE_ROOT/.run"
cp "$ROOT_DIR/Caddyfile" "$STAGE_ROOT/.config/Caddyfile"
cp "$ROOT_DIR/allow-domain.py" "$STAGE_ROOT/.bin/allow-domain.py"
cp "$ROOT_DIR/run.sh" "$STAGE_ROOT/.bin/run.sh"
cp "$CADDY_PATH" "$STAGE_ROOT/.bin/caddy"
cp "$UV_PATH" "$STAGE_ROOT/.bin/uv"
cp "$LANDRUN_PATH" "$STAGE_ROOT/.bin/landrun"
chmod +x "$STAGE_ROOT/.bin/caddy" "$STAGE_ROOT/.bin/run.sh" "$STAGE_ROOT/.bin/allow-domain.py" "$STAGE_ROOT/.bin/uv" "$STAGE_ROOT/.bin/landrun"

if [[ ! -d "$SAMPLE_APP_SOURCE" ]]; then
  echo "error: sample app not found at $SAMPLE_APP_SOURCE" >&2
  exit 1
fi

cp -R "$SAMPLE_APP_SOURCE" "$STAGE_ROOT/$SAMPLE_APP"

rm -f "$OUTPUT_TAR"
(
  cd "$STAGE_DIR"
  tar -czf "$OUTPUT_TAR" reverse-bin
)

echo "created $OUTPUT_TAR"
echo "archive contents:"
tar -tzf "$OUTPUT_TAR"
