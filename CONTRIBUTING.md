# Contributing

This repository contains the `caddy-reverse-bin` Caddy plugin and its tests.

## Development workflow

1. Format Go code before committing:

   ```bash
   gofmt -w <changed-go-files>
   ```

2. Run the plugin test suite:

   ```bash
   go test ./...
   ```

3. Build a local Caddy binary when changing module registration or Caddyfile behavior:

   ```bash
   make build
   ./caddy list-modules | grep http.handlers.reverse-bin
   ```

## Test expectations

- Keep tests focused on plugin behavior.
- Comments should state the intent of each test.
- HTTP assertions should be specific and stable.
- Do not add retry loops to mask flakes; fix the underlying cause.

## Hosting package

Debian packaging, systemd deployment, bundled helper runtimes, app discovery policy, and hosted-app compatibility workflows live in the sibling `reverse-bin-hosting` repository.
