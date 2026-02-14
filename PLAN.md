# Plan: Migrate `cmd/caddy/integration_test.go` to static Caddy configs + Unix socket listener

I reviewed `cmd/caddy/integration_test.go` before writing this plan.

## Current state (from code)
- Integration tests currently build Caddyfile snippets inline via helpers:
  - `reverseBinStaticAppBlock(...)`
  - `reverseBinDynamicDetectorBlock(...)`
  - `siteWithReverseBin(...)`
- Caddy itself is started with TCP ports using:
  - `startTestServer(t, httpPort, httpsPort, siteBlocks)`
  - `tester.InitServerWithDefaults(httpPort, httpsPort, siteBlocks)`
- `reverse-bin` backend already uses Unix sockets in tests:
  - `reverse_proxy_to unix/<socketPath>`
  - env `REVERSE_PROXY_TO=unix/<socketPath>`
- Hosts/URLs are TCP (`http://localhost:908x/...`).

So only backend socket policy is currently satisfied; Caddy listener is still TCP.

---

## Target state
1. Use **static checked-in Caddyfile fixtures** for integration scenarios.
2. Make **Caddy listener Unix-socket based** (not `localhost:908x`).
3. Keep reverse-bin backend on Unix sockets.

---

## Concrete implementation plan

### 1) Add static fixture files
Create `cmd/caddy/testdata/integration/caddyfiles/` with fixtures such as:
- `basic_static.caddy`
- `dynamic_discovery.caddy`
- `detector_failure.caddy`
- `readiness.caddy`
- `multiple_apps.caddy`

Use placeholders:
- `{{CADDY_LISTEN}}`
- `{{APP_SOCKET}}`, `{{APP1_SOCKET}}`, `{{APP2_SOCKET}}`
- `{{PYTHON_APP}}`, `{{APP_DIR}}`, `{{DETECTOR}}`, `{{FAIL_DETECTOR}}`

This removes inline Caddyfile assembly from test code.

### 2) Add fixture render helper
In `cmd/caddy/integration_test.go` (or a `_test` helper file):
- `readFixture(t, name string) string`
- `renderFixture(t, tpl string, vars map[string]string) string`

Use `os.ReadFile` + `strings.NewReplacer`.

### 3) Create Unix socket for Caddy listener per test
Add helper similar to `createSocketPath` (already exists and can be reused):
- allocate path in temp area
- remove pre-existing file
- cleanup on test end

Set fixture `{{CADDY_LISTEN}}` to `unix/{{CADDY_SOCKET_PATH}}`.

### 4) Start Caddy with rendered static config
Replace calls that pass inline site blocks with rendered fixture text:
- keep using `startTestServer(...)` if required by harness
- pass dummy/unused HTTP/HTTPS ports if API requires ints
- fixture controls actual server listen address (Unix)

If `InitServerWithDefaults` forces TCP and ignores fixture listen, add a Unix-specific starter in test harness (e.g. `InitServerWithCaddyfile(...)`) and use that in integration tests.

### 5) Add HTTP-over-unix client helper
Current assertions call `tester.Client.Get("http://localhost:908x/..." )`.
Add helper client/request path for Unix listener:
- custom `http.Transport{ DialContext: ... net.Dial("unix", caddySock) }`
- requests to `http://unix/<path>`
- set `Host` if routing requires it

Then update:
- `assertNonEmpty200` -> Unix-aware variant
- `assertStatus5xx` -> Unix-aware variant

### 6) Migrate each test case to fixtures
Map existing tests:
- `TestBasicReverseProxy` -> `basic_static.caddy`
- `TestDynamicDiscovery` -> `dynamic_discovery.caddy`
- `TestDynamicDiscovery_DetectorFailure` -> `detector_failure.caddy`
- `TestDynamicDiscovery_FirstRequestOK_SecondPathFails` -> dedicated fixture with temp detector path
- `TestReadinessCheck` -> `readiness.caddy`
- `TestMultipleApps` -> `multiple_apps.caddy`
- `TestLifecycleIdleTimeout` -> base static fixture

### 7) Keep backend Unix-socket behavior intact
Do not change existing backend socket approach:
- `reverse_proxy_to unix/<socket>`
- env `REVERSE_PROXY_TO=unix/<socket>`

This already matches policy.

### 8) Run integration tests in tmux
Per repo policy, run test command in tmux.

---

## Acceptance criteria
- No integration test uses `http://localhost:908x` for Caddy listener traffic.
- Caddy listener address is Unix socket in fixtures.
- Fixtures are checked-in and used instead of inline Caddyfile builders.
- Existing test coverage/scenarios remain equivalent.
- Integration suite passes reliably without TCP port conflicts.

---

## Minimal first PR slice
1. Introduce fixture infrastructure + one fixture (`basic_static.caddy`).
2. Convert `TestBasicReverseProxy` to Unix-listener + static fixture.
3. Add Unix HTTP client helper used by assertions.
4. Convert remaining tests in follow-up commits.
