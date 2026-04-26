package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// getRepoRoot returns the repository root directory.
func getRepoRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("unable to determine current file path")
	}
	// We're in cmd/caddy/, repo root is ../../
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
}

// GetFreePort asks the kernel for a free open port that is ready to use.
func GetFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close()
			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return
}

// createSocketPath creates a unique temp socket path.
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
	_ = os.Remove(socketPath)
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
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

func requireCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("skipping integration test: required command %q not found in PATH", name)
	}
}

type fixtures struct {
	PythonApp       string
	AppDir          string
	PythonTCPAppDir string
	DenoAppDir      string
	DiscoverApp     string
}

func mustFixtures(t *testing.T) fixtures {
	t.Helper()
	repoRoot := getRepoRoot()
	f := fixtures{
		PythonApp:       filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py"),
		AppDir:          filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo"),
		PythonTCPAppDir: filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-echo"),
		DenoAppDir:      filepath.Join(repoRoot, "examples/reverse-proxy/apps/deno-echo"),
		DiscoverApp:     filepath.Join(repoRoot, "utils/discover-app/discover-app.py"),
	}
	requirePaths(t,
		pathCheck{Label: "python test app", Path: f.PythonApp, MustBeRegular: true},
		pathCheck{Label: "dynamic app dir", Path: f.AppDir, MustBeDir: true},
		pathCheck{Label: "python tcp app dir", Path: f.PythonTCPAppDir, MustBeDir: true},
		pathCheck{Label: "deno app dir", Path: f.DenoAppDir, MustBeDir: true},
		pathCheck{Label: "discover app script", Path: f.DiscoverApp, MustBeRegular: true},
	)
	return f
}

func renderTemplate(input string, values map[string]string) string {
	replacements := make([]string, 0, len(values)*2)
	for k, v := range values {
		replacements = append(replacements, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(replacements...).Replace(input)
}

func createTestingTransport() *http.Transport {
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Second}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		parts := strings.Split(addr, ":")
		destAddr := fmt.Sprintf("127.0.0.1:%s", parts[len(parts)-1])
		return dialer.DialContext(ctx, network, destAddr)
	}
	return &http.Transport{DialContext: dialContext}
}

func newTestHTTPClient() *http.Client {
	return &http.Client{
		Transport: createTestingTransport(),
		Timeout:   10 * time.Second,
	}
}

func assertGetResponse(t *testing.T, client *http.Client, requestURI string, expectedStatusCode int, expectedBodyContains string, invariant string) (*http.Response, string) {
	t.Helper()

	var (
		resp *http.Response
		err  error
	)
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err = client.Get(requestURI)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: failed to call server: %v", invariant, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: unable to read response body: %v", invariant, err)
	}
	body := string(bodyBytes)

	if resp.StatusCode != expectedStatusCode {
		t.Fatalf("%s: requesting %q expected status %d but got %d (body: %s)", invariant, requestURI, expectedStatusCode, resp.StatusCode, body)
	}
	if expectedBodyContains != "" && !strings.Contains(body, expectedBodyContains) {
		t.Fatalf("%s: requesting %q expected body to contain %q but got %q", invariant, requestURI, expectedBodyContains, body)
	}
	return resp, body
}

func ptr(s string) *string {
	return &s
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return syscall.Kill(pid, 0) == nil
}

type reverseProxySetup struct {
	Port int
}

func createReverseProxySetup(t *testing.T, handleBlock string, values map[string]string) (*reverseProxySetup, func()) {
	t.Helper()

	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	vars := map[string]string{}
	for k, v := range values {
		vars[k] = v
	}
	resolvedHandle := renderTemplate(handleBlock, vars)

	caddyfilePath := filepath.Join(t.TempDir(), "Caddyfile")
	fixture := `
{
	admin off
	http_port {{HTTP_PORT}}
}

http://localhost:{{HTTP_PORT}} {
	{{HANDLE_BLOCK}}
}
`
	rendered := renderTemplate(fixture, map[string]string{
		"HTTP_PORT":    fmt.Sprintf("%d", port),
		"HANDLE_BLOCK": resolvedHandle,
	})
	if err := os.WriteFile(caddyfilePath, []byte(rendered), 0o600); err != nil {
		t.Fatalf("failed to write temp Caddyfile: %v", err)
	}

	prevArgs := os.Args
	os.Args = []string{"caddy", "run", "--config", caddyfilePath, "--adapter", "caddyfile"}
	go caddycmd.Main()

	dispose := func() {
		os.Args = prevArgs
		_ = caddy.Stop()
	}

	return &reverseProxySetup{Port: port}, dispose
}

func createBasicReverseProxySetup(t *testing.T, f fixtures) (*reverseProxySetup, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	handleBlock := `handle /test/path* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
		}
	}`

	return createReverseProxySetup(t, handleBlock, map[string]string{
		"PYTHON_APP": f.PythonApp,
		"APP_SOCKET": filepath.Join(tmpDir, "app.sock"),
	})
}

// TestBasicReverseProxy is a static-control integration test.
// Strategy: configure reverse-bin with explicit exec + reverse_proxy_to, then
// verify one request succeeds through the Unix-socket backend.
func TestBasicReverseProxy(t *testing.T) {
	requireIntegration(t)

	setup, dispose := createBasicReverseProxySetup(t, mustFixtures(t))
	defer dispose()

	// Static baseline: request is routed to reverse-bin static upstream and
	// should include echoed request path from backend response.
	_, _ = assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/test/path", setup.Port), 200, "echo-backend", "basic reverse proxy must route request to echo backend")
}

// TestProcessCrashAndRestart verifies reverse-bin restarts a crashed backend process.
// Strategy:
//  1. First request via Caddy reaches shared Unix-socket echo backend and returns backend PID.
//  2. Call shared backend directly over Unix socket at /crash to force process exit.
//  3. Second request via Caddy succeeds and returns a different PID (restarted process).
func TestProcessCrashAndRestart(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socketPath := createSocketPath(t)
	setup, dispose := createReverseProxySetup(t, `handle /test/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
		}
	}`, map[string]string{
		"PYTHON_APP": f.PythonApp,
		"APP_SOCKET": socketPath,
	})
	defer dispose()

	parsePID := func(t *testing.T, body string) int {
		t.Helper()
		var payload struct {
			PID int `json:"pid"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("failed to parse JSON response %q: %v", body, err)
		}
		if payload.PID <= 0 {
			t.Fatalf("response does not contain valid pid: %q", body)
		}
		return payload.PID
	}

	client := newTestHTTPClient()

	// First request via Caddy proves backend starts and serves traffic.
	_, body1 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/test/first", setup.Port), 200, "\"pid\":", "first request must return backend pid before crash")
	pid1 := parsePID(t, body1)

	// Direct Unix-socket request to /crash intentionally terminates backend process.
	directTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	directClient := &http.Client{Transport: directTransport, Timeout: 5 * time.Second}
	resp, err := directClient.Get("http://unix/crash")
	if err == nil && resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// Wait until crashed process PID is gone.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid1, 0)
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend pid %d did not exit after /crash within timeout", pid1)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Second request via Caddy must succeed and come from a new backend PID.
	_, body2 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/test/second", setup.Port), 200, "\"pid\":", "second request must succeed with restarted backend pid")
	pid2 := parsePID(t, body2)
	if pid1 == pid2 {
		t.Fatalf("expected backend restart with different pid, got same pid=%d (first=%q second=%q)", pid1, body1, body2)
	}
}

// TestDynamicDiscovery is a dynamic-discovery integration test.
// Strategy:
//  1. Route only /dynamic/* to reverse-bin with dynamic_proxy_detector.
//  2. Add a separate static /path route that returns a fixed body.
//  3. Assert /dynamic/path is served by discovered backend, while /path is
//     served by static route. This proves matcher scoping + discovery/proxy flow.
func TestDynamicDiscovery(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socketPath := createSocketPath(t)
	detector := createExecutableScript(t, t.TempDir(), "detector-static.py", `#!/usr/bin/env python3
import json
import sys
from pathlib import Path

app_dir = Path(sys.argv[1]).resolve()
socket_path = Path(sys.argv[2]).resolve()
result = {
    "executable": ["python3", str(app_dir / "main.py")],
    "reverse_proxy_to": f"unix/{socket_path}",
    "working_directory": str(app_dir),
    "envs": [f"SOCKET_PATH={socket_path}"],
}
print(json.dumps(result))
`)

	setup, dispose := createReverseProxySetup(t, `# Only /dynamic/* routes use dynamic discovery.
	handle /dynamic/* {
		reverse-bin {
			dynamic_proxy_detector {{DETECTOR}} {{APP_DIR}} {{SOCKET_PATH}}
		}
	}
	# Explicit non-dynamic route for matcher verification.
	handle /path {
		respond "non-dynamic"
	}`, map[string]string{
		"DETECTOR":    detector,
		"APP_DIR":     f.AppDir,
		"SOCKET_PATH": socketPath,
	})
	defer dispose()

	client := newTestHTTPClient()

	// Positive path: /dynamic/* must go through dynamic discovery to the
	// discovered echo backend, identified by explicit marker in body.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/dynamic/path", setup.Port), 200, "echo-backend", "dynamic route must be served by discovered backend")

	// Control path: /path must NOT hit dynamic discovery; it should match the
	// explicit static handler and return the known marker body.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/path", setup.Port), 200, "non-dynamic", "non-dynamic route must match static handler")
}

// TestDynamicDiscovery_WithDiscoverAppPython verifies discover-app detects
// executable main.py apps and routes requests through the discovered backend.
func TestDynamicDiscovery_WithDiscoverAppPython(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)
	requireCommand(t, "uv")

	setup, dispose := createReverseProxySetup(t, `handle /dynamic/* {
		reverse-bin {
			dynamic_proxy_detector {{DETECTOR}} --no-sandbox {{APP_DIR}}
			health_check HEAD /
		}
	}`, map[string]string{
		"DETECTOR": f.DiscoverApp,
		"APP_DIR":  f.PythonTCPAppDir,
	})
	defer dispose()

	// HTTP request exercises python entrypoint detection in discover-app and verifies dynamic proxying end-to-end.
	_, body := assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/dynamic/python", setup.Port), 200, "Location: /dynamic/python", "python app should be detected by discover-app and serve dynamic request")
	if strings.Contains(body, "Environment Variables:") {
		t.Fatalf("python app response unexpectedly matched deno format: %q", body)
	}
}

// TestDynamicDiscovery_WithDiscoverAppDeno verifies discover-app detects
// main.ts apps and routes requests through the deno backend.
func TestDynamicDiscovery_WithDiscoverAppDeno(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)
	requireCommand(t, "uv")
	requireCommand(t, "deno")

	setup, dispose := createReverseProxySetup(t, `handle /dynamic/* {
		reverse-bin {
			dynamic_proxy_detector {{DETECTOR}} --no-sandbox {{APP_DIR}}
			health_check HEAD /
		}
	}`, map[string]string{
		"DETECTOR": f.DiscoverApp,
		"APP_DIR":  f.DenoAppDir,
	})
	defer dispose()

	// HTTP request exercises deno entrypoint detection in discover-app and verifies dynamic proxying end-to-end.
	_, body := assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/dynamic/deno", setup.Port), 200, "Location: /dynamic/deno", "deno app should be detected by discover-app and serve dynamic request")
	if !strings.Contains(body, "Environment Variables:") {
		t.Fatalf("deno app response missing deno marker section: %q", body)
	}
}

// TestDynamicDiscovery_DetectorFailure validates failure handling when the
// dynamic detector exits non-zero for a dynamic route.
func TestDynamicDiscovery_DetectorFailure(t *testing.T) {
	requireIntegration(t)

	failDetector := createExecutableScript(t, t.TempDir(), "detector-fail.py", `#!/usr/bin/env python3
import sys
print("detector failed on purpose", file=sys.stderr)
sys.exit(2)
`)

	setup, dispose := createReverseProxySetup(t, `handle /dynamic/* {
		reverse-bin {
			dynamic_proxy_detector {{DETECTOR}} {path}
		}
	}
	handle /ok {
		respond "ok"
	}`, map[string]string{"DETECTOR": failDetector})
	defer dispose()

	client := newTestHTTPClient()

	// Control request: non-dynamic route should remain healthy and return static body.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/ok", setup.Port), 200, "ok", "control route must remain healthy when detector fails")

	// Dynamic request: failing detector must surface as service unavailable.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/dynamic/fail", setup.Port), 503, "", "dynamic route must return 503 when detector exits non-zero")
}

// TestHealthCheck verifies Unix health behavior for GET, HEAD, and omitted health_check.
func TestHealthCheck(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	testCases := []struct {
		name            string
		healthDirective string
		expectedMethod  *string
	}{
		{name: "GET", healthDirective: "health_check GET /health", expectedMethod: ptr("GET")},
		{name: "HEAD", healthDirective: "health_check HEAD /health", expectedMethod: ptr("HEAD")},
		{name: "OMITTED", healthDirective: "", expectedMethod: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			socketPath := createSocketPath(t)

			setup, dispose := createReverseProxySetup(t, `handle_path /ready/* {
			reverse-bin {
				exec uv run --script {{PYTHON_APP}}
				reverse_proxy_to unix/{{APP_SOCKET}}
				env SOCKET_PATH={{APP_SOCKET}}
				# pass_all_env keeps uv/python runtime env (PATH/HOME/etc.) available in tests.
				pass_all_env
				{{HEALTH_DIRECTIVE}}
			}
		}`, map[string]string{
				"PYTHON_APP":       f.PythonApp,
				"APP_SOCKET":       socketPath,
				"HEALTH_DIRECTIVE": tc.healthDirective,
			})
			defer dispose()

			client := newTestHTTPClient()

			// Request through Caddy to prove proxying works with the configured health mode.
			_, pingBody := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/ready/ping", setup.Port), 200, "", "ready endpoint must proxy request to backend")
			var pingPayload struct {
				Backend string `json:"backend"`
				Path    string `json:"path"`
			}
			if err := json.Unmarshal([]byte(pingBody), &pingPayload); err != nil {
				t.Fatalf("failed to parse /ready/ping JSON %q: %v", pingBody, err)
			}
			if pingPayload.Backend != "echo-backend" || pingPayload.Path != "/ping" {
				t.Fatalf("unexpected /ready/ping payload: %s", pingBody)
			}

			// Request backend debug endpoint to verify whether /health was probed and by which method.
			_, healthBody := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/ready/health-last", setup.Port), 200, "", "health-last endpoint must return health probe metadata")
			if !strings.Contains(healthBody, "last_health_method") {
				t.Fatalf("/ready/health-last response must include last_health_method (body=%s)", healthBody)
			}
			var healthPayload struct {
				LastHealthMethod *string `json:"last_health_method"`
			}
			if err := json.Unmarshal([]byte(healthBody), &healthPayload); err != nil {
				t.Fatalf("failed to parse /ready/health-last JSON %q: %v", healthBody, err)
			}
			if tc.expectedMethod == nil {
				if healthPayload.LastHealthMethod != nil {
					t.Fatalf("expected null last_health_method when health_check is omitted, got %v (body=%s)", *healthPayload.LastHealthMethod, healthBody)
				}
			} else {
				if healthPayload.LastHealthMethod == nil || *healthPayload.LastHealthMethod != *tc.expectedMethod {
					t.Fatalf("expected health method %q, got %v (body=%s)", *tc.expectedMethod, healthPayload.LastHealthMethod, healthBody)
				}
			}
		})
	}
}

// TestHealthFailureTimeout validates that health polling timeout surfaces as 503.
// Strategy: start a long-running process that never binds reverse_proxy_to, so health
// cannot succeed and reverse-bin must fail request with service unavailable.
func TestHealthCheckAcceptsExplicitUnauthorizedStatus(t *testing.T) {
	requireIntegration(t)

	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("failed to allocate backend port: %v", err)
	}

	app := createExecutableScript(t, t.TempDir(), "auth-health.py", `#!/usr/bin/env python3
import http.server
import os

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/v2/":
            self.send_response(401)
            self.end_headers()
            self.wfile.write(b"auth-required")
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"registry-backend")

host = os.environ["REVERSE_BIN_HOST"]
port = int(os.environ["REVERSE_BIN_PORT"])
http.server.HTTPServer((host, port), Handler).serve_forever()
`)

	setup, dispose := createReverseProxySetup(t, `handle /registry/* {
		reverse-bin {
			exec {{APP}}
			env REVERSE_BIN_HOST=127.0.0.1 REVERSE_BIN_PORT={{BACKEND_PORT}}
			reverse_proxy_to 127.0.0.1:{{BACKEND_PORT}}
			health_check GET /v2/ 401
		}
	}`, map[string]string{
		"APP":          app,
		"BACKEND_PORT": strconv.Itoa(port),
	})
	defer dispose()

	client := newTestHTTPClient()
	// HTTP request verifies reverse-bin accepts auth-protected /v2/ health status 401 before proxying normal traffic.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/registry/ok", setup.Port), 200, "registry-backend", "explicit 401 health status must allow backend startup and proxying")
}

func TestHealthFailureTimeout(t *testing.T) {
	requireIntegration(t)

	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("failed to get free backend port: %v", err)
	}

	sleeper := createExecutableScript(t, t.TempDir(), "sleep-forever.sh", `#!/usr/bin/env sh
sleep 30
`)

	setup, dispose := createReverseProxySetup(t, `handle /fail/* {
		reverse-bin {
			exec {{SLEEPER}}
			reverse_proxy_to 127.0.0.1:{{BACKEND_PORT}}
			health_check GET /health
		}
	}`, map[string]string{
		"SLEEPER":      sleeper,
		"BACKEND_PORT": fmt.Sprintf("%d", port),
	})
	defer dispose()

	client := &http.Client{Transport: createTestingTransport(), Timeout: 20 * time.Second}
	// Request a proxied route to trigger backend startup + health polling.
	// Invariant: backend never binds the configured upstream, so health times out and reverse-bin must return 503.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/fail/test", setup.Port), 503, "", "request must fail with 503 when health polling times out")
}

// TestLifecycleIdleTimeout verifies a backend process is terminated after configured idle_timeout_ms.
func TestLifecycleIdleTimeout(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socketPath := createSocketPath(t)
	setup, dispose := createReverseProxySetup(t, `handle /test/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
			# pass_all_env keeps uv/python runtime env (PATH/HOME/etc.) available in tests.
			pass_all_env
			idle_timeout_ms 100
		}
	}`, map[string]string{
		"PYTHON_APP": f.PythonApp,
		"APP_SOCKET": socketPath,
	})
	defer dispose()

	parsePID := func(t *testing.T, body string) int {
		t.Helper()
		var payload struct {
			PID int `json:"pid"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("failed to parse JSON response %q: %v", body, err)
		}
		if payload.PID <= 0 {
			t.Fatalf("response does not contain valid pid: %q", body)
		}
		return payload.PID
	}

	client := newTestHTTPClient()

	// First request starts backend process and returns its PID.
	_, body1 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/test/first", setup.Port), 200, "", "first idle-timeout request must start backend and return pid")
	pid1 := parsePID(t, body1)

	// Wait without traffic so idle timeout can fire naturally.
	time.Sleep(250 * time.Millisecond)

	// Next request should be served by a newly spawned process.
	_, body2 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/test/second", setup.Port), 200, "", "second idle-timeout request must succeed after respawn")
	pid2 := parsePID(t, body2)
	if pid2 == pid1 {
		t.Fatalf("expected new pid after idle timeout; got same pid=%d (first=%s second=%s)", pid1, body1, body2)
	}
}

// TestMultipleApps verifies two independent reverse-bin handlers can run side-by-side
// with separate Unix sockets and processes.
// TestHealthImmediateExitFailsFast verifies startup failure is reported from process exit instead of health timeout.
func TestHealthImmediateExitFailsFast(t *testing.T) {
	requireIntegration(t)

	exiter := createExecutableScript(t, t.TempDir(), "exit-42.sh", `#!/usr/bin/env sh
exit 42
`)
	socketPath := createSocketPath(t)
	setup, dispose := createReverseProxySetup(t, `handle /failfast/* {
		reverse-bin {
			exec {{EXITER}}
			reverse_proxy_to unix/{{APP_SOCKET}}
		}
	}`, map[string]string{
		"EXITER":     exiter,
		"APP_SOCKET": socketPath,
	})
	defer dispose()

	client := newTestHTTPClient()
	started := time.Now()
	// HTTP request exercises startup path where backend exits before health can pass.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/failfast/test", setup.Port), 503, "", "immediate backend exit must return 503")
	elapsed := time.Since(started)
	if elapsed >= 2*time.Second {
		t.Fatalf("expected immediate backend exit to fail fast under 2s, took %s", elapsed)
	}
}

// TestLifecycleIdleTimeoutKillsChildProcessGroup verifies idle cleanup terminates child processes.
func TestLifecycleIdleTimeoutKillsChildProcessGroup(t *testing.T) {
	requireIntegration(t)
	if runtime.GOOS == "windows" {
		t.Skip("process groups differ on Windows")
	}

	socketPath := createSocketPath(t)
	backend := createExecutableScript(t, t.TempDir(), "parent-child.py", `#!/usr/bin/env python3
import http.server, json, os, signal, socket, subprocess, sys

socket_path = os.environ["SOCKET_PATH"]
if os.path.exists(socket_path):
    os.remove(socket_path)
child = subprocess.Popen(["sleep", "30"])

class UnixHTTPServer(http.server.HTTPServer):
    address_family = socket.AF_UNIX

class Handler(http.server.BaseHTTPRequestHandler):
    def address_string(self):
        return "unix"
    def do_GET(self):
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"parent": os.getpid(), "child": child.pid}).encode())

server = UnixHTTPServer(socket_path, Handler)
def stop(signum, frame):
    server.server_close()
    sys.exit(0)
signal.signal(signal.SIGTERM, stop)
server.serve_forever()
`)
	setup, dispose := createReverseProxySetup(t, `handle /child/* {
		reverse-bin {
			exec {{BACKEND}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
			idle_timeout_ms 100
		}
	}`, map[string]string{
		"BACKEND":    backend,
		"APP_SOCKET": socketPath,
	})
	defer dispose()

	// HTTP request exercises backend startup and returns parent/child PIDs for idle cleanup assertion.
	_, body := assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/child/pids", setup.Port), 200, "child", "child cleanup request must return spawned child pid")
	var payload struct {
		Parent int `json:"parent"`
		Child  int `json:"child"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("expected JSON parent/child pid payload, got %q: %v", body, err)
	}
	if payload.Parent <= 0 || payload.Child <= 0 || payload.Parent == payload.Child {
		t.Fatalf("expected distinct positive parent/child pids, got parent=%d child=%d", payload.Parent, payload.Child)
	}

	time.Sleep(500 * time.Millisecond)
	if processExists(payload.Child) {
		t.Fatalf("expected child pid %d to be gone after idle timeout process-group stop", payload.Child)
	}
}

// TestCleanupStopsBackendProcessGroup verifies module cleanup terminates running backend descendants.
func TestCleanupStopsBackendProcessGroup(t *testing.T) {
	requireIntegration(t)
	if runtime.GOOS == "windows" {
		t.Skip("process groups differ on Windows")
	}

	socketPath := createSocketPath(t)
	backend := createExecutableScript(t, t.TempDir(), "cleanup-parent-child.py", `#!/usr/bin/env python3
import http.server, json, os, signal, socket, subprocess, sys

socket_path = os.environ["SOCKET_PATH"]
if os.path.exists(socket_path):
    os.remove(socket_path)
child = subprocess.Popen(["sleep", "30"])

class UnixHTTPServer(http.server.HTTPServer):
    address_family = socket.AF_UNIX

class Handler(http.server.BaseHTTPRequestHandler):
    def address_string(self):
        return "unix"
    def do_GET(self):
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"parent": os.getpid(), "child": child.pid}).encode())

server = UnixHTTPServer(socket_path, Handler)
def stop(signum, frame):
    server.server_close()
    sys.exit(0)
signal.signal(signal.SIGTERM, stop)
server.serve_forever()
`)
	setup, dispose := createReverseProxySetup(t, `handle /cleanup/* {
		reverse-bin {
			exec {{BACKEND}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
		}
	}`, map[string]string{
		"BACKEND":    backend,
		"APP_SOCKET": socketPath,
	})

	// HTTP request exercises backend startup and returns child PID for cleanup assertion.
	_, body := assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/cleanup/pids", setup.Port), 200, "child", "cleanup request must return spawned child pid")
	var payload struct {
		Child int `json:"child"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("expected JSON child pid payload, got %q: %v", body, err)
	}
	if payload.Child <= 0 {
		t.Fatalf("expected positive child pid, got %d", payload.Child)
	}

	dispose()
	time.Sleep(500 * time.Millisecond)
	if processExists(payload.Child) {
		t.Fatalf("expected child pid %d to be gone after cleanup process-group stop", payload.Child)
	}
}

// TestUnixSocketMissingRestartsWithoutLeakingOldProcess verifies unhealthy alive backend is stopped before replacement.
func TestUnixSocketMissingRestartsWithoutLeakingOldProcess(t *testing.T) {
	requireIntegration(t)
	if runtime.GOOS == "windows" {
		t.Skip("Unix sockets not supported on Windows")
	}

	socketPath := createSocketPath(t)
	backend := createExecutableScript(t, t.TempDir(), "removable-socket.py", `#!/usr/bin/env python3
import http.server, json, os, socket

socket_path = os.environ["SOCKET_PATH"]
if os.path.exists(socket_path):
    os.remove(socket_path)

class UnixHTTPServer(http.server.HTTPServer):
    address_family = socket.AF_UNIX

class Handler(http.server.BaseHTTPRequestHandler):
    def address_string(self):
        return "unix"
    def _send(self, payload):
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(payload).encode())
    def do_GET(self):
        if self.path == "/remove-socket-and-stay-alive":
            if os.path.exists(socket_path):
                os.remove(socket_path)
            self._send({"removed": True, "pid": os.getpid()})
            return
        self._send({"pid": os.getpid()})

server = UnixHTTPServer(socket_path, Handler)
server.serve_forever()
`)
	setup, dispose := createReverseProxySetup(t, `handle /missing-socket/* {
		reverse-bin {
			exec {{BACKEND}}
			reverse_proxy_to unix/{{APP_SOCKET}}
			env SOCKET_PATH={{APP_SOCKET}}
		}
	}`, map[string]string{
		"BACKEND":    backend,
		"APP_SOCKET": socketPath,
	})
	defer dispose()

	parsePID := func(t *testing.T, body string) int {
		t.Helper()
		var payload struct {
			PID int `json:"pid"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("expected JSON pid payload, got %q: %v", body, err)
		}
		if payload.PID <= 0 {
			t.Fatalf("expected positive pid in payload, got %q", body)
		}
		return payload.PID
	}

	client := newTestHTTPClient()
	// HTTP request exercises initial backend startup and returns original PID.
	_, body1 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/missing-socket/first", setup.Port), 200, "pid", "initial missing-socket request must return backend pid")
	pid1 := parsePID(t, body1)

	directTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	directClient := &http.Client{Transport: directTransport, Timeout: 5 * time.Second}
	// Direct Unix-socket request simulates backend losing its listening socket while process remains alive.
	resp, err := directClient.Get("http://unix/remove-socket-and-stay-alive")
	if err != nil {
		t.Fatalf("expected direct unix request to remove socket, got error: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// HTTP request exercises supervisor restart after detecting missing Unix socket.
	_, body2 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/missing-socket/second", setup.Port), 200, "pid", "missing-socket request must restart backend")
	pid2 := parsePID(t, body2)
	if pid1 == pid2 {
		t.Fatalf("expected restarted backend pid to differ after missing socket, got same pid=%d", pid1)
	}
	time.Sleep(500 * time.Millisecond)
	if processExists(pid1) {
		t.Fatalf("expected old backend pid %d to be gone after missing socket restart", pid1)
	}
}

func TestMultipleApps(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socket1 := createSocketPath(t)
	socket2 := createSocketPath(t)

	setup, dispose := createReverseProxySetup(t, `handle_path /app1/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET_1}}
			env SOCKET_PATH={{APP_SOCKET_1}}
			pass_all_env
		}
	}
	handle_path /app2/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET_2}}
			env SOCKET_PATH={{APP_SOCKET_2}}
			pass_all_env
		}
	}`, map[string]string{
		"PYTHON_APP":   f.PythonApp,
		"APP_SOCKET_1": socket1,
		"APP_SOCKET_2": socket2,
	})
	defer dispose()

	parse := func(t *testing.T, body string) (pid int, path string, backend string) {
		t.Helper()
		var payload struct {
			PID     int    `json:"pid"`
			Path    string `json:"path"`
			Backend string `json:"backend"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("failed to parse response JSON %q: %v", body, err)
		}
		if payload.PID <= 0 {
			t.Fatalf("invalid pid in payload: %s", body)
		}
		return payload.PID, payload.Path, payload.Backend
	}

	client := newTestHTTPClient()

	_, body1 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/app1/test", setup.Port), 200, "", "app1 route must be served by its backend")
	pid1, path1, backend1 := parse(t, body1)
	if backend1 != "echo-backend" || path1 != "/test" {
		t.Fatalf("unexpected app1 payload: %s", body1)
	}

	_, body2 := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/app2/test", setup.Port), 200, "", "app2 route must be served by its backend")
	pid2, path2, backend2 := parse(t, body2)
	if backend2 != "echo-backend" || path2 != "/test" {
		t.Fatalf("unexpected app2 payload: %s", body2)
	}

	if pid1 == pid2 {
		t.Fatalf("expected distinct backend processes for app1/app2, got same pid=%d (app1=%s app2=%s)", pid1, body1, body2)
	}
}

// TestAllowDomainViaPathWildcard validates /allow/{app} path wildcard mapping to allow-domain checker decisions.
func TestAllowDomainViaPathWildcard(t *testing.T) {
	requireIntegration(t)

	appRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(appRoot, "existingapp"), 0o755); err != nil {
		t.Fatalf("failed to create existing app directory: %v", err)
	}

	allowSocket := createSocketPath(t)
	matches, err := filepath.Glob(filepath.Join(getRepoRoot(), "examples", "*", "allow-domain.py"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("failed to locate allow-domain script: %v", err)
	}
	allowScript := matches[0]

	setup, dispose := createReverseProxySetup(t, `@allow path_regexp allow ^/allow/([a-z0-9-]+)$
	handle @allow {
		rewrite * /allow-domain?domain={re.allow.1}.localhost
		reverse-bin {
			exec python3 {{ALLOW_SCRIPT}} {{ALLOW_SOCKET}} {{APP_ROOT}} --allowed-suffix .localhost
			reverse_proxy_to unix/{{ALLOW_SOCKET}}
		}
	}`, map[string]string{
		"ALLOW_SOCKET": allowSocket,
		"ALLOW_SCRIPT": allowScript,
		"APP_ROOT":     appRoot,
	})
	defer dispose()

	client := newTestHTTPClient()
	tests := []struct {
		name           string
		requestPath    string
		expectedStatus int
		expectedBody   string
		invariant      string
	}{
		{
			name:           "existing directory is allowed",
			requestPath:    "/allow/existingapp",
			expectedStatus: 200,
			expectedBody:   "ok",
			invariant:      "request for existing app path must return allowed from checker",
		},
		{
			name:           "missing directory is denied",
			requestPath:    "/allow/missingapp",
			expectedStatus: 403,
			expectedBody:   "forbidden",
			invariant:      "request for missing app path must return forbidden from checker",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// HTTP request exercises wildcard extraction from /allow/{app} and forwards app as checker domain query.
			_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d%s", setup.Port, tc.requestPath), tc.expectedStatus, tc.expectedBody, tc.invariant)
		})
	}
}
