# Discover App Env Config Design

**Date:** 2026-04-13

## Goal

Simplify app configuration for `utils/discover-app/discover-app.py` by removing JSON-based app definitions entirely and using `.env` keys instead. Prefer explicit `.env` configuration when present, fall back to automatic detection otherwise, and update existing examples/tests to the new downstream app env conventions.

## Explicit Config Contract

Explicit reverse-bin app configuration lives in the app's `.env` file.

### TCP examples

```env
REVERSE_BIN_COMMAND="python3 server.py"
LISTEN=8080
```

```env
REVERSE_BIN_COMMAND="python3 server.py"
LISTEN=127.0.0.1:8080
```

```env
REVERSE_BIN_COMMAND="python3 server.py"
LISTEN=[::1]:8080
```

### Unix socket example

```env
REVERSE_BIN_COMMAND="python3 server.py"
SOCKET_PATH=run/app.sock
```

## Rules

- `REVERSE_BIN_COMMAND` enables explicit config mode.
- In explicit config mode, exactly one of `LISTEN` or `SOCKET_PATH` must be set.
- `SOCKET_PATH` must be a relative path.
- There is no JSON config file anymore.
- Automatic detection remains when `REVERSE_BIN_COMMAND` is absent.

## Discovery Behavior

### Explicit mode

When `.env` contains `REVERSE_BIN_COMMAND`:

- parse the command string into argv
- derive the upstream target from `LISTEN` or `SOCKET_PATH`
- build child env vars using the new conventions below

### Fallback autodetection

When `REVERSE_BIN_COMMAND` is absent:

- support `main.ts`
- support executable `main.py`
- do not support `main.sh`

## Normalization Rules

### `LISTEN`

- If `LISTEN` is digits-only, normalize it to `127.0.0.1:<port>`.
- Otherwise, keep the string as-is.
- Extract the bind port using `rsplit(":", 1)[-1]`, then `int(...)`.
- If port parsing fails, treat it as a hard error.
- Do not add extra validation beyond that.

Examples:

- `LISTEN=8080` -> `127.0.0.1:8080`
- `LISTEN=127.0.0.1:8080` -> unchanged
- `LISTEN=[::1]:8080` -> unchanged
- `LISTEN=http://127.0.0.1:8080` -> accepted because the suffix still yields `8080`
- `LISTEN=foo` -> hard error

### `SOCKET_PATH`

- Config value is just a relative path like `run/app.sock`.
- Convert it internally to `reverse_proxy_to=unix/<absolute path under working_dir>`.
- Reject absolute `SOCKET_PATH` values.

## Child App Env Conventions

These are the env vars passed to the launched app.

### TCP apps

```env
LISTEN=127.0.0.1:8080
```

### Unix socket apps

```env
SOCKET_PATH=/absolute/path/to/run/app.sock
```

Do not pass:

- `REVERSE_PROXY_TO`
- `PORT`

`reverse_proxy_to` still remains in the JSON detector output because reverse-bin/Caddy needs it internally.

## Existing Code and Examples To Update

The repo currently contains examples and tests that rely on `REVERSE_PROXY_TO`. These should be updated to the new conventions.

Expected updates include:

- `utils/discover-app/discover-app.py`
- `utils/discover-app/test_discover_app.py`
- example apps under `examples/reverse-proxy/apps/`
- example READMEs that describe `REVERSE_PROXY_TO`
- integration tests that assert env payloads or app behavior using the old variable

## Testing Expectations

Add or update tests for:

- explicit `.env` config using `REVERSE_BIN_COMMAND + LISTEN`
- explicit `.env` config using `REVERSE_BIN_COMMAND + SOCKET_PATH`
- invalid explicit config:
  - both `LISTEN` and `SOCKET_PATH`
  - neither set
  - absolute `SOCKET_PATH`
  - unparseable `LISTEN` port suffix
- fallback autodetection still works for `main.ts` and executable `main.py`
- `main.sh` is no longer detected
- child env output uses only `LISTEN` or `SOCKET_PATH`
- existing examples/integration tests are updated to the new env contract

## Implementation Approach

Keep the code simple by splitting `discover-app.py` into small helpers:

- load explicit config from `.env`
- normalize `LISTEN`
- normalize `SOCKET_PATH`
- derive child env vars
- choose explicit config first, autodetection second

Avoid additional schema layers or over-validation.
