# reverse-bin plugin example

This example shows two ways to use `reverse-bin` to spawn subprocess backends and proxy to them.

Build and run it with:

```bash
make example-run
```

Smoke-test it with:

```bash
make example-smoke
```

Then open `http://localhost:9080` routes below.

## Static detector routes

These routes use static Caddyfile configuration. Each `reverse-bin` block directly declares the subprocess to spawn and the upstream to proxy to.

- `/static-detector/static/` spawns `./tmp/caddy file-server` and serves `apps/static/index.html`.
- `/static-detector/echo/` spawns the compiled Go echo server at `apps/go-echo/go-echo` and proxies over a Unix socket.

## Dynamic detector routes

These routes use one `reverse-bin` block with `dynamic_proxy_detector`. The detector is a small Go program built by `run.sh`; it inspects the requested path and emits reverse-bin JSON config.

- `/dynamic-detector/static/` detects `apps/static/index.html` and returns config to spawn `./tmp/caddy file-server`.
- `/dynamic-detector/go-echo/` detects executable `apps/go-echo/go-echo` and returns config to spawn it.

This detector is intentionally only a proof of concept. For more advanced app detection, see https://github.com/tarasglek/reverse-bin-hosting.
