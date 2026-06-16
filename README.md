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
- SOPS age identity: `/var/lib/reverse-bin/keys/age.key`
- SOPS age recipient: `/var/lib/reverse-bin/keys/age.pub`
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
- The package generates the age identity once on install and never overwrites the private key.

## App lifecycle model

- Apps live under `/var/lib/reverse-bin/apps/<app-name>`.
- Reverse-bin discovers each app dynamically and decides how to launch it.
- On incoming request, reverse-bin starts the app if it is not already running.
- The app runs as a subprocess behind a local TCP port or Unix socket.
- Caddy/reverse-bin proxies public HTTP traffic to that local app subprocess.
- Reverse-bin injects runtime environment such as `REVERSE_BIN_HOST`, `REVERSE_BIN_PORT`, and `HOME`.
- `HOME` is set to `/var/lib/reverse-bin/apps/<app-name>/data` when the app does not define `HOME` itself.
- App runtime state should live under `/var/lib/reverse-bin/apps/<app-name>/data`.
- Apps run inside a `landrun` sandbox with read access to app source, read-write access to app `data/`, required runtime/system paths, and any network/bind permissions granted by the discovered launch policy.
- App subprocesses are reused while active and terminated after the idle timeout.
- If the discovered runtime uses watch mode, edits in the app directory can restart the subprocess automatically.

Important implications:

- The deployed app copy is `/var/lib/reverse-bin/apps/<app-name>`.
- Runtime app path is `/var/lib/reverse-bin/apps/<app-name>`. It may be bind-mounted elsewhere. Check with `mount` or `findmnt` before assuming a separate checkout is not live.
- A developer checkout is not the running code unless it has been deployed there.
- Direct edits under `/var/lib/reverse-bin/apps/` can affect production immediately.
- Files under app `data/` are runtime state, not source.
- Package managers and language runtimes should put caches, managed toolchains, virtualenvs, databases, and generated files under app `data/`.
- For Deno apps with npm/remote imports, put cache state under app `data/` in `.env`:
  ```sh
  DENO_DIR=data/.cache/deno
  ```
- Deno apps should also expose `GET /` or configure `REVERSE_BIN_HEALTH_PATH=/health`.
- The Debian package includes `uv` at `/usr/lib/reverse-bin/uv`, and `reverse-bin.service` puts `/usr/lib/reverse-bin` on `PATH`.
- For `uv` apps, prefer app-local state such as `UV_CACHE_DIR=$HOME/.cache/uv`, `UV_PYTHON_INSTALL_DIR=$HOME/.local/share/uv/python`, or a prebuilt virtualenv under `data/`; otherwise uv-managed Python installs created outside app `data/` may not be exposed by the sandbox.
- Long-lived app subprocesses can hold file locks or database handles until idle termination.

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
- App launch scripts may use packaged `uv` directly; it is available on the reverse-bin service `PATH`.
- `HOME` defaults to app `data/`, so tools that respect `HOME` can keep runtime state there. If a tool uses separate cache/data variables, set them explicitly in `.env` or `launch.sh`.
- `REVERSE_BIN_HEALTH_STATUS` is optional and enables exact-status health checks like registry `/v2/` returning `401`.

Wrangler apps use this same explicit launch-script pattern; there is no Wrangler-specific detector or separate sandbox wrapper in the compatibility path.

## Encrypted app env files

Apps may use either plaintext `.env` or encrypted `secrets.enc.json`, not both. `discover-app.py` rejects app directories containing both files to avoid ambiguous secret sources.

Create plaintext dotenv first:

```sh
REVERSE_BIN_COMMAND=./launch.sh
REVERSE_BIN_HOST=127.0.0.1
REVERSE_BIN_PORT=
SECRET_KEY=change-me
```

The package seeds `/var/lib/reverse-bin/apps/.sops.yaml` with the reverse-bin server key. For app checkouts elsewhere, create `.sops.yaml` with the package age recipient plus your SSH public key. The age recipient lets reverse-bin decrypt at runtime; the SSH public key lets you edit secrets without access to the server private key.

```bash
SERVER_RECIPIENT=$(cat /var/lib/reverse-bin/keys/age.pub)
SSH_RECIPIENT="$(awk '{print $1" "$2}' ~/.ssh/id_ed25519.pub)"
cat > .sops.yaml <<EOF
creation_rules:
  - path_regex: secrets\\.enc\\.json$
    key_groups:
      - age:
        - $SERVER_RECIPIENT # reverse-bin server key: /var/lib/reverse-bin/keys/age.pub
        - $SSH_RECIPIENT # your SSH public key for editing secrets
EOF
```

For GitHub-managed SSH keys, `github-to-sops` can add users to an existing `.sops.yaml`. Example: add your current GitHub account using `gh` and `uv`:

```bash
GITHUB_USER=$(gh api user --jq .login)
uvx github-to-sops --github-users "$GITHUB_USER" import-keys --inplace-edit .sops.yaml
```

Later, refresh GitHub keys and re-encrypt committed secrets with:

```bash
uvx github-to-sops updatekeys
```

See: https://github.com/tarasglek/github-to-sops

Encrypt `.env` to JSON, then remove plaintext only after encryption succeeds:

```bash
sops --encrypt --input-type dotenv --output-type json --filename-override secrets.enc.json .env > secrets.enc.json && rm .env
```

At runtime, systemd sets `SOPS_AGE_KEY_FILE=/var/lib/reverse-bin/keys/age.key`. `discover-app.py` decrypts `secrets.enc.json` in memory with bundled `/usr/lib/reverse-bin/sops`, asks SOPS to output dotenv, and passes parsed keys to the child app. The private key stays outside app directories; child apps only receive `SOPS_AGE_KEY_FILE` if the app env explicitly defines it.

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

## related projects

* https://github.com/sablierapp/sablier
* https://github.com/losfair/zeroserve
