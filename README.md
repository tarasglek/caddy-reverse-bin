# caddy-reverse-bin

`reverse-bin` is a Caddy-based on-demand app launcher packaged for Debian systems.

## Debian package layout

The package installs these primary paths:

- binary: `/usr/bin/reverse-bin-caddy`
- config entrypoint: selected by `REVERSE_BIN_CADDYFILE` in `/etc/default/reverse-bin`
- packaged configs: `/etc/reverse-bin/Caddyfile.acme`, `/etc/reverse-bin/Caddyfile.http-only`
- defaults: `/etc/default/reverse-bin`
- helper scripts and bundled runtimes: `/usr/lib/reverse-bin/`
- writable app root: `/var/lib/reverse-bin/apps/`
- service home: `/var/lib/reverse-bin/home`
- packaged examples: `/usr/share/doc/reverse-bin/examples/`

## What it does

1. Runs a custom Caddy binary with the `reverse-bin` handler.
2. Uses `discover-app.py` to detect app entrypoints and proxy targets.
3. Uses `landrun` and helper scripts installed under `/usr/lib/reverse-bin/`.

## Build the Debian package

```bash
make deb
```

This produces a `.deb` in the parent directory.

## Runtime model

- Caddy runs from the packaged systemd unit.
- The service loads deployment-specific variables from `/etc/default/reverse-bin`.
- The service reads the Caddy config path from `REVERSE_BIN_CADDYFILE`.
- App directories live under `/var/lib/reverse-bin/apps/`.
- Example apps ship under `/usr/share/doc/reverse-bin/examples/` and can be copied into the app root.

## Example deployment flow

```bash
sudo editor /etc/default/reverse-bin
sudo install -d -o reverse-bin -g reverse-bin /var/lib/reverse-bin/apps
sudo cp -a /usr/share/doc/reverse-bin/examples/python3-unix-echo /var/lib/reverse-bin/apps/
sudo chown -R reverse-bin:reverse-bin /var/lib/reverse-bin/apps/python3-unix-echo
sudo systemctl enable reverse-bin.service
sudo systemctl restart reverse-bin.service
```

Set these values in `/etc/default/reverse-bin` before restarting:

```sh
OPS_EMAIL=admin@overthinker.dev
DOMAIN_SUFFIX=overthinker.dev
```

## TLS and Cloudflare Tunnel modes

For public HTTPS with Caddy-managed on-demand ACME certificates, use:

```sh
REVERSE_BIN_CADDYFILE=/etc/reverse-bin/Caddyfile.acme
```

When reverse-bin is behind a trusted proxy or Cloudflare Tunnel that terminates TLS, use HTTP-only mode:

```sh
REVERSE_BIN_CADDYFILE=/etc/reverse-bin/Caddyfile.http-only
REVERSE_BIN_HTTP_PORT=7777
```

Point the tunnel ingress at `http://localhost:${REVERSE_BIN_HTTP_PORT}`. HTTP-only mode should not be exposed directly to the public internet without a trusted TLS-terminating proxy in front of it.

## Health checks

Use health names in Caddyfiles:

```caddyfile
health_check GET /health
health_timeout_ms 15000
```

A plain `health_check METHOD PATH` accepts any `2xx` or `3xx` response. For auth-protected endpoints, add one exact expected status:

```caddyfile
health_check GET /v2/ 401
```

## Explicit launch-script apps

Apps can opt into a generic launch-script contract through `.env` in the app directory:

```sh
REVERSE_BIN_COMMAND=./launch.sh
REVERSE_BIN_HOST=127.0.0.1
REVERSE_BIN_PORT=
REVERSE_BIN_HEALTH_METHOD=GET
REVERSE_BIN_HEALTH_PATH=/v2/
REVERSE_BIN_HEALTH_STATUS=401
```

- `REVERSE_BIN_COMMAND` is the command `discover-app.py` runs.
- Blank `REVERSE_BIN_PORT=` asks the detector to allocate a free TCP port and inject the resolved value into the child environment.
- Missing `REVERSE_BIN_HOST` defaults to `127.0.0.1`.
- App launch scripts should bind to `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
- `REVERSE_BIN_HEALTH_STATUS` is optional and enables exact-status health checks like registry `/v2/` returning `401`.

Wrangler apps use this same explicit launch-script pattern; there is no Wrangler-specific detector or separate sandbox wrapper in the compatibility path.

## Manual app smoke runner

Run any app directory through local reverse-bin/Caddy without Debian packaging:

```bash
utils/run-reverse-bin-app.sh /path/to/app 9080
curl -i http://127.0.0.1:9080/
```

Wrangler registry smoke example:

```bash
utils/run-reverse-bin-app.sh ~/Downloads/serverless-registry 9080
curl -i http://127.0.0.1:9080/v2/
```

Expected registry smoke result: HTTP `401` from the app, proving reverse-bin launched and proxied it.
