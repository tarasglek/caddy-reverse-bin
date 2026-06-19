# Go Echo Examples Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace complex Python/Deno example apps with a small compiled Go echo helper that demonstrates and tests the Caddy plugin contract.

**Architecture:** Add one Go helper app under `examples/reverse-proxy/apps/go-echo` that supports TCP and Unix socket modes via environment variables. Update integration tests to compile that helper once per test run and use it for explicit Unix socket, dynamic detector stdout, health check, lifecycle, and multiple-app coverage. Keep fancy detection and hosting policy out of this repo.

**Tech Stack:** Go, Caddy integration tests, GitHub Actions-compatible local compilation.

---

### Task 1: Add failing test expectation for Go echo fixture

**Files:**
- Modify: `cmd/caddy/integration_test.go`

**Steps:**
1. Change fixture lookup to require a compiled Go echo helper path instead of Python example paths.
2. Run a focused existing integration test and verify it fails because `examples/reverse-proxy/apps/go-echo/main.go` does not exist.

### Task 2: Add Go echo helper

**Files:**
- Create: `examples/reverse-proxy/apps/go-echo/main.go`

**Steps:**
1. Implement TCP mode from `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`.
2. Implement Unix mode from `SOCKET_PATH`.
3. Add `/health`, `/health-last`, `/crash`, `/pid`, and default echo endpoints with stable JSON/text markers.
4. Re-run focused tests and verify they pass.

### Task 3: Replace old examples

**Files:**
- Delete old Python/Deno example app files under `examples/reverse-proxy/apps/`.
- Create: `examples/reverse-proxy/apps/static-site/index.html`.

**Steps:**
1. Remove Python and Deno apps from this plugin repo.
2. Add static HTML proof-of-concept example.
3. Update README/example docs if they mention old examples.

### Task 4: Verify and commit

**Steps:**
1. Run `go test ./cmd/caddy -run 'TestBasicReverseProxy|TestDynamicDiscovery|TestHealthCheck|TestProcessCrashAndRestart|TestMultipleApps' -count=1`.
2. Run `go test ./...`.
3. Commit with `test: use go echo app for plugin integration`.
