package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/quic-go/quic-go/http3"

	"github.com/tarasglek/caddy-reverse-bin/internal/testapp"
)

func TestMain(m *testing.M) {
	testapp.Main()
	os.Exit(m.Run())
}

// serve runs Caddy with one HTTPS site whose body is site and returns its
// URL. The certificate is made here so it exists before the first request.
func serve(t *testing.T, site string) string {
	t.Helper()
	// Caddy listens on TCP and, for HTTP/3, UDP: the port must be free for both.
	udp, err := net.ListenPacket("udp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = udp.Close(), tcp.Close()
	cfg := fmt.Sprintf(`{
		admin off
		storage file_system %s
		auto_https off
	}
	https://localhost:%d {
		tls %s
		%s
	}`, t.TempDir(), port, certFiles(t), strings.ReplaceAll(site, "{tmp}", t.TempDir()))
	adapted, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cfg), nil)
	if err == nil {
		err = caddy.Load(adapted, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = caddy.Stop() })
	return fmt.Sprintf("https://localhost:%d", port)
}

// certFiles writes a self-signed certificate for localhost and returns
// "<cert> <key>".
func certFiles(t *testing.T) string {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1),
		DNSNames: []string{"localhost"}, NotAfter: time.Now().Add(time.Hour)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	cert, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err == nil {
		err = errors.Join(os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600),
			os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	}
	if err != nil {
		t.Fatal(err)
	}
	return cert + " " + keyFile
}

func transport(proto string) http.RoundTripper {
	insecure := &tls.Config{InsecureSkipVerify: true} // transports modify it
	if proto == "HTTP/3.0" {
		return &http3.Transport{TLSClientConfig: insecure}
	}
	tr := &http.Transport{TLSClientConfig: insecure, Protocols: new(http.Protocols)}
	tr.Protocols.SetHTTP1(proto == "HTTP/1.1")
	tr.Protocols.SetHTTP2(proto == "HTTP/2.0")
	return tr
}

// get sends GET url with header X-App: app over proto (default HTTP/1.1)
// and returns the status and body.
func get(t *testing.T, url, app string, proto ...string) (int, string) {
	proto = append(proto, "HTTP/1.1")
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-App", app)
	resp, err := (&http.Client{Transport: transport(proto[0]), Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.Proto != proto[0] {
		t.Fatalf("response over %s, want %s", resp.Proto, proto[0])
	}
	return resp.StatusCode, string(body)
}

// static is a reverse_proxy block running the test app on a unix socket
// named by the X-App header; the second %s adds env entries.
const static = `reverse_proxy {
		dynamic reverse-bin {
			exec %s
			reverse_proxy_to unix/{tmp}/{http.request.header.X-App}.sock
			env TEST_APP_DIAL=unix/{tmp}/{http.request.header.X-App}.sock %s
			%s
		}
		lb_retries 1
	}`

// TestAppIdlesAfterResponse verifies a request releases its app when Caddy
// is done with it, over each HTTP version: the app idles out after the
// response. This rests on the request context ending after the handler
// returns, which reverse-bin assumes.
func TestAppIdlesAfterResponse(t *testing.T) {
	hold, dead := testapp.HoldListener(t)
	base := serve(t, fmt.Sprintf(static, os.Args[0], "TEST_APP_HOLD="+hold, "idle_timeout_ms 100"))
	for _, proto := range []string{"HTTP/1.1", "HTTP/2.0", "HTTP/3.0"} {
		// GET /pid over proto starts the app, which connects to hold.
		if status, _ := get(t, base+"/pid", "a", proto); status != 200 {
			t.Fatalf("%s: status %d, want 200", proto, status)
		}
		if err := dead(10 * time.Second); !errors.Is(err, io.EOF) {
			t.Fatalf("%s: app holder read = %v, want EOF from an idle stop", proto, err)
		}
	}
}

// TestDetector verifies a detector's App is run, and that a failing detector
// answers 503 at once.
func TestDetector(t *testing.T) {
	dial := "unix/" + filepath.Join(t.TempDir(), "app.sock")
	out := filepath.Join(t.TempDir(), "app.json")
	app := fmt.Sprintf(`{"executable":[%q],"reverse_proxy_to":%q,"envs":["TEST_APP_DIAL=%s"]}`, os.Args[0], dial, dial)
	if err := os.WriteFile(out, []byte(app), 0o600); err != nil {
		t.Fatal(err)
	}
	base := serve(t, `handle /pid {
		reverse_proxy {
			dynamic reverse-bin {
				dynamic_proxy_detector cat `+out+`
			}
		}
	}
	reverse_proxy {
		dynamic reverse-bin {
			dynamic_proxy_detector false
		}
		lb_retries 1
	}`)
	// GET /pid: served by the detected app.
	if status, pid := get(t, base+"/pid", ""); status != 200 || pid == "" {
		t.Fatalf("detected app: %d %q, want 200 with a pid", status, pid)
	}
	// GET /fail: the detector exits 1, so the request fails with 503.
	start := time.Now()
	if status, _ := get(t, base+"/fail", ""); status != 503 || time.Since(start) > 2*time.Second {
		t.Fatalf("failing detector: %d after %v, want 503 at once", status, time.Since(start))
	}
}

// TestAppPerKeyAndRetry verifies each distinct placeholder expansion is its
// own app, a repeated one reuses it, and with lb_retries 1 a request arriving
// as the app exits is served by a new app.
func TestAppPerKeyAndRetry(t *testing.T) {
	base := serve(t, fmt.Sprintf(static, os.Args[0], "", ""))
	// GET /pid with X-App a, b: two apps. GET /exit with a: same app, which exits.
	_, a := get(t, base+"/pid", "a")
	_, b := get(t, base+"/pid", "b")
	_, again := get(t, base+"/exit", "a")
	if a == "" || a == b || a != again {
		t.Fatalf("pids a=%q b=%q a=%q, want two apps with a reused", a, b, again)
	}
	// GET /pid with a right away: a new app answers.
	if status, next := get(t, base+"/pid", "a"); status != 200 || next == a {
		t.Fatalf("after exit: %d pid %q, want 200 from a new app (was %q)", status, next, a)
	}
}

// TestStopLeavesNoApp verifies stopping Caddy stops and reaps every app.
func TestStopLeavesNoApp(t *testing.T) {
	base := serve(t, fmt.Sprintf(static, os.Args[0], "", ""))
	// GET /pid: starts the app.
	_, pid := get(t, base+"/pid", "a")
	var n int
	if _, err := fmt.Sscan(pid, &n); err != nil {
		t.Fatal(err)
	}
	if err := caddy.Stop(); err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(n, 0) == nil {
		t.Fatalf("app %d still exists after Caddy stopped", n)
	}
}

// TestWebSocketHoldsTheApp verifies an upgraded connection counts as a
// request for its whole life: the app does not idle while it is open, and
// idles out once it closes.
func TestWebSocketHoldsTheApp(t *testing.T) {
	hold, dead := testapp.HoldListener(t)
	base := serve(t, fmt.Sprintf(static, os.Args[0], "TEST_APP_HOLD="+hold, "idle_timeout_ms 100"))
	conn, err := tls.Dial("tcp", strings.TrimPrefix(base, "https://"), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	// GET /ws with Upgrade: the proxy tunnels it to the app, which echoes.
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nX-App: a\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v, want 101", resp, err)
	}
	echo := make([]byte, 4)
	fmt.Fprint(conn, "ping")
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo = %q, %v; want ping", echo, err)
	}
	if err := dead(10 * 100 * time.Millisecond); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("app holder read = %v while the WebSocket is open, want it alive", err)
	}
	_ = conn.Close()
	if err := dead(10 * time.Second); !errors.Is(err, io.EOF) {
		t.Fatalf("app holder read = %v after the WebSocket closed, want EOF from an idle stop", err)
	}
}
