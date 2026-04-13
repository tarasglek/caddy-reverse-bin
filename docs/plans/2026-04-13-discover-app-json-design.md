# Discover App JSON Design

**Date:** 2026-04-13

## Goal

Add `reverse-bin-app.json` as a first-class app definition format for `utils/discover-app/discover-app.py`, prefer it when present, keep automatic detection as a fallback, and remove `main.sh` support.

## Requirements

- If `reverse-bin-app.json` exists in the app directory, use it instead of automatic detection.
- Keep automatic detection when the config file is absent.
- Automatic detection should support:
  - `main.ts`
  - executable `main.py`
- Automatic detection should no longer support `main.sh`.
- `reverse-bin-app.json` should support:
  - `command: string[]`
  - `socket: int | string`
- `socket` should accept:
  - TCP port number, e.g. `8080`
  - host:port string, e.g. `127.0.0.1:8080`
  - relative unix socket path, e.g. `run/app.sock`
- Keep existing result JSON shape from `discover-app.py`.
- Keep current sandboxing behavior and existing env handling unless the new config format requires normalization changes.

## Recommended Approach

Refactor the detector into small focused helpers:

1. Load app config if `reverse-bin-app.json` exists.
2. Normalize config `socket` into final `reverse_proxy_to`.
3. Build executable from config or autodetection.
4. Reuse the existing result-building and sandbox assembly pipeline.

This keeps the behavior change small while making the code easier to read and extend.

## Data Format

Example config using a TCP port:

```json
{
  "command": ["python3", "server.py"],
  "socket": 8080
}
```

Example config using a host:port string:

```json
{
  "command": ["deno", "serve", "--port", "9000", "main.ts"],
  "socket": "127.0.0.1:9000"
}
```

Example config using a relative unix socket path:

```json
{
  "command": ["./server"],
  "socket": "run/app.sock"
}
```

## Socket Normalization Rules

- `8080` -> `127.0.0.1:8080`
- `"127.0.0.1:8080"` -> unchanged
- `"run/app.sock"` -> `unix/<absolute path under working_dir>`
- `"unix/run/app.sock"` -> also normalize to `unix/<absolute path under working_dir>`
- absolute unix socket paths should be rejected to keep config relocatable and consistent with current `.env` handling

## Validation Rules

- `command` must be a non-empty JSON array of strings.
- `socket` must be an integer or string.
- relative unix socket paths must not escape the app directory by being absolute.
- invalid config should fail clearly and early.

## Testing Strategy

Add focused unit tests for:

- config parsing returns the expected command and normalized socket target
- integer `socket` becomes `127.0.0.1:<port>`
- host:port string remains unchanged
- relative unix socket path becomes an absolute `unix/...` target
- `main.sh` is no longer auto-detected
- fallback autodetection still works for `main.ts` and executable `main.py`

Keep tests small, explicit, and behavior-oriented.
