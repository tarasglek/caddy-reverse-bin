# Health Timeout Diagnostics Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make health-check timeout logs say last probe result + docs link.

**Architecture:** Track last health probe in `waitHealthy()`. On timeout, log one short actionable line. Add stable `README.md#health-checks` docs anchor.

**Tech Stack:** Go stdlib, zap logger, existing Go tests.

---

## Desired log

Short. No essay.

```text
health timeout: last HEAD / -> 500, want 2xx/3xx; configure health_check or see https://github.com/tarasglek/caddy-reverse-bin#health-checks
```

Structured fields too:

```json
{
  "msg": "health timeout",
  "method": "HEAD",
  "path": "/",
  "last_status": 500,
  "want": "2xx/3xx",
  "docs": "https://github.com/tarasglek/caddy-reverse-bin#health-checks"
}
```

If last probe error:

```text
health timeout: last HEAD / error: context deadline exceeded, want 2xx/3xx; configure health_check or see https://github.com/tarasglek/caddy-reverse-bin#health-checks
```

---

## Task 1: Add docs anchor

**Files:**
- Modify: `README.md`

**Steps:**
- Add `## Health checks` section.
- Mention:
  - default accepts `2xx/3xx`
  - explicit status works: `health_check GET /v2/ 401`
  - auth apps should use public `/health` or configure expected redirect/status
  - reverse-bin-hosting env vars:
    ```sh
    REVERSE_BIN_HEALTH_METHOD=GET
    REVERSE_BIN_HEALTH_PATH=/
    REVERSE_BIN_HEALTH_STATUS=302
    ```
- Keep short.

**Verify:**
```bash
grep -n "## Health checks" README.md
```

---

## Task 2: Add last-probe model

**Files:**
- Modify: `reverse-bin.go`

**Steps:**
- Add const:
  ```go
  const healthCheckDocsURL = "https://github.com/tarasglek/caddy-reverse-bin#health-checks"
  ```
- Add tiny struct:
  ```go
  type healthProbeResult struct {
      method string
      path   string
      status int
      err    error
      want   string
  }
  ```
- Add helper:
  ```go
  func healthWant(status int) string {
      if status != 0 {
          return strconv.Itoa(status)
      }
      return "2xx/3xx"
  }
  ```
- Need `strconv` if not imported.

**Verify:**
```bash
go test ./... -run TestNonExistent
```
Expected: compile pass, no tests run.

---

## Task 3: Make probe return status detail

**Files:**
- Modify: `reverse-bin.go`

**Steps:**
- Keep `probeHealth()` API if simpler, or add new helper that returns `(bool, healthProbeResult)`.
- Preferred: change to:
  ```go
  func (c *ReverseBin) probeHealth(ctx context.Context, cfg resolvedConfig, sourceReq *http.Request) (bool, healthProbeResult)
  ```
- Populate:
  - `method: cfg.HealthMethod`
  - `path: cfg.HealthPath`
  - `want: healthWant(cfg.HealthStatus)`
  - `status: resp.StatusCode` when response exists
  - `err: err` when request fails
- For config/build errors, put `err` in result.

**Verify:**
```bash
go test ./... -run 'TestProbeHealth|TestWaitHealthy'
```
Expected: update compile errors until tests pass.

---

## Task 4: Log once on timeout

**Files:**
- Modify: `reverse-bin.go`

**Steps:**
- In `waitHealthy()`, keep:
  ```go
  var last healthProbeResult
  ```
- Each failed probe updates `last`.
- When context done / timeout path happens, log:
  ```go
  c.logger.Warn("health timeout",
      zap.String("method", last.method),
      zap.String("path", last.path),
      zap.Int("last_status", last.status),
      zap.String("want", last.want),
      zap.String("docs", healthCheckDocsURL),
      zap.Error(last.err),
  )
  ```
- If no probe ran, fields can be empty/zero. OK.
- Do not log every failed probe at info. No spam.
- Existing returned error can stay `context deadline exceeded`.

**Verify:**
```bash
go test ./... -run TestWaitHealthyStopsOnContextCancel
```
Expected: PASS.

---

## Task 5: Add/adjust tests

**Files:**
- Modify: `reverse-bin_test.go`

**Steps:**
- Add test for `healthWant()`:
  - `0 -> "2xx/3xx"`
  - `401 -> "401"`
- Add test for `probeHealth()` detail if easy:
  - server returns `500`
  - want `2xx/3xx`
  - result status `500`
  - ok false
- If logger assertion hard, do not overbuild.

**Verify:**
```bash
go test ./... -run 'TestHealthWant|TestProbeHealth'
```
Expected: PASS.

---

## Task 6: Full verify

**Files:**
- All touched files.

**Steps:**
```bash
gofmt -w reverse-bin.go reverse-bin_test.go
go test ./...
grep -n "healthCheckDocsURL" reverse-bin.go
grep -n "## Health checks" README.md
```

Expected:
- gofmt clean
- tests pass
- docs URL const exists
- README anchor exists

---

## Non-goals

- No changing health semantics.
- No making `500` healthy by default.
- No probe spam logs.
- No reverse-bin-hosting changes.
- No env secret redaction here; separate plan handles that.
