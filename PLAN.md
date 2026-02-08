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
- **Process Key Generation:** Verify `getProcessKey` correctly handles replacers for dynamic discovery.

## 3. Integration Tests (`module_test.go`)
- **Basic Reverse Proxy:**
    - Use `examples/reverse-proxy/apps/python3-echo/main.py` as the backend.
    - Verify that `reverse-bin` starts the process on the first request.
    - Verify that the request is successfully proxied and receives a response.
- **Unix Socket Proxy:**
    - Use `examples/reverse-proxy/apps/python3-unix-echo/main.py`.
    - Verify `reverse_proxy_to unix/...` works correctly.
- **Dynamic Discovery:**
    - Use `utils/discover-app/discover-app.py` as the detector.
    - Verify that `reverse-bin` executes the detector and uses the returned JSON to configure the backend.
- **Lifecycle Management:**
    - Verify that the process is terminated after the idle timeout (shortened for testing).
    - Verify that the process is cleaned up when Caddy stops.

## 4. Test Helpers
- Utilize `examples/reverse-proxy/apps/` for real-world backend testing where possible.
- Use `caddytest` for full end-to-end Caddyfile integration.
