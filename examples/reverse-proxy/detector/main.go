package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type detectorResult struct {
	Executable       []string `json:"executable"`
	ReverseProxyTo   string   `json:"reverse_proxy_to"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	Envs             []string `json:"envs,omitempty"`
	HealthMethod     string   `json:"health_method,omitempty"`
	HealthPath       string   `json:"health_path,omitempty"`
}

func main() {
	// This detector is intentionally small: it maps the first request path
	// segment to examples/reverse-proxy/apps/<app>, then detects only two app
	// shapes: an index.html static directory or an executable named like the app.
	// More capable runtime detection belongs in reverse-bin-hosting.
	if len(os.Args) != 2 {
		fatalf("usage: example-detector REQUEST_PATH")
	}

	root, err := os.Getwd()
	if err != nil {
		fatalf("get working directory: %v", err)
	}

	appName := firstPathSegment(os.Args[1])
	if appName == "" {
		fatalf("missing app name in path %q", os.Args[1])
	}
	appDir := filepath.Join(root, "examples", "reverse-proxy", "apps", appName)
	if info, err := os.Stat(appDir); err != nil || !info.IsDir() {
		fatalf("app directory missing: %s", appDir)
	}

	if fileExists(filepath.Join(appDir, "index.html")) {
		// handle_path strips /dynamic-detector but leaves /<app>/... in the
		// upstream request path, so serve the parent apps directory. That lets
		// /dynamic-detector/static/ resolve to apps/static/index.html.
		writeResult(detectorResult{
			Executable:       []string{"./tmp/caddy", "file-server", "--listen", "127.0.0.1:19082", "--root", filepath.Dir(appDir)},
			ReverseProxyTo:   "127.0.0.1:19082",
			WorkingDirectory: root,
			HealthMethod:     "GET",
			HealthPath:       "/" + appName + "/",
		})
		return
	}

	binaryPath := filepath.Join(appDir, appName)
	if isExecutable(binaryPath) {
		// Executable apps get a deterministic per-app Unix socket so multiple
		// detected apps can run independently without hardcoded route blocks.
		writeResult(detectorResult{
			Executable:       []string{binaryPath},
			ReverseProxyTo:   fmt.Sprintf("unix//tmp/reverse-bin-dynamic-%s.sock", appName),
			WorkingDirectory: appDir,
			Envs:             []string{fmt.Sprintf("SOCKET_PATH=/tmp/reverse-bin-dynamic-%s.sock", appName)},
		})
		return
	}

	fatalf("unsupported app %q: expected index.html or executable %s", appName, binaryPath)
}

func firstPathSegment(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	return strings.Split(path, "/")[0]
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func writeResult(result detectorResult) {
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatalf("encode detector result: %v", err)
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
