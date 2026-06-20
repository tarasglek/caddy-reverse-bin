package reverseproxyexample_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gavv/httpexpect/v2"
)

func TestSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping example smoke test in short mode")
	}

	repoRoot := repoRoot(t)
	requireExampleBinaries(t, repoRoot)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, filepath.Join(repoRoot, "tmp", "caddy"), "run", "--adapter", "caddyfile", "--config", filepath.Join(repoRoot, "examples", "reverse-proxy", "Caddyfile"))
	cmd.Dir = repoRoot
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start example Caddy: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("example Caddy logs:\n%s", logs.String())
		}
	}()

	waitForHTTP(t, "127.0.0.1:9080")

	e := httpexpect.Default(t, "http://localhost:9080")

	// HTTP request tests the statically configured reverse-bin route that spawns Caddy file-server.
	e.GET("/static-detector/static/").Expect().Status(http.StatusOK).Body().Contains("<h1>reverse-bin static demo</h1>")

	// HTTP request tests the statically configured reverse-bin route that spawns the Go echo subprocess.
	e.GET("/static-detector/echo/").Expect().Status(http.StatusOK).JSON().Object().Value("backend").String().IsEqual("echo-backend")

	// HTTP request tests dynamic detector output for the static site app.
	e.GET("/dynamic-detector/static/").Expect().Status(http.StatusOK).Body().Contains("<h1>reverse-bin static demo</h1>")

	// HTTP request tests dynamic detector output for the Go echo app binary.
	e.GET("/dynamic-detector/go-echo/").Expect().Status(http.StatusOK).JSON().Object().Value("backend").String().IsEqual("echo-backend")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate smoke test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func requireExampleBinaries(t *testing.T, repoRoot string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(repoRoot, "tmp", "caddy"),
		filepath.Join(repoRoot, "examples", "reverse-proxy", "apps", "go-echo", "go-echo"),
		filepath.Join(repoRoot, "examples", "reverse-proxy", "detector", "example-detector"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("required example binary is missing; run make example-build first: %s: %v", path, err)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			t.Fatalf("required example binary is not executable: %s", path)
		}
	}
}

func waitForHTTP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %v", addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
