# reverse-bin for Caddy

`reverse-bin` is a Caddy HTTP handler that launches an executable on demand and reverse-proxies requests to it.

## Scope

This repository contains the Caddy plugin implementation and plugin tests. Opinionated Debian/systemd hosting, packaged helper runtimes, app discovery policy, and deployment documentation live in the sibling `reverse-bin-hosting` repository.

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

## Development

Run tests:

```bash
go test ./...
```

Build a local Caddy binary with this plugin:

```bash
make build
```
