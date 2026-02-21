package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

type fixtures struct {
	PythonApp string
	AppDir    string
}

func mustFixtures(t *testing.T) fixtures {
	t.Helper()
	repoRoot := getRepoRoot()
	f := fixtures{
		PythonApp: filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py"),
		AppDir:    filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo"),
	}
	requirePaths(t,
		pathCheck{Label: "python test app", Path: f.PythonApp, MustBeRegular: true},
		pathCheck{Label: "dynamic app dir", Path: f.AppDir, MustBeDir: true},
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
			env REVERSE_PROXY_TO=unix/{{APP_SOCKET}}
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
			env REVERSE_PROXY_TO=unix/{{APP_SOCKET}}
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
    "envs": [f"REVERSE_PROXY_TO=unix/{socket_path}"],
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

// TestReadinessCheck verifies Unix readiness behavior for GET, HEAD, and null readiness_check.
func TestReadinessCheck(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	testCases := []struct {
		name              string
		readinessDirective string
		expectedMethod    *string
	}{
		{name: "GET", readinessDirective: "readiness_check GET /health", expectedMethod: ptr("GET")},
		{name: "HEAD", readinessDirective: "readiness_check HEAD /health", expectedMethod: ptr("HEAD")},
		{name: "NULL", readinessDirective: "readiness_check null", expectedMethod: nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			socketPath := createSocketPath(t)

			setup, dispose := createReverseProxySetup(t, `handle_path /ready/* {
			reverse-bin {
				exec uv run --script {{PYTHON_APP}}
				reverse_proxy_to unix/{{APP_SOCKET}}
				env REVERSE_PROXY_TO=unix/{{APP_SOCKET}}
				# pass_all_env keeps uv/python runtime env (PATH/HOME/etc.) available in tests.
				pass_all_env
				{{READINESS_DIRECTIVE}}
			}
		}`, map[string]string{
				"PYTHON_APP":           f.PythonApp,
				"APP_SOCKET":           socketPath,
				"READINESS_DIRECTIVE": tc.readinessDirective,
			})
			defer dispose()

			client := newTestHTTPClient()

			// Request through Caddy to prove proxying works with the configured readiness mode.
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
			_, healthBody := assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/ready/health-last", setup.Port), 200, "", "health-last endpoint must return readiness probe metadata")
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
					t.Fatalf("expected null last_health_method for null readiness_check, got %v (body=%s)", *healthPayload.LastHealthMethod, healthBody)
				}
			} else {
				if healthPayload.LastHealthMethod == nil || *healthPayload.LastHealthMethod != *tc.expectedMethod {
					t.Fatalf("expected readiness method %q, got %v (body=%s)", *tc.expectedMethod, healthPayload.LastHealthMethod, healthBody)
				}
			}
		})
	}
}

// TestReadinessFailureTimeout validates that readiness polling timeout surfaces as 503.
// Strategy: start a long-running process that never binds reverse_proxy_to, so readiness
// cannot succeed and reverse-bin must fail request with service unavailable.
func TestReadinessFailureTimeout(t *testing.T) {
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
			readiness_check GET /health
		}
	}`, map[string]string{
		"SLEEPER":      sleeper,
		"BACKEND_PORT": fmt.Sprintf("%d", port),
	})
	defer dispose()

	client := &http.Client{Transport: createTestingTransport(), Timeout: 20 * time.Second}
	// Request a proxied route to trigger backend startup + readiness polling.
	// Invariant: backend never binds the configured upstream, so readiness times out and reverse-bin must return 503.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/fail/test", setup.Port), 503, "", "request must fail with 503 when readiness polling times out")
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
			env REVERSE_PROXY_TO=unix/{{APP_SOCKET}}
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
func TestMultipleApps(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socket1 := createSocketPath(t)
	socket2 := createSocketPath(t)

	setup, dispose := createReverseProxySetup(t, `handle_path /app1/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET_1}}
			env REVERSE_PROXY_TO=unix/{{APP_SOCKET_1}}
			pass_all_env
		}
	}
	handle_path /app2/* {
		reverse-bin {
			exec uv run --script {{PYTHON_APP}}
			reverse_proxy_to unix/{{APP_SOCKET_2}}
			env REVERSE_PROXY_TO=unix/{{APP_SOCKET_2}}
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

