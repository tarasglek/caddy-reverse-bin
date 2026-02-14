# reverse-bin for Caddy

`reverse-bin` is a Caddy HTTP handler that launches an executable on demand and reverse proxies requests to it.

## What it does
- Starts a backend process when traffic arrives
- Waits for backend readiness
- Proxies matching requests
- Stops the process after a configurable idle timeout

## Basic Caddyfile example

```caddy
reverse-bin /app* {
    exec ./my-backend --port 8080
    reverse_proxy_to 127.0.0.1:8080
    readiness_check GET /health
    idle_timeout_ms 30000
}
```

## Notes
- `exec` is required
- `reverse_proxy_to` can be static, or discovered dynamically when configured
- Prefer readiness checks for robust startup behavior
