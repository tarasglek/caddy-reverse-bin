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

func startTestServer(t *testing.T, httpPort, httpsPort int, siteBlocks string) *Tester {
	t.Helper()
	tester := NewTester(t)
	tester.InitServerWithDefaults(httpPort, httpsPort, siteBlocks)
	return tester
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

func newUnixHTTPClient(socketPath string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func assertNonEmpty200Unix(t *testing.T, client *http.Client, rawURL string) string {
	t.Helper()
	var (
		resp *http.Response
		err  error
	)
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err = client.Get(rawURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("request failed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed reading response body: %v", err)
	}
	body := string(bodyBytes)

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for %s, got %d (body: %s)", rawURL, resp.StatusCode, body)
	}
	if body == "" {
		t.Fatalf("expected non-empty response body for %s", rawURL)
	}
	return body
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

func assertNonEmpty200(t *testing.T, tester *Tester, rawURL string) string {
	t.Helper()
	resp, body := tester.AssertGetResponse(rawURL, 200, "")
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

func TestBasicReverseProxy(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	fixture := `
{
	admin localhost:2999
	http_port 9080
	https_port 9443
}

http://unix/{{CADDY_SOCKET}} {
	reverse-bin {
		exec uv run --script {{PYTHON_APP}}
		reverse_proxy_to unix/{{APP_SOCKET}}
		env REVERSE_PROXY_TO=unix/{{APP_SOCKET}}
		pass_all_env
	}
}
`

	caddySocketPath := createSocketPath(t)
	appSocketPath := createSocketPath(t)

	rendered := renderTemplate(fixture, map[string]string{
		"CADDY_SOCKET": caddySocketPath,
		"PYTHON_APP":   f.PythonApp,
		"APP_SOCKET":   appSocketPath,
	})

	tester := NewTester(t)
	tester.InitServer(rendered, "caddyfile")

	client := newUnixHTTPClient(caddySocketPath)
	_ = assertNonEmpty200Unix(t, client, "http://unix/test/path")
}

func TestDynamicDiscovery(t *testing.T) {
	requireIntegration(t)
	f := mustFixtures(t)

	siteBlocks := siteWithReverseBin(
		"localhost:9082",
		reverseBinDynamicDetectorBlock([]string{"uv", "run", "--script", f.Detector, f.AppDir}),
	)
	tester := startTestServer(t, 9082, 9445, siteBlocks)

	_ = assertNonEmpty200(t, tester, "http://localhost:9082/dynamic/test")
}

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
