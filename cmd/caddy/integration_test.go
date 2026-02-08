package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// getRepoRoot returns the repository root directory
func getRepoRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to determine current file path")
	}
	// We're in cmd/caddy/, repo root is ../../
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// createSocketPath creates a unique temp socket path
func createSocketPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets not supported on Windows")
	}
	f, err := os.CreateTemp("", "reverse-bin-*.sock")
	if err != nil {
		t.Fatalf("failed to create temp file for socket path: %s", err)
	}
	socketPath := f.Name()
	f.Close()
	os.Remove(socketPath)
	t.Cleanup(func() {
		os.Remove(socketPath)
	})
	return socketPath
}

func createExecutableScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write script %s: %v", path, err)
	}
	return path
}

func assertStatus5xx(t *testing.T, tester *Tester, rawURL string) string {
	t.Helper()
	resp, err := tester.Client.Get(rawURL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading response body: %v", err)
	}
	body := string(bodyBytes)

	if resp.StatusCode < 500 || resp.StatusCode > 599 {
		t.Fatalf("expected 5xx for %s, got %d (body: %s)", rawURL, resp.StatusCode, body)
	}
	return body
}

// TestBasicReverseProxy tests basic Unix socket reverse proxy functionality
func TestBasicReverseProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")

	// Verify the test app exists
	if _, err := os.Stat(pythonApp); os.IsNotExist(err) {
		t.Skipf("test app not found: %s", pythonApp)
	}

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	// Caddyfile config with reverse-bin handler using Unix socket
	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9080
	https_port 9443
	grace_period 1ns
}

http://localhost:9080 {
	reverse-bin {
		exec uv run --script %s
		reverse_proxy_to unix/%s
		env REVERSE_PROXY_TO=unix/%s
		pass_all_env
	}
}
`, pythonApp, socketPath, socketPath)

	tester.InitServer(config, "caddyfile")

	// Give the server a moment to be ready
	time.Sleep(100 * time.Millisecond)

	// Make a request - this should start the process and proxy
	resp, body := tester.AssertGetResponse("http://localhost:9080/test/path", 200, "")

	t.Logf("Response body: %s", body)

	// Verify we got a response from the Python echo server
	if body == "" {
		t.Error("expected non-empty response body")
	}

	_ = resp
}

// TestDynamicDiscovery tests dynamic proxy detector functionality
func TestDynamicDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	detector := filepath.Join(repoRoot, "utils/discover-app/discover-app.py")

	// Verify detector exists
	if _, err := os.Stat(detector); os.IsNotExist(err) {
		t.Skipf("detector not found: %s", detector)
	}

	// Use the unix-echo app which has a .env with unix socket config
	appDir := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo")

	tester := NewTester(t)

	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9082
	https_port 9445
	grace_period 1ns
}

http://localhost:9082 {
	reverse-bin {
		dynamic_proxy_detector uv run --script %s %s
	}
}
`, detector, appDir)

	tester.InitServer(config, "caddyfile")

	// Give the server a moment
	time.Sleep(100 * time.Millisecond)

	// Make a request
	resp, body := tester.AssertGetResponse("http://localhost:9082/dynamic/test", 200, "")

	t.Logf("Response body: %s", body)

	if body == "" {
		t.Error("expected non-empty response body from dynamically discovered app")
	}

	_ = resp
}

func TestDynamicDiscovery_DetectorFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	failDetector := createExecutableScript(t, tmpDir, "detector-fail.py", `#!/usr/bin/env python3
import sys
print("detector failed on purpose", file=sys.stderr)
sys.exit(2)
`)

	tester := NewTester(t)
	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9086
	https_port 9449
	grace_period 1ns
}

http://localhost:9086 {
	reverse-bin {
		dynamic_proxy_detector %s {path}
	}
}
`, failDetector)

	tester.InitServer(config, "caddyfile")
	time.Sleep(100 * time.Millisecond)

	body := assertStatus5xx(t, tester, "http://localhost:9086/fail")
	if !strings.Contains(body, "dynamic proxy detector failed") {
		t.Logf("expected detector failure text, got: %s", body)
	}
}

func TestDynamicDiscovery_FirstRequestOK_SecondPathFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")
	if _, err := os.Stat(pythonApp); os.IsNotExist(err) {
		t.Skipf("test app not found: %s", pythonApp)
	}

	socketPath := createSocketPath(t)
	tmpDir := t.TempDir()
	detector := createExecutableScript(t, tmpDir, "detector-switch.py", fmt.Sprintf(`#!/usr/bin/env python3
import json
import sys

path = sys.argv[1] if len(sys.argv) > 1 else ""
if path == "/ok":
    print(json.dumps({
        "executable": ["uv", "run", "--script", %q],
        "reverse_proxy_to": %q,
        "envs": [%q],
    }))
    sys.exit(0)

print("intentional detector failure for path=" + path, file=sys.stderr)
sys.exit(3)
`, pythonApp, "unix/"+socketPath, "REVERSE_PROXY_TO=unix/"+socketPath))

	tester := NewTester(t)
	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9087
	https_port 9450
	grace_period 1ns
}

http://localhost:9087 {
	reverse-bin {
		dynamic_proxy_detector %s {path}
		pass_all_env
	}
}
`, detector)

	tester.InitServer(config, "caddyfile")
	time.Sleep(100 * time.Millisecond)

	_, body1 := tester.AssertGetResponse("http://localhost:9087/ok", 200, "")
	if body1 == "" {
		t.Fatal("expected non-empty response for /ok")
	}

	_ = assertStatus5xx(t, tester, "http://localhost:9087/bad")

	_, body3 := tester.AssertGetResponse("http://localhost:9087/ok", 200, "")
	if body3 == "" {
		t.Fatal("expected non-empty response for second /ok")
	}
}

// TestLifecycleIdleTimeout tests that processes are cleaned up after idle timeout
func TestLifecycleIdleTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")

	if _, err := os.Stat(pythonApp); os.IsNotExist(err) {
		t.Skipf("test app not found: %s", pythonApp)
	}

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9083
	https_port 9446
	grace_period 1ns
}

http://localhost:9083 {
	reverse-bin {
		exec uv run --script %s
		reverse_proxy_to unix/%s
		env REVERSE_PROXY_TO=unix/%s
		pass_all_env
	}
}
`, pythonApp, socketPath, socketPath)

	tester.InitServer(config, "caddyfile")

	// First request should start the process
	resp1, body1 := tester.AssertGetResponse("http://localhost:9083/first", 200, "")
	t.Logf("First response: %s", body1)

	// Second request should reuse the running process
	resp2, body2 := tester.AssertGetResponse("http://localhost:9083/second", 200, "")
	t.Logf("Second response: %s", body2)

	_ = resp1
	_ = resp2

	// Note: Testing actual idle timeout cleanup would require:
	// 1. Adding idle_timeout config option to reverse-bin
	// 2. Waiting for the timeout period
	// 3. Verifying the process is terminated
	// This is left as a future enhancement
}

// TestReadinessCheck tests that reverse-bin waits for process readiness
func TestReadinessCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")

	if _, err := os.Stat(pythonApp); os.IsNotExist(err) {
		t.Skipf("test app not found: %s", pythonApp)
	}

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9084
	https_port 9447
	grace_period 1ns
}

http://localhost:9084 {
	reverse-bin {
		exec uv run --script %s
		reverse_proxy_to unix/%s
		env REVERSE_PROXY_TO=unix/%s
		pass_all_env
		readiness_check GET /
	}
}
`, pythonApp, socketPath, socketPath)

	tester.InitServer(config, "caddyfile")

	// The request should succeed after readiness check passes
	resp, body := tester.AssertGetResponse("http://localhost:9084/ready", 200, "")
	t.Logf("Response after readiness: %s", body)

	_ = resp
}

// TestMultipleApps tests multiple reverse-bin instances with different Unix sockets
func TestMultipleApps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")

	if _, err := os.Stat(pythonApp); os.IsNotExist(err) {
		t.Skipf("test app not found: %s", pythonApp)
	}

	socket1 := createSocketPath(t)
	socket2 := createSocketPath(t)
	tester := NewTester(t)

	config := fmt.Sprintf(`
{
	skip_install_trust
	admin localhost:2999
	http_port 9085
	https_port 9448
	grace_period 1ns
}

http://localhost:9085/app1 {
	reverse-bin {
		exec uv run --script %s
		reverse_proxy_to unix/%s
		env REVERSE_PROXY_TO=unix/%s
		pass_all_env
	}
}

http://localhost:9085/app2 {
	reverse-bin {
		exec uv run --script %s
		reverse_proxy_to unix/%s
		env REVERSE_PROXY_TO=unix/%s
		pass_all_env
	}
}
`, pythonApp, socket1, socket1, pythonApp, socket2, socket2)

	tester.InitServer(config, "caddyfile")

	time.Sleep(100 * time.Millisecond)

	// Test app1
	resp1, body1 := tester.AssertGetResponse("http://localhost:9085/app1/test", 200, "")
	t.Logf("App1 response: %s", body1)

	// Test app2
	resp2, body2 := tester.AssertGetResponse("http://localhost:9085/app2/test", 200, "")
	t.Logf("App2 response: %s", body2)

	_ = resp1
	_ = resp2
}
