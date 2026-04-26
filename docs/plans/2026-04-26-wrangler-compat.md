# Wrangler Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make reverse-bin compatible with Wrangler registry apps by supporting auth-protected health checks first, then wiring detector-friendly Wrangler launch configuration.

**Architecture:** Start with the blocking runtime capability: a breaking rename from readiness names to health names plus one optional exact health status, so `/v2/` returning `401` can mark auth-protected Wrangler apps healthy. Then update discover-app health config and generic TCP env config so app-owned launch scripts can receive dynamic `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT` without any Wrangler-specific detector logic.

**Tech Stack:** Go Caddy module, Caddyfile parser, Python discover-app detector, Go integration tests, Python unittest, Wrangler/Miniflare launch scripts.

---

## Design

### Wrangler compatibility target

Target app shape from `~/Downloads/serverless-registry/`:

- `wrangler.toml` describes Worker app.
- `package.json` has local Wrangler dependency.
- `launch.sh` prepares state dirs and runs `wrangler dev`.
- no separate sandbox wrapper script is part of the compatibility path.
- app launch script owns Wrangler prep/state behavior.
- app launch script reads `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
- registry `/v2/` can return `401` and still be alive.

Desired detector `.env` shape after health support:

```sh
REVERSE_BIN_COMMAND=./launch.sh
REVERSE_BIN_HOST=127.0.0.1
REVERSE_BIN_PORT=
REVERSE_BIN_HEALTH_METHOD=GET
REVERSE_BIN_HEALTH_PATH=/v2/
REVERSE_BIN_HEALTH_STATUS=401
```

Detector must derive `reverse_proxy_to` from `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`. Blank `REVERSE_BIN_PORT` means allocate a free TCP port and inject the resolved value into the child env. Missing `REVERSE_BIN_HOST` defaults to `127.0.0.1`. Do not add Wrangler-specific auto-detection; use explicit `.env` launch-script config.

### Manual reverse-bin launch process

Add a dev helper that runs one app through real reverse-bin/Caddy without Debian packaging or systemd. This becomes the generic compatibility workflow for bringing up any new app type: write app `.env`, run it through `utils/run-reverse-bin-app.sh`, curl the expected route, then promote the working `.env`/launch-script pattern into docs or examples.

```sh
utils/run-reverse-bin-app.sh /path/to/app 9080
```

The helper must generate a temporary Caddyfile and run:

```sh
go run ./cmd/caddy run --adapter caddyfile --config "$TEMP_CADDYFILE"
```

Generated Caddyfile shape:

```caddyfile
{
	admin off
	http_port 9080
}

http://127.0.0.1:9080 {
	reverse-bin {
		dynamic_proxy_detector /absolute/repo/utils/discover-app/discover-app.py --no-sandbox /absolute/app
		idle_timeout_ms 300000
		health_timeout_ms 15000
		termination_grace_ms 5000
		termination_kill_wait_ms 1000
	}
}
```

The app `.env` supplies launch command, TCP bind envs, and health overrides. Example Wrangler smoke:

```sh
cat > ~/Downloads/serverless-registry/.env <<'EOF'
REVERSE_BIN_COMMAND=./launch.sh
REVERSE_BIN_HOST=127.0.0.1
REVERSE_BIN_PORT=
REVERSE_BIN_HEALTH_METHOD=GET
REVERSE_BIN_HEALTH_PATH=/v2/
REVERSE_BIN_HEALTH_STATUS=401
EOF

utils/run-reverse-bin-app.sh ~/Downloads/serverless-registry 9080
curl -i http://127.0.0.1:9080/v2/
```

Expected curl result for registry health smoke: HTTP `401` from app, proving reverse-bin launched backend and proxied request.

### Wrangler launch script contract

The Wrangler app launch script must consume the generic reverse-bin TCP envs. For the registry script, `REVERSE_BIN_*` values must win over local defaults:

```sh
HOST="${REVERSE_BIN_HOST:-${HOST:-127.0.0.1}}"
PORT="${REVERSE_BIN_PORT:-${PORT:-9999}}"
```

The Wrangler command should bind to those values:

```sh
wrangler --config "$WRANGLER_CONFIG" --env "$WRANGLER_ENV" dev \
  --ip "$HOST" \
  --port "$PORT" \
  --persist-to "$STATE_DIR" \
  --show-interactive-dev-session=false
```

If the installed Wrangler version does not support `--ip`, keep `REVERSE_BIN_HOST=127.0.0.1` and omit `--ip`; `REVERSE_BIN_PORT` support remains required.

Delete the old sandbox wrapper script from the compatibility workflow. `utils/run-reverse-bin-app.sh` already runs the detector with `--no-sandbox`, and `launch.sh` owns Wrangler prep/state directly.

### Config contract

Use health names only. No compatibility aliases.

```caddyfile
health_check METHOD PATH [STATUS]
health_timeout_ms N
```

Examples:

```caddyfile
health_check GET /health
```

Accepts any `2xx` or `3xx` status.

```caddyfile
health_check GET /v2/ 401
```

Accepts only status `401`.

`STATUS` must be one integer from `100` through `599`. No ranges. No lists.

### Detector JSON contract

Use health fields only:

```json
{
  "health_method": "GET",
  "health_path": "/v2/",
  "health_status": 401
}
```

`health_status` is optional. Missing or zero means accept `2xx`/`3xx`.

Remove old detector fields from code and tests:

```json
readiness_method
readiness_path
```

### discover-app `.env` contract

Use `REVERSE_BIN_`-prefixed health variables only:

```sh
REVERSE_BIN_HEALTH_METHOD=GET
REVERSE_BIN_HEALTH_PATH=/v2/
REVERSE_BIN_HEALTH_STATUS=401
```

Rules:

- `REVERSE_BIN_HEALTH_METHOD` and `REVERSE_BIN_HEALTH_PATH` must appear together.
- `REVERSE_BIN_HEALTH_STATUS` requires `REVERSE_BIN_HEALTH_METHOD` and `REVERSE_BIN_HEALTH_PATH`.
- `REVERSE_BIN_HEALTH_METHOD` is uppercased after trimming.
- `REVERSE_BIN_HEALTH_PATH` must be non-empty after trimming.
- `REVERSE_BIN_HEALTH_STATUS` must parse as integer `100..599`.

Remove old `.env` variables from code and tests:

```sh
READINESS_METHOD
READINESS_PATH
```

### Go internals

Rename readiness names to health names:

- `ReadinessMethod` -> `HealthMethod`
- `ReadinessPath` -> `HealthPath`
- add `HealthStatus int`
- `ReadinessTimeoutMS` -> `HealthTimeoutMS`
- `readinessTimeout()` -> `healthTimeout()`
- `readinessConfigured()` -> `healthConfigured()`
- `probeReady()` -> `probeHealth()`
- `waitReady()` -> `waitHealthy()`
- log/error strings mention `health_check`, not `readiness_check`

Health success rule:

```go
if cfg.HealthStatus != 0 {
    return resp.StatusCode == cfg.HealthStatus, nil
}
return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
```

---

## Implementation Checklist

### Task 1: Add Go unit coverage for health status parsing and probing

**Files:**
- Modify: `reverse-bin_test.go`

**Checklist:**

- [ ] Add test case: Caddyfile parses `health_check GET /v2/ 401` into `HealthMethod=GET`, `HealthPath=/v2/`, `HealthStatus=401`.
- [ ] Add test case: Caddyfile parses `health_check GET /health` with `HealthStatus=0`.
- [ ] Add test case: Caddyfile parses `health_timeout_ms 15000` into `HealthTimeoutMS=15000`.
- [ ] Add test case: `health_check GET /v2/ 99` returns parser error.
- [ ] Add test case: `health_check GET /v2/ 600` returns parser error.
- [ ] Add test for health probe accepting configured `401` from `httptest.Server`.
- [ ] Add test for health probe rejecting `200` when configured status is `401`.
- [ ] Run: `go test ./...`
- [ ] Expected: fail due missing health fields/parser/probe behavior.

### Task 2: Rename Go config and Caddyfile parser to health names

**Files:**
- Modify: `module.go`
- Modify: `reverse-bin.go`
- Modify: `reverse-bin_test.go`
- Modify: `cmd/caddy/debian_layout_test.go`

**Checklist:**

- [ ] Rename `ReverseBin` fields to `HealthMethod`, `HealthPath`, `HealthStatus`, `HealthTimeoutMS`.
- [ ] Update JSON tags to `health_method`, `health_path`, `health_status`, `healthTimeoutMs` or consistent existing camel JSON style for module config.
- [ ] Replace Caddyfile subdirective `readiness_check` with `health_check`.
- [ ] Parse optional single status argument for `health_check`.
- [ ] Validate status range `100..599`.
- [ ] Replace `readiness_timeout_ms` with `health_timeout_ms`.
- [ ] Update provision validation error text to say `health_check is required for non-unix reverse_proxy_to targets`.
- [ ] Rename helper functions and config structs in tests.
- [ ] Update Debian default Caddyfile expectations from readiness timeout env name to health timeout env name.
- [ ] Run: `go test ./...`
- [ ] Expected: pass Go unit tests or fail only integration/discover-app references still using old names.
- [ ] Commit: `refactor(health): rename readiness config`

### Task 3: Update health probe behavior for explicit status

**Files:**
- Modify: `reverse-bin.go`
- Modify: `reverse-bin_test.go`

**Checklist:**

- [ ] Add `HealthStatus` to `proxyOverrides` and `resolvedConfig`.
- [ ] Ensure detector override can set `health_status`.
- [ ] Update health probe success rule: configured status means exact match; missing status means `2xx/3xx`.
- [ ] Update timeout usage from readiness naming to health naming.
- [ ] Run: `go test ./...`
- [ ] Expected: Go unit tests pass.
- [ ] Commit: `feat(health): allow explicit health status`

### Task 4: Rename discover-app readiness envs to health envs

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`

**Checklist:**

- [ ] Rename `readiness_method`/`readiness_path` typed fields to `health_method`/`health_path`.
- [ ] Add `health_status: int | None` typed field.
- [ ] Read `.env` keys `REVERSE_BIN_HEALTH_METHOD`, `REVERSE_BIN_HEALTH_PATH`, `REVERSE_BIN_HEALTH_STATUS`.
- [ ] Remove handling for `READINESS_METHOD`, `READINESS_PATH`.
- [ ] Validate method/path pair.
- [ ] Validate status requires method/path.
- [ ] Validate status integer range `100..599`.
- [ ] Emit detector JSON fields `health_method`, `health_path`, `health_status` only.
- [ ] Update all Python tests and comments from readiness to health.
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`
- [ ] Expected: pass.
- [ ] Commit: `feat(discover-app): emit health check config`

### Task 5: Update integration tests and fixtures

**Files:**
- Modify: `cmd/caddy/integration_test.go`
- Possibly modify: test fixture scripts under `cmd/caddy/testdata/` if old strings exist.

**Checklist:**

- [ ] Rename `TestReadinessCheck` to `TestHealthCheck`.
- [ ] Replace Caddyfile snippets `readiness_check` with `health_check`.
- [ ] Replace `readiness_timeout_ms` with `health_timeout_ms`.
- [ ] Add integration case for TCP backend where `/v2/` returns `401` and `health_check GET /v2/ 401` succeeds.
- [ ] Ensure every HTTP request test comment states what request verifies.
- [ ] Keep assertions specific: response code, marker body, and probe metadata where applicable.
- [ ] No retry loops.
- [ ] Run: `go test ./cmd/caddy -run 'TestHealthCheck|TestDynamicDiscovery|TestReverseProxy' -count=1`
- [ ] Expected: pass.
- [ ] Commit: `test(health): cover explicit status checks`

### Task 6: Update docs and packaging names

**Files:**
- Modify: `README.md`
- Modify: `docs/plans/*` only if referenced by live docs or tests require it; do not rewrite old historical plans unless needed.
- Modify: `packaging/debian/Caddyfile`
- Modify: `packaging/debian/reverse-bin`
- Modify: `debian/*` generated/package files only if repo keeps them current.

**Checklist:**

- [ ] Replace documented `readiness_check` with `health_check`.
- [ ] Replace documented `readiness_timeout_ms` env/default names with health names.
- [ ] Document `health_check GET /v2/ 401` for auth-protected health endpoints.
- [ ] Run: `rg -n "readiness|READINESS" README.md docs utils cmd packaging debian *.go *.md`.
- [ ] Decide if any remaining `readiness` occurrences are historical plan text; otherwise remove/rename.
- [ ] Run: `go test ./...`.
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`.
- [ ] Commit: `docs(health): document health check config`

### Task 7: Use `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT` for TCP detector apps

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`

**Checklist:**

- [ ] Add failing test: blank `REVERSE_BIN_PORT=` allocates `reverse_proxy_to` and envs include exact `REVERSE_BIN_PORT=<allocated-port>`.
- [ ] Add failing test: fixed `REVERSE_BIN_PORT=9999` emits `reverse_proxy_to=127.0.0.1:9999` and preserves `REVERSE_BIN_PORT=9999`.
- [ ] Add failing test: `REVERSE_BIN_HOST=0.0.0.0` with `REVERSE_BIN_PORT=9999` emits `reverse_proxy_to=0.0.0.0:9999` and preserves both envs.
- [ ] Add failing test: missing `REVERSE_BIN_HOST` defaults to `127.0.0.1` and injects `REVERSE_BIN_HOST=127.0.0.1`.
- [ ] Add failing test: unix socket app does not get `REVERSE_BIN_HOST` or `REVERSE_BIN_PORT`.
- [ ] Remove legacy listener env as detector transport config and from child env injection.
- [ ] Implement TCP config by deriving `reverse_proxy_to` from `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`.
- [ ] Commit: `feat(discover-app): use reverse-bin tcp envs`

### Task 8: Adapt Wrangler registry launch script for reverse-bin envs

**Files:**
- Modify locally for smoke testing: `~/Downloads/serverless-registry/launch-env.sh`
- Modify locally for smoke testing: `~/Downloads/serverless-registry/launch.sh`
- Delete locally for smoke testing: `~/Downloads/serverless-registry/launch-w-landrun.sh`

**Checklist:**

- [ ] Update `launch-env.sh` so `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT` override local host/port defaults.
- [ ] Update `launch.sh` so Wrangler receives `--port "$PORT"` from `REVERSE_BIN_PORT` when reverse-bin launches it.
- [ ] Add `--ip "$HOST"` to Wrangler args if local Wrangler supports it; otherwise require `REVERSE_BIN_HOST=127.0.0.1` for smoke testing.
- [ ] Ensure `launch.sh --prepare` still works when `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT` are unset.
- [ ] Delete `launch-w-landrun.sh`; compatibility path must not depend on the sandbox wrapper.
- [ ] Run: `~/Downloads/serverless-registry/launch.sh --prepare`.
- [ ] Commit repo changes only; do not commit files under `~/Downloads/serverless-registry` unless that repo is intentionally being modified separately.

### Task 9: Add manual reverse-bin app runner

**Files:**
- Create: `utils/run-reverse-bin-app.sh`
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`

**Checklist:**

- [ ] Create `utils/run-reverse-bin-app.sh` with usage `utils/run-reverse-bin-app.sh APP_DIR [HTTP_PORT]`.
- [ ] Resolve repo root from script location, and resolve `APP_DIR` to an absolute path.
- [ ] Generate a temporary Caddyfile using the shape from "Manual reverse-bin launch process".
- [ ] Use `dynamic_proxy_detector "$REPO_ROOT/utils/discover-app/discover-app.py" --no-sandbox "$APP_DIR"`.
- [ ] Run `go run ./cmd/caddy run --adapter caddyfile --config "$TEMP_CADDYFILE"` from repo root.
- [ ] Trap shell exit and remove the temporary Caddyfile.
- [ ] Add usage docs with curl smoke command.
- [ ] Update `CONTRIBUTING.md` with the generic new-app compatibility workflow: create `.env`, run `utils/run-reverse-bin-app.sh APP_DIR PORT`, curl expected route/status, then add docs/examples once working.
- [ ] Run: `bash -n utils/run-reverse-bin-app.sh`.
- [ ] Run: `utils/run-reverse-bin-app.sh examples/reverse-proxy/apps/python3-echo 9080`, then in another shell run `curl -i http://127.0.0.1:9080/` and verify HTTP `200` from the example app.
- [ ] Commit: `feat(dev): add reverse-bin app runner`

### Task 10: Document explicit launch-script compatibility

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Possibly create example under `examples/` if project has suitable app examples.

**Checklist:**

- [ ] Document generic `.env` launch-script pattern with `REVERSE_BIN_COMMAND`, `REVERSE_BIN_HOST`, `REVERSE_BIN_PORT`, and `REVERSE_BIN_HEALTH_*` fields.
- [ ] Document that blank `REVERSE_BIN_PORT=` asks detector to allocate a TCP port.
- [ ] Document that missing `REVERSE_BIN_HOST` defaults to `127.0.0.1`.
- [ ] Document that app launch scripts should bind to `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
- [ ] Document Wrangler as an example of explicit launch-script config, not auto-detection.
- [ ] Document that no separate sandbox wrapper script is needed or supported by this compatibility workflow.
- [ ] Link to `utils/run-reverse-bin-app.sh` as manual end-to-end test process.
- [ ] In `CONTRIBUTING.md`, document this as the preferred generic process for adding compatibility with new app runtimes before adding detector-specific code.
- [ ] Run: `rg -n "wrangler|health_check|REVERSE_BIN_HEALTH|REVERSE_BIN_PORT|REVERSE_BIN_HOST|run-reverse-bin-app|new app" README.md CONTRIBUTING.md docs utils`.
- [ ] Commit: `docs(discover-app): document tcp launch envs`

---

## Final Verification

- [ ] Run: `go test ./...`
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`
- [ ] Run: `rg -n "readiness_check|readiness_timeout_ms|readiness_method|readiness_path|READINESS_METHOD|READINESS_PATH" --glob '!docs/plans/2026-04-26-wrangler-compat.md'`
- [ ] Confirm only acceptable historical references remain, or none.
- [ ] Review diff for naming consistency and no compatibility aliases.
- [ ] Run detector against copied/sample Wrangler app with explicit `.env` and confirm JSON includes launch command, TCP target derived from `REVERSE_BIN_HOST`/`REVERSE_BIN_PORT`, health fields derived from `REVERSE_BIN_HEALTH_*`, and `health_status=401`.
- [ ] Run `bash -n utils/run-reverse-bin-app.sh`.
- [ ] Confirm `~/Downloads/serverless-registry/launch-w-landrun.sh` is absent from the smoke workflow.
- [ ] Run `utils/run-reverse-bin-app.sh ~/Downloads/serverless-registry 9080`, then `curl -i http://127.0.0.1:9080/v2/`, and confirm HTTP `401` from registry app.
