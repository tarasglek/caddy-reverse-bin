# Wrangler Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make reverse-bin compatible with Wrangler registry apps by supporting auth-protected health checks first, then wiring detector-friendly Wrangler launch configuration.

**Architecture:** Start with the blocking runtime capability: a breaking rename from readiness names to health names plus one optional exact health status, so `/v2/` returning `401` can mark auth-protected Wrangler apps healthy. Then update discover-app health config and add Wrangler launch-script detection/config support around `launch.sh`, `launch-w-landrun.sh`, `wrangler.toml`, dynamic `PORT`, and writable Wrangler state.

**Tech Stack:** Go Caddy module, Caddyfile parser, Python discover-app detector, Go integration tests, Python unittest, Wrangler/Miniflare launch scripts.

---

## Design

### Wrangler compatibility target

Target app shape from `~/Downloads/serverless-registry/`:

- `wrangler.toml` describes Worker app.
- `package.json` has local Wrangler dependency.
- `launch.sh` prepares state dirs and runs `wrangler dev`.
- `launch-w-landrun.sh` prepares outside sandbox, then runs `launch.sh` under landrun.
- app listens on `PORT`, not `LISTEN`.
- registry `/v2/` can return `401` and still be alive.

Desired detector `.env` shape after health support:

```sh
REVERSE_BIN_COMMAND=./launch-w-landrun.sh
LISTEN=
HEALTH_METHOD=GET
HEALTH_PATH=/v2/
HEALTH_STATUS=401
```

Detector must inject both `LISTEN=127.0.0.1:<port>` and `PORT=<port>` for TCP apps. Wrangler-specific auto-detection should prefer app-owned launch scripts over duplicating Wrangler command construction.

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

Use health variables only:

```sh
HEALTH_METHOD=GET
HEALTH_PATH=/v2/
HEALTH_STATUS=401
```

Rules:

- `HEALTH_METHOD` and `HEALTH_PATH` must appear together.
- `HEALTH_STATUS` requires `HEALTH_METHOD` and `HEALTH_PATH`.
- `HEALTH_METHOD` is uppercased after trimming.
- `HEALTH_PATH` must be non-empty after trimming.
- `HEALTH_STATUS` must parse as integer `100..599`.

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
- [ ] Read `.env` keys `HEALTH_METHOD`, `HEALTH_PATH`, `HEALTH_STATUS`.
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

### Task 7: Inject `PORT` for TCP detector apps

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`

**Checklist:**

- [ ] Add failing test: blank `LISTEN=` allocates `reverse_proxy_to` and envs include exact `PORT=<allocated-port>`.
- [ ] Add failing test: fixed `LISTEN=9999` emits `PORT=9999` while preserving `LISTEN=9999` if user set that value.
- [ ] Add failing test: unix socket app does not get `PORT`.
- [ ] Implement minimal TCP port injection by deriving port from normalized `reverse_proxy_to`.
- [ ] Ensure explicit `.env PORT=...` wins only if it matches derived port, or choose error-on-conflict and document in test.
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`.
- [ ] Commit: `feat(discover-app): set port for tcp apps`

### Task 8: Add Wrangler launch-script detector support

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`

**Checklist:**

- [ ] Add failing test: app with `wrangler.toml` and executable `launch-w-landrun.sh` detects command `./launch-w-landrun.sh`.
- [ ] Add failing test: app with `wrangler.toml` and executable `launch.sh` detects command `./launch.sh` if landrun wrapper script is absent.
- [ ] Add failing test: Wrangler detection emits TCP `reverse_proxy_to`, `LISTEN`, and `PORT`.
- [ ] Add failing test: Wrangler detection emits health fields from `.env` when present.
- [ ] Implement detector kind `wrangler` with `supports_unix_socket=false`.
- [ ] Prefer `launch-w-landrun.sh` over `launch.sh` because app script owns writable state sandboxing.
- [ ] Do not duplicate raw `wrangler dev` command in detector.
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`.
- [ ] Commit: `feat(discover-app): detect wrangler launch scripts`

### Task 9: Document Wrangler compatibility

**Files:**
- Modify: `README.md`
- Possibly create example under `examples/` if project has suitable app examples.

**Checklist:**

- [ ] Document minimal Wrangler `.env` using `REVERSE_BIN_COMMAND=./launch-w-landrun.sh`, blank `LISTEN`, and `HEALTH_STATUS=401`.
- [ ] Document auto-detection prerequisites: `wrangler.toml` plus executable `launch-w-landrun.sh` or `launch.sh`.
- [ ] Document that launch scripts own install/prepare/writable state; detector only launches them.
- [ ] Document `--no-sandbox` recommendation if app-owned launch script already uses landrun.
- [ ] Run: `rg -n "wrangler|health_check|HEALTH_STATUS" README.md docs utils`.
- [ ] Commit: `docs(wrangler): document launch compatibility`

---

## Final Verification

- [ ] Run: `go test ./...`
- [ ] Run: `uv run python utils/discover-app/test_discover_app.py -v`
- [ ] Run: `rg -n "readiness_check|readiness_timeout_ms|readiness_method|readiness_path|READINESS_METHOD|READINESS_PATH" --glob '!docs/plans/2026-04-26-wrangler-compat.md'`
- [ ] Confirm only acceptable historical references remain, or none.
- [ ] Review diff for naming consistency and no compatibility aliases.
- [ ] Run detector against copied/sample Wrangler app and confirm JSON includes `./launch-w-landrun.sh`, TCP target, `PORT`, `HEALTH_*`-derived fields, and `health_status=401`.
