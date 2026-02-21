# reverse-bin apps config example

This example shows a production-style `reverse-bin` deployment layout where:

- Caddy runs as a **normal (non-root) user**
- Caddy can still bind `:80/:443` via Linux file capability
- domain admission is enforced by a sandboxed allow-checker
- backend apps are launched through a sandboxed runtime model

## 1) Non-root Caddy with privileged port binding

`.bin/run.sh` refuses to run as root. Instead, run as a normal user and grant only the bind capability to the Caddy binary:

```bash
sudo setcap 'cap_net_bind_service=+ep' /path/to/reverse-bin/.bin/caddy
getcap /path/to/reverse-bin/.bin/caddy
```

Why: this keeps the main proxy process unprivileged while still allowing TLS on standard ports.

`run.sh` verifies this capability at startup and fails fast with actionable error messages if it is missing.

## 2) App-per-directory model

The deployment root is expected at `$HOME/reverse-bin` with this shape:

- `.bin/` runtime binaries and helpers
- `.config/` Caddy config
- `.run/` runtime sockets/state
- `<app-name>/` one directory per app

On-demand TLS ask requests are validated against app directories:

- incoming domain must match `.<DOMAIN_SUFFIX>`
- app name is extracted from `<app>.<DOMAIN_SUFFIX>`
- app name must be a single label (no extra dots)
- app directory must exist under app root

If validation fails, the checker returns `403`.

## 3) Sandboxed allow checker

`allow-domain.py` is not exposed directly to the internet. Caddy talks to it over a local Unix socket through an internal route.

The checker is executed under `landrun` with constrained filesystem/env access (see `Caddyfile`):

- read-only root
- writable runtime directory only for its socket
- only required environment variables passed through

This reduces blast radius if the checker is compromised.

## 4) Sandboxed app processes

Final app processes are also intended to run in a constrained sandbox model (via `dynamic_proxy_detector` + app launcher command).

Security intent:

- each app gets explicit filesystem/env permissions
- app runtime is isolated from Caddy control plane concerns
- compromise of one app should not imply unrestricted host access

## 5) Request flow (high level)

1. Client connects to `https://<app>.<DOMAIN_SUFFIX>`.
2. Caddy on-demand TLS performs `ask` against internal `/allow-domain`.
3. Sandboxed allow checker validates suffix + app directory existence.
4. If allowed, Caddy obtains/serves certificate and routes request.
5. Backend is started/routed by reverse-bin dynamic detection.

## 6) Basic usage

Build bundle:

```bash
examples/reverse-bin-apps-config/bundle.sh
```

Run bundle (as non-root):

```bash
cd /path/to/reverse-bin
export OPS_EMAIL=ops@example.com
export DOMAIN_SUFFIX=example.com
./.bin/run.sh
```

If startup fails, check capability and environment variable errors first.
