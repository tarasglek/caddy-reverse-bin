// Package testapp is the app the tests launch: each test binary calls Main
// from TestMain and so doubles as the app when re-executed.
package testapp

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Main serves HTTP on the unix socket TEST_APP_DIAL, if set, and never returns:
//
//	GET /pid    -> pid
//	GET /env    -> environment, one KEY=value per line
//	GET /health -> status TEST_APP_HEALTH, default 200, Location /unreachable (503);
//	               400 unless "<method> <host>" equals TEST_APP_HEALTH_WANT, if set,
//	               and 400 if X-Leak is set
//	GET /exit   -> answers, then exits
//	GET /close  -> closes the listener, stays alive
//	GET /ws     -> 101 Switching Protocols, then echoes bytes until closed
//
// TEST_APP_IGNORE_TERM ignores SIGTERM. TEST_APP_HOLD=<socket> connects there
// for life, so the test sees EOF when the process dies; TEST_APP_CHILD does
// the same from a child process.
func Main() {
	dial, hold := os.Getenv("TEST_APP_DIAL"), os.Getenv("TEST_APP_HOLD")
	if hold != "" {
		conn, err := net.Dial("unix", hold)
		must(err)
		defer conn.Close() // keeps conn reachable for the process's life
	}
	if dial == "" {
		if hold != "" {
			select {} // the child
		}
		return
	}
	if os.Getenv("TEST_APP_IGNORE_TERM") != "" {
		signal.Ignore(syscall.SIGTERM)
	}
	if child := os.Getenv("TEST_APP_CHILD"); child != "" {
		cmd := exec.Command(os.Args[0])
		cmd.Env = []string{"TEST_APP_HOLD=" + child}
		must(cmd.Start())
	}
	path := strings.TrimPrefix(dial, "unix/")
	_ = os.Remove(path) // a previous instance's socket, as launchers must
	l, err := net.Listen("unix", path)
	must(err)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	http.HandleFunc("/pid", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, os.Getpid()) })
	http.HandleFunc("/env", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Join(os.Environ(), "\n")) })
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		status, err := strconv.Atoi(os.Getenv("TEST_APP_HEALTH"))
		if err != nil {
			status = http.StatusOK
		}
		if want := os.Getenv("TEST_APP_HEALTH_WANT"); want != "" && want != r.Method+" "+r.Host || r.Header.Get("X-Leak") != "" {
			status = http.StatusBadRequest
		}
		w.Header().Set("Location", "/unreachable")
		w.WriteHeader(status)
	})
	http.HandleFunc("/unreachable", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	http.HandleFunc("/exit", func(w http.ResponseWriter, _ *http.Request) {
		_ = l.Close()
		w.Header().Set("Connection", "close")
		fmt.Fprint(w, os.Getpid())
		w.(http.Flusher).Flush()
		os.Exit(0)
	})
	http.HandleFunc("/close", func(http.ResponseWriter, *http.Request) { _ = l.Close() })
	http.HandleFunc("/ws", func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		must(err)
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
		_ = conn.Close()
	})
	_ = http.Serve(l, nil) // returns once /exit or /close closed the listener
	select {}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// HoldListener returns a socket for TEST_APP_HOLD or TEST_APP_CHILD, and a
// func that waits up to wait for the current holder (accepted on first use)
// to die: it returns io.EOF when it died, a timeout error while it lives.
func HoldListener(t *testing.T) (string, func(wait time.Duration) error) {
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "hold.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var conn net.Conn
	return l.Addr().String(), func(wait time.Duration) error {
		if conn == nil {
			if conn, err = l.Accept(); err != nil {
				return err
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(wait))
		if _, err = conn.Read(make([]byte, 1)); errors.Is(err, io.EOF) {
			_ = conn.Close()
			conn = nil // the next holder is a new process
		}
		return err
	}
}
