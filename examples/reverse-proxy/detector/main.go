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
	if len(os.Args) != 2 {
		fatalf("usage: example-detector REQUEST_PATH")
	}

	root, err := os.Getwd()
	if err != nil {
		fatalf("get working directory: %v", err)
	}

	requestPath := strings.TrimPrefix(os.Args[1], "/")
	parts := strings.Split(requestPath, "/")
	if len(parts) == 0 || parts[0] == "" {
		fatalf("missing app name in path %q", os.Args[1])
	}

	var result detectorResult
	switch parts[0] {
	case "static":
		indexPath := filepath.Join(root, "examples/reverse-proxy/apps/static-site/index.html")
		if _, err := os.Stat(indexPath); err != nil {
			fatalf("static app missing index.html: %v", err)
		}
		result = detectorResult{
			Executable:       []string{"./tmp/caddy", "file-server", "--listen", "127.0.0.1:19082", "--root", "./examples/reverse-proxy/apps/static-site"},
			ReverseProxyTo:   "127.0.0.1:19082",
			WorkingDirectory: root,
			HealthMethod:     "GET",
			HealthPath:       "/",
		}
	case "echo":
		goEcho := filepath.Join(root, "examples/reverse-proxy/apps/go-echo/go-echo")
		if _, err := os.Stat(goEcho); err != nil {
			fatalf("go echo app binary missing: %v", err)
		}
		result = detectorResult{
			Executable:       []string{goEcho},
			ReverseProxyTo:   "unix//tmp/reverse-bin-dynamic-go-echo.sock",
			WorkingDirectory: filepath.Dir(goEcho),
			Envs:             []string{"SOCKET_PATH=/tmp/reverse-bin-dynamic-go-echo.sock"},
		}
	default:
		fatalf("unknown app %q", parts[0])
	}

	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatalf("encode detector result: %v", err)
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
