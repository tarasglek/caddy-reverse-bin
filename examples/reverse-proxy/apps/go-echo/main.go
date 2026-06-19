package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

var lastHealthMethod *string

func main() {
	listener, err := listenFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(handle)}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func listenFromEnv() (net.Listener, error) {
	if socketPath := os.Getenv("SOCKET_PATH"); socketPath != "" {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
		return net.Listen("unix", socketPath)
	}

	host := os.Getenv("REVERSE_BIN_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("REVERSE_BIN_PORT")
	if port == "" {
		port = os.Getenv("PORT")
	}
	if port == "" {
		return nil, fmt.Errorf("SOCKET_PATH or REVERSE_BIN_PORT/PORT must be set")
	}
	return net.Listen("tcp", net.JoinHostPort(host, port))
}

func handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/crash":
		writeJSON(w, map[string]any{"status": "crashing", "pid": os.Getpid()})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		os.Exit(42)
	case "/health":
		method := r.Method
		lastHealthMethod = &method
		if os.Getenv("REQUIRE_FORWARDED_HEALTH") == "1" {
			host := r.Header.Get("X-Forwarded-Host")
			proto := r.Header.Get("X-Forwarded-Proto")
			if !strings.HasPrefix(host, "localhost:") || proto != "http" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprintf(w, "host=%s proto=%s", host, proto)
				return
			}
		}
		status := envInt("HEALTH_STATUS", http.StatusOK)
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "http://127.0.0.1:1/unreachable")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("healthy"))
	case "/health-last":
		writeJSON(w, map[string]any{"last_health_method": lastHealthMethod})
	case "/pid":
		writeJSON(w, map[string]any{"pid": os.Getpid()})
	default:
		writeJSON(w, map[string]any{
			"backend": "echo-backend",
			"pid":     os.Getpid(),
			"headers": r.Header,
			"path":    r.URL.Path,
		})
	}
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write response: %v", err)
	}
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
