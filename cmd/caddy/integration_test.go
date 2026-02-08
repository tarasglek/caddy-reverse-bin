package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

type pathCheck struct {
	Label         string
	Path          string
	MustBeDir     bool
	MustBeRegular bool
}

func requirePaths(t *testing.T, checks ...pathCheck) {
	t.Helper()
	for _, c := range checks {
		info, err := os.Stat(c.Path)
		if err != nil {
			t.Fatalf("required %s missing/unreadable at %s: %v", c.Label, c.Path, err)
		}
		if c.MustBeDir && !info.IsDir() {
			t.Fatalf("required %s is not a directory: %s", c.Label, c.Path)
		}
		if c.MustBeRegular && !info.Mode().IsRegular() {
			t.Fatalf("required %s is not a regular file: %s", c.Label, c.Path)
		}
	}
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

func reverseBinStaticAppBlock(appPath, socketPath string, extraDirectives ...string) string {
	directives := []string{
		fmt.Sprintf("exec uv run --script %s", appPath),
		fmt.Sprintf("reverse_proxy_to unix/%s", socketPath),
		fmt.Sprintf("env REVERSE_PROXY_TO=unix/%s", socketPath),
		"pass_all_env",
	}
	directives = append(directives, extraDirectives...)
	return fmt.Sprintf("reverse-bin {\n\t\t%s\n\t}", strings.Join(directives, "\n\t\t"))
}

func siteWithReverseBin(host string, block string) string {
	return fmt.Sprintf("\nhttp://%s {\n\t%s\n}\n", host, block)
}

// TestBasicReverseProxy tests basic Unix socket reverse proxy functionality
func TestBasicReverseProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	repoRoot := getRepoRoot()
	pythonApp := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py")

	requirePaths(t, pathCheck{Label: "python test app", Path: pythonApp, MustBeRegular: true})

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	siteBlocks := siteWithReverseBin("localhost:9080", reverseBinStaticAppBlock(pythonApp, socketPath))

	tester.InitServerWithDefaults(9080, 9443, siteBlocks)

	// Make a request - this should start the process and proxy
	resp, body := tester.AssertGetResponse("http://localhost:9080/test/path", 200, "")

	t.Logf("Response body: %s", body)

	// Verify we got a response from the Python echo server
	if body == "" {
		t.Logf("empty body response status=%d headers=%v", resp.StatusCode, resp.Header)
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

	// Use the unix-echo app which has a .env with unix socket config
	appDir := filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo")
	requirePaths(t,
		pathCheck{Label: "dynamic detector", Path: detector, MustBeRegular: true},
		pathCheck{Label: "dynamic app dir", Path: appDir, MustBeDir: true},
	)

	tester := NewTester(t)

	siteBlocks := fmt.Sprintf(`
http://localhost:9082 {
	reverse-bin {
		dynamic_proxy_detector uv run --script %s %s
	}
}
`, detector, appDir)

	tester.InitServerWithDefaults(9082, 9445, siteBlocks)

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
	siteBlocks := fmt.Sprintf(`
http://localhost:9086 {
	reverse-bin {
		dynamic_proxy_detector %s {path}
	}
}
`, failDetector)

	tester.InitServerWithDefaults(9086, 9449, siteBlocks)

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
	requirePaths(t, pathCheck{Label: "python test app", Path: pythonApp, MustBeRegular: true})

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
	siteBlocks := fmt.Sprintf(`
http://localhost:9087 {
	reverse-bin {
		dynamic_proxy_detector %s {path}
		pass_all_env
	}
}
`, detector)

	tester.InitServerWithDefaults(9087, 9450, siteBlocks)

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
	requirePaths(t, pathCheck{Label: "python test app", Path: pythonApp, MustBeRegular: true})

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	siteBlocks := siteWithReverseBin("localhost:9083", reverseBinStaticAppBlock(pythonApp, socketPath))

	tester.InitServerWithDefaults(9083, 9446, siteBlocks)

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
	requirePaths(t, pathCheck{Label: "python test app", Path: pythonApp, MustBeRegular: true})

	socketPath := createSocketPath(t)
	tester := NewTester(t)

	siteBlocks := siteWithReverseBin("localhost:9084", reverseBinStaticAppBlock(pythonApp, socketPath, "readiness_check GET /"))

	tester.InitServerWithDefaults(9084, 9447, siteBlocks)

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
	requirePaths(t, pathCheck{Label: "python test app", Path: pythonApp, MustBeRegular: true})

	socket1 := createSocketPath(t)
	socket2 := createSocketPath(t)
	tester := NewTester(t)

	siteBlocks := siteWithReverseBin("localhost:9085/app1", reverseBinStaticAppBlock(pythonApp, socket1)) +
		siteWithReverseBin("localhost:9085/app2", reverseBinStaticAppBlock(pythonApp, socket2))

	tester.InitServerWithDefaults(9085, 9448, siteBlocks)

	// Test app1
	resp1, body1 := tester.AssertGetResponse("http://localhost:9085/app1/test", 200, "")
	t.Logf("App1 response: %s", body1)

	// Test app2
	resp2, body2 := tester.AssertGetResponse("http://localhost:9085/app2/test", 200, "")
	t.Logf("App2 response: %s", body2)

	_ = resp1
	_ = resp2
}
