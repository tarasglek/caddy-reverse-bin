# Test Suite Refactoring Plan

The goal is to replace the legacy CGI-based tests with modern tests that validate the process-managing reverse proxy functionality and the dynamic proxy detector.

## 1. Cleanup
- Remove `_TestReverseBin_ServeHTTPPost` from `reverse-bin_test.go` as it relies on non-existent fields and outdated CGI logic.
- Remove `passAll()` from `reverse-bin.go` as it is unused.

## 2. Unit Tests (`reverse-bin_test.go`)
- **Caddyfile Parsing:** Update `TestReverseBin_UnmarshalCaddyfile` to include cases for:
    - `reverse_proxy_to`
    - `readiness_check`
    - `dynamic_proxy_detector`
- **Upstream Selection:** Test `GetUpstreams` logic, ensuring it correctly parses various `reverse_proxy_to` formats (IP, port-only, unix sockets).

## 3. Integration Tests (`module_test.go`)
- **Basic Reverse Proxy:**
    - Use a simple python or go "echo" server as the backend.
    - Verify that `reverse-bin` starts the process on the first request.
    - Verify that the request is successfully proxied and receives a response.
- **Dynamic Discovery:**
    - Create a mock "detector" script that returns JSON overrides.
    - Verify that `reverse-bin` executes the detector and uses the returned `reverse_proxy_to` and `executable`.
- **Lifecycle Management:**
    - Verify that the process is terminated after the idle timeout (shortened for testing).
    - Verify that the process is cleaned up when Caddy stops.

## 4. Test Helpers
- Utilize `examples/reverse-proxy/apps/` for real-world backend testing where possible.
- Use `caddytest` for full end-to-end Caddyfile integration.
