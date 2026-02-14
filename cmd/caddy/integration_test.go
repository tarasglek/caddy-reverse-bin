package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	Detector  string
}

func mustFixtures(t *testing.T) fixtures {
	t.Helper()
	repoRoot := getRepoRoot()
	f := fixtures{
		PythonApp: filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo/main.py"),
		AppDir:    filepath.Join(repoRoot, "examples/reverse-proxy/apps/python3-unix-echo"),
		Detector:  filepath.Join(repoRoot, "utils/discover-app/discover-app.py"),
	}
	requirePaths(t,
		pathCheck{Label: "python test app", Path: f.PythonApp, MustBeRegular: true},
		pathCheck{Label: "dynamic app dir", Path: f.AppDir, MustBeDir: true},
		pathCheck{Label: "dynamic detector", Path: f.Detector, MustBeRegular: true},
	)
	return f
}

func readCaddyFixture(t *testing.T, fixturePath string) string {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", fixturePath, err)
	}
	return string(b)
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

func assertGetResponse(t *testing.T, client *http.Client, requestURI string, expectedStatusCode int, expectedBodyContains string) (*http.Response, string) {
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
			t.Fatalf("failed to call server: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("unable to read response body: %v", err)
	}
	body := string(bodyBytes)

	if resp.StatusCode != expectedStatusCode {
		t.Fatalf("requesting %q expected status %d but got %d (body: %s)", requestURI, expectedStatusCode, resp.StatusCode, body)
	}
	if expectedBodyContains != "" && !strings.Contains(body, expectedBodyContains) {
		t.Fatalf("requesting %q expected body to contain %q but got %q", requestURI, expectedBodyContains, body)
	}
	return resp, body
}

func assertStatus5xx(t *testing.T, client *http.Client, rawURL string) string {
	t.Helper()
	resp, err := client.Get(rawURL)
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

func assertNonEmpty200(t *testing.T, client *http.Client, rawURL string) string {
	t.Helper()
	resp, body := assertGetResponse(t, client, rawURL, 200, "")
	if body == "" {
		t.Fatalf("expected non-empty response body for %s (status=%d headers=%v)", rawURL, resp.StatusCode, resp.Header)
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

func reverseBinDynamicDetectorBlock(detectorArgs []string, extraDirectives ...string) string {
	directives := []string{fmt.Sprintf("dynamic_proxy_detector %s", strings.Join(detectorArgs, " "))}
	directives = append(directives, extraDirectives...)
	return fmt.Sprintf("reverse-bin {\n\t\t%s\n\t}", strings.Join(directives, "\n\t\t"))
}

func siteWithReverseBin(host string, block string) string {
	return fmt.Sprintf("\nhttp://%s {\n\t%s\n}\n", host, block)
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
			pass_all_env
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
	_, _ = assertGetResponse(t, newTestHTTPClient(), fmt.Sprintf("http://localhost:%d/test/path", setup.Port), 200, "echo-backend")
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

	setup, dispose := createReverseProxySetup(t, `# Only /dynamic/* routes use dynamic discovery.
	handle /dynamic/* {
		reverse-bin {
			dynamic_proxy_detector uv run --script {{DETECTOR}} {{APP_DIR}}
		}
	}
	# Explicit non-dynamic route for matcher verification.
	handle /path {
		respond "non-dynamic"
	}`, map[string]string{
		"DETECTOR": f.Detector,
		"APP_DIR":  f.AppDir,
	})
	defer dispose()

	client := newTestHTTPClient()

	// Positive path: /dynamic/* must go through dynamic discovery to the
	// discovered echo backend, identified by explicit marker in body.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/dynamic/path", setup.Port), 200, "echo-backend")

	// Control path: /path must NOT hit dynamic discovery; it should match the
	// explicit static handler and return the known marker body.
	_, _ = assertGetResponse(t, client, fmt.Sprintf("http://localhost:%d/path", setup.Port), 200, "non-dynamic")
}

/*
func TestDynamicDiscovery_DetectorFailure(t *testing.T) {
	requireIntegration(t)

	tmpDir := t.TempDir()
	failDetector := createExecutableScript(t, tmpDir, "detector-fail.py", `#!/usr/bin/env python3
import sys
print("detector failed on purpose", file=sys.stderr)
sys.exit(2)
`)

	siteBlocks := siteWithReverseBin(
		"localhost:9086",
		reverseBinDynamicDetectorBlock([]string{failDetector, "{path}"}),
	)
	tester := startTestServer(t, 9086, 9449, siteBlocks)

	body := assertStatus5xx(t, tester, "http://localhost:9086/fail")
	if !strings.Contains(body, "dynamic proxy detector failed") {
		t.Logf("expected detector failure text, got: %s", body)
	}
}

func TestDynamicDiscovery_FirstRequestOK_SecondPathFails(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

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
`, f.PythonApp, "unix/"+socketPath, "REVERSE_PROXY_TO=unix/"+socketPath))

	siteBlocks := siteWithReverseBin(
		"localhost:9087",
		reverseBinDynamicDetectorBlock([]string{detector, "{path}"}, "pass_all_env"),
	)
	tester := startTestServer(t, 9087, 9450, siteBlocks)

	_ = assertNonEmpty200(t, tester, "http://localhost:9087/ok")
	_ = assertStatus5xx(t, tester, "http://localhost:9087/bad")
	_ = assertNonEmpty200(t, tester, "http://localhost:9087/ok")
}

func TestLifecycleIdleTimeout(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socketPath := createSocketPath(t)
	siteBlocks := siteWithReverseBin("localhost:9083", reverseBinStaticAppBlock(f.PythonApp, socketPath))
	tester := startTestServer(t, 9083, 9446, siteBlocks)

	body1 := assertNonEmpty200(t, tester, "http://localhost:9083/first")
	t.Logf("First response: %s", body1)

	body2 := assertNonEmpty200(t, tester, "http://localhost:9083/second")
	t.Logf("Second response: %s", body2)

	// Note: Testing actual idle timeout cleanup would require:
	// 1. Adding idle_timeout config option to reverse-bin
	// 2. Waiting for the timeout period
	// 3. Verifying the process is terminated
	// This is left as a future enhancement
}

func TestReadinessCheck(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socketPath := createSocketPath(t)
	siteBlocks := siteWithReverseBin("localhost:9084", reverseBinStaticAppBlock(f.PythonApp, socketPath, "readiness_check GET /"))
	tester := startTestServer(t, 9084, 9447, siteBlocks)

	body := assertNonEmpty200(t, tester, "http://localhost:9084/ready")
	t.Logf("Response after readiness: %s", body)
}

func TestMultipleApps(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	socket1 := createSocketPath(t)
	socket2 := createSocketPath(t)

	siteBlocks := `
http://localhost:9085 {
	handle /app1* {
		` + reverseBinStaticAppBlock(f.PythonApp, socket1) + `
	}
	handle /app2* {
		` + reverseBinStaticAppBlock(f.PythonApp, socket2) + `
	}
}
`
	tester := startTestServer(t, 9085, 9448, siteBlocks)

	body1 := assertNonEmpty200(t, tester, "http://localhost:9085/app1/test")
	t.Logf("App1 response: %s", body1)

	body2 := assertNonEmpty200(t, tester, "http://localhost:9085/app2/test")
	t.Logf("App2 response: %s", body2)
}
*/
