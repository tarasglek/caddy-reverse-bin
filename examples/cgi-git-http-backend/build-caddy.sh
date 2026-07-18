#!/bin/sh
set -eu

app_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
image='caddy:2.10.2-builder@sha256:01668408cc26e2e00c9d067c30cb43b2ba14ad1f2808beda55503cb2a31f59dc'
container=$(docker create "$image" \
  xcaddy build v2.10.2 \
  --with github.com/aksdb/caddy-cgi/v2@v2.2.7 \
  --output /tmp/caddy)
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT INT TERM

docker start -a "$container"
mkdir -p "$app_dir/bin"
docker cp "$container:/tmp/caddy" "$app_dir/bin/caddy.new"
chmod 0755 "$app_dir/bin/caddy.new"
mv "$app_dir/bin/caddy.new" "$app_dir/bin/caddy"
"$app_dir/bin/caddy" list-modules | grep -qx 'http.handlers.cgi'
