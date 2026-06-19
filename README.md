# caddy-reverse-bin

`caddy-reverse-bin` is a Caddy HTTP handler that starts an application process on demand and reverse-proxies requests to it.

This repository contains the Caddy plugin, its development Caddy binary entrypoint, and plugin tests. Opinionated Debian/systemd hosting, packaged runtime helpers, deployment docs, and hosted app examples live in the sibling `reverse-bin-hosting` repository.

## Caddyfile usage

```caddyfile
:8080 {
	reverse-bin {
		exec ./app
		dir /path/to/app
		reverse_proxy_to 127.0.0.1:9000
		health_check GET /health
	}
}
```

Common subdirectives:

- `exec <command> [args...]`: command to launch on demand.
- `dir <path>`: working directory for the command.
- `env KEY=value...`: environment variables for the command.
- `pass_env KEY...`: pass selected parent environment variables.
- `pass_all_env`: pass the full parent environment.
- `reverse_proxy_to <upstream>`: static upstream address, such as `127.0.0.1:9000` or `unix//tmp/app.sock`.
- `dynamic_proxy_detector <command> [args...]`: command that discovers launch/proxy settings dynamically.
- `health_check <METHOD> <PATH> [STATUS]`: health probe before proxying. Without `STATUS`, any `2xx` or `3xx` response is accepted.
- `idle_timeout_ms <ms>`: stop the child process after it has been idle for this long.
- `health_timeout_ms <ms>`: timeout for health checks.
- `termination_grace_ms <ms>`: graceful termination timeout.
- `termination_kill_wait_ms <ms>`: delay before force-killing a process after graceful termination fails.

Non-Unix static upstreams require `health_check` so the handler can tell when the launched process is ready.

## Development

Run all plugin tests:

```bash
go test ./...
```

Build a local Caddy binary with this plugin:

```bash
make build
./caddy list-modules | grep http.handlers.reverse-bin
```

## Hosting package

For the opinionated packaged product with Debian packaging, systemd units, helper runtimes, app discovery scripts, and deployment documentation, use the sibling `reverse-bin-hosting` repository.

## Related projects

- https://github.com/sablierapp/sablier
- https://github.com/losfair/zeroserve
