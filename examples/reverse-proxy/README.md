# reverse-bin plugin example

This example shows two ways to use `reverse-bin` to spawn subprocess backends and proxy to them.

Run it with:

```bash
./examples/reverse-proxy/run.sh
```

Then open `http://localhost:9080` routes below.

## Static detector routes

These routes use static Caddyfile configuration. Each `reverse-bin` block directly declares the subprocess to spawn and the upstream to proxy to.

- `/static-detector/static/` spawns `./tmp/caddy file-server` and serves `apps/static-site/index.html`.
- `/static-detector/echo/` spawns the compiled Go echo server at `apps/go-echo/go-echo` and proxies over a Unix socket.

## Dynamic detector routes

These routes use one `reverse-bin` block with `dynamic_proxy_detector`. The detector is a small Go program built by `run.sh`; it inspects the requested path and emits reverse-bin JSON config.

- `/dynamic-detector/static/` detects the static app and returns config to spawn `./tmp/caddy file-server`.
- `/dynamic-detector/echo/` detects the Go echo app and returns config to spawn `apps/go-echo/go-echo`.

This detector is intentionally only a proof of concept. For more advanced app detection, see https://github.com/tarasglek/reverse-bin-hosting.
