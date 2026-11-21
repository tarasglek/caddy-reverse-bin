package reversebin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap/zaptest"

	"github.com/tarasglek/caddy-reverse-bin/internal/testapp"
)

func TestMain(m *testing.M) {
	testapp.Main()
	stopGrace = 500 * time.Millisecond
	becomeSubreaper()
	os.Exit(m.Run())
}

// testApp is a static config running the test binary on a unix socket in a
// directory that does not exist yet.
func testApp(t *testing.T, env ...string) *ReverseBin {
	dial := "unix/" + filepath.Join(t.TempDir(), "app.sock")
	c := &ReverseBin{App: App{Executable: []string{os.Args[0]}, ReverseProxyTo: dial, Envs: append([]string{"TEST_APP_DIAL=" + dial}, env...)}}
	return c
}

// provision readies c as Caddy would; cleanup cancels its context and waits.
func provision(t *testing.T, c *ReverseBin) (cleanup func()) {
	t.Helper()
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	if err := c.Provision(ctx); err != nil {
		t.Fatal(err)
	}
	c.logger = zaptest.NewLogger(t)
	cleanup = func() { cancel(); _ = c.Cleanup() }
	t.Cleanup(cleanup)
	return cleanup
}

func request(vars map[string]any) *http.Request {
	repl := caddy.NewReplacer()
	for k, v := range vars {
		repl.Set(k, v)
	}
	r := httptest.NewRequest("GET", "http://example.test/", nil)
	r.Header.Set("X-Leak", "1") // must never reach a health probe
	ctx := context.WithValue(r.Context(), caddy.ReplacerCtxKey, repl)
	return r.WithContext(context.WithValue(ctx, caddyhttp.VarsCtxKey, map[string]any{}))
}

// use is one proxied request: get the upstream, GET path from it, and end
// the request (which releases the app). It returns the body, "" on error.
func use(t *testing.T, c *ReverseBin, path string) string {
	t.Helper()
	r := request(nil)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ups, err := c.GetUpstreams(r.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	return get(ups[0].Dial, path)
}

// get sends GET path straight to the app at dial.
func get(dial, path string) string {
	na, _ := caddy.ParseNetworkAddress(dial)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return new(net.Dialer).DialContext(ctx, na.Network, na.JoinHostPort(0))
		}}}
	resp, err := client.Get("http://app" + path)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// writeFile returns the path of a new file holding content.
func writeFile(t *testing.T, content string) string {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func alive(pid string) bool {
	n, err := strconv.Atoi(pid)
	return err == nil && syscall.Kill(n, 0) == nil
}

// TestCaddyfile verifies every subdirective sets its field, and that unknown
// names and wrong argument counts are rejected.
func TestCaddyfile(t *testing.T) {
	got := new(ReverseBin)
	err := got.UnmarshalCaddyfile(caddyfile.NewTestDispenser(`reverse-bin {
		exec ./app --flag
		dir /srv
		env A=1 B={env.B}
		reverse_proxy_to unix//run/app.sock
		health_check GET /health 2xx
		dynamic_proxy_detector ./detect {host}
		idle_timeout_ms 120000
		health_timeout_ms 60000
	}`))
	want := &ReverseBin{App: App{Executable: []string{"./app", "--flag"}, WorkingDirectory: "/srv", Envs: []string{"A=1", "B={env.B}"},
		ReverseProxyTo: "unix//run/app.sock", HealthMethod: "GET", HealthPath: "/health", HealthStatus: 2},
		DynamicProxyDetector: []string{"./detect", "{host}"}, IdleTimeoutMS: 120000, HealthTimeoutMS: 60000}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v\nwant %+v", got, err, want)
	}
	for _, line := range []string{"bogus 1", "exec", "dir", "dir a b", "env", "reverse_proxy_to", "health_check GET", "health_check GET / ok", "dynamic_proxy_detector", "idle_timeout_ms", "health_timeout_ms soon"} {
		if err := new(ReverseBin).UnmarshalCaddyfile(caddyfile.NewTestDispenser("reverse-bin {\n" + line + "\n}")); err == nil {
			t.Errorf("%q: want parse error", line)
		}
	}
}

// TestProvision runs Caddy's real provisioning of a reverse_proxy using
// reverse-bin. Invariant: a block is either static (exec+reverse_proxy_to) or a detector.
func TestProvision(t *testing.T) {
	for source, wantErr := range map[string]string{
		`"executable":["x"],"reverse_proxy_to":"unix//x.sock"`:    "",
		`"dynamic_proxy_detector":["./detect"]`:                   "",
		`"dynamic_proxy_detector":["./d"],"executable":["x"]`:     "detector excludes",
		`"executable":["x"]`:                                      "need exec and reverse_proxy_to",
		`"envs":["A=1"],"health_path":"/"`:                        "need exec and reverse_proxy_to",
		`"dynamic_proxy_detector":["./d"],"health_path":"/ready"`: "detector excludes",
		`"dynamic_proxy_detector":["./d"],"idleTimeoutMs":-1`:     "must not be negative",
	} {
		cfg := `{"admin":{"disabled":true},"apps":{"http":{"servers":{"s":{"listen":["127.0.0.1:0"],"routes":[{"handle":[
			{"handler":"reverse_proxy","dynamic_upstreams":{"source":"reverse-bin",` + source + `}}]}]}}}}}`
		var parsed caddy.Config
		if err := json.Unmarshal([]byte(cfg), &parsed); err != nil {
			t.Fatal(err)
		}
		err := caddy.Validate(&parsed)
		if (wantErr == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want %q", source, err, wantErr)
		}
	}
}

// TestValidateDial pins which dial addresses an app may have: exactly what
// Caddy's proxy can dial as one socket.
func TestValidateDial(t *testing.T) {
	for dial, ok := range map[string]bool{
		"unix//run/a.sock": true, "unix/rel.sock": true, "127.0.0.1:9000": true, "localhost:9000": true, ":9000": true,
		"localhost": false, "host:1-2": false, "host:http": false, "udp/host:1": false, "http://127.0.0.1:9000": false, "unix/": false, "h:0": false,
	} {
		if _, err := validate(App{Executable: []string{"x"}, ReverseProxyTo: dial}); (err == nil) != ok {
			t.Errorf("%q: err = %v, want ok=%v", dial, err, ok)
		}
	}
}

// TestExpansionSelectsTheApp verifies equal expansions share a supervisor,
// different ones do not, an argument with a space stays one argument, an
// empty placeholder fails before anything runs, and unknown ones (a shell's
// ${PORT}) are left as written.
func TestExpansionSelectsTheApp(t *testing.T) {
	c := &ReverseBin{DynamicProxyDetector: []string{"detect", "{app}"}}
	provision(t, c)
	hold := func(app any) (*supervisor, error) { return c.hold(request(map[string]any{"app": app})) }
	a1, _ := hold("a b")
	a2, _ := hold("a b")
	b, _ := hold("b")
	if a1 != a2 || a1 == b || !reflect.DeepEqual(a1.detector, []string{"detect", "a b"}) {
		t.Fatalf("supervisors a=%p a=%p b=%p, detector %q", a1, a2, b, a1.detector)
	}
	if _, err := hold(""); err == nil {
		t.Fatal("empty placeholder: want error")
	}
	c.DynamicProxyDetector = []string{"sh", "-c", "exec server --port ${PORT} {nope}"}
	unknown, err := hold("x")
	if err != nil || unknown.detector[2] != c.DynamicProxyDetector[2] {
		t.Fatalf("unknown placeholders: %v, %q; want them left as written", err, unknown.detector)
	}
	c.release(unknown)
	for _, s := range []*supervisor{a1, a2, b} {
		c.release(s)
	}
	c.wg.Wait()
}

// TestAppExitIsReapedWithoutARequest verifies requests share the running
// app, and that an app that exits is noticed at once: its supervisor retires
// with no request, and the next request gets a new app.
func TestAppExitIsReapedWithoutARequest(t *testing.T) {
	c := testApp(t)
	provision(t, c)
	first := use(t, c, "/pid")
	if again := use(t, c, "/exit"); again != first {
		t.Fatalf("pids %q then %q, want the same app", first, again)
	}
	c.wg.Wait()
	if alive(first) || len(c.supervisors) != 0 {
		t.Fatalf("pid %s alive=%v, %d supervisors; want reaped and retired", first, alive(first), len(c.supervisors))
	}
	if second := use(t, c, "/pid"); second == "" || second == first {
		t.Fatalf("pid after exit = %q, want a new app (was %s)", second, first)
	}
}

// TestAppExitTakesItsProcessGroup verifies descendants do not outlive the
// app, and that the app counts as exited only once its whole group is gone,
// even when Caddy inherits the orphans (PID 1 or a subreaper).
func TestAppExitTakesItsProcessGroup(t *testing.T) {
	hold, dead := testapp.HoldListener(t)
	c := testApp(t, "TEST_APP_CHILD="+hold)
	provision(t, c)
	pid, _ := strconv.Atoi(use(t, c, "/exit"))
	if err := dead(10 * time.Second); !errors.Is(err, io.EOF) {
		t.Fatalf("child read = %v, want EOF because the child was killed", err)
	}
	// The orphaned child's zombie is ours (see becomeSubreaper); the app
	// counts as exited, and its supervisor retires, only once it is reaped.
	retired := make(chan struct{})
	go func() { c.wg.Wait(); close(retired) }()
	select {
	case <-retired:
	case <-time.After(10 * time.Second):
		t.Fatal("app never counted as exited: its group's zombie was not reaped")
	}
	if err := syscall.Kill(-pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill(-%d, 0) = %v once exited, want ESRCH: no member left", pid, err)
	}
}

// TestIdleStop verifies an open request keeps the app past idle_timeout_ms,
// and that the app stops idle_timeout_ms after the request ends.
func TestIdleStop(t *testing.T) {
	c := testApp(t)
	c.IdleTimeoutMS = 50
	provision(t, c)
	s, _ := c.hold(request(nil))
	dial, err := s.upstream(t.Context(), false, "")
	before := get(dial, "/pid")
	time.Sleep(200 * time.Millisecond) // 4 idle timeouts
	if after := get(dial, "/pid"); err != nil || before == "" || before != after {
		t.Fatalf("pid %q then %q (%v), want the same app while held", before, after, err)
	}
	c.release(s)
	c.wg.Wait()
	if alive(before) {
		t.Fatalf("pid %s still exists after idle stop", before)
	}
}

// TestShutdown verifies Cleanup returns only once an app that ignores
// SIGTERM was killed, and that no request can start an app afterwards.
func TestShutdown(t *testing.T) {
	c := testApp(t, "TEST_APP_IGNORE_TERM=1")
	cleanup := provision(t, c)
	pid := use(t, c, "/pid")
	cleanup()
	if alive(pid) {
		t.Fatalf("pid %s alive after Cleanup", pid)
	}
	if _, err := c.hold(request(nil)); err == nil {
		t.Fatal("hold after shutdown succeeded")
	}
}

// startErr runs one start of c and returns its error, checking that the
// failed start leaves no supervisor behind.
func startErr(t *testing.T, c *ReverseBin) error {
	t.Helper()
	s, err := c.hold(request(nil))
	if err == nil {
		_, err = s.upstream(t.Context(), false, "example.test")
		c.release(s)
	}
	c.wg.Wait()
	if len(c.supervisors) != 0 {
		t.Fatalf("%d supervisors left after a failed start", len(c.supervisors))
	}
	return err
}

// TestFailedStarts verifies starts that cannot succeed fail, with a reason
// and within health_timeout_ms: the app exits, the address already answers, the
// detector's output has an unknown field, the app never listens.
func TestFailedStarts(t *testing.T) {
	squat, err := net.Listen("unix", filepath.Join(t.TempDir(), "squat.sock")) // not an app of ours
	if err != nil {
		t.Fatal(err)
	}
	defer squat.Close()
	exits, squatted, never := testApp(t), testApp(t), testApp(t)
	exits.Executable = []string{"sh", "-c", "exit 3"}
	squatted.ReverseProxyTo = "unix/" + squat.Addr().String()
	never.Executable, never.HealthTimeoutMS = []string{"sleep", "60"}, 200
	detector := &ReverseBin{DynamicProxyDetector: []string{"cat", writeFile(t, `{"executable":["x"],"reverse_proxy_to":":1","bogus":1}`)}}
	for want, c := range map[string]*ReverseBin{"exited before": exits, "already accepts": squatted,
		"unknown field": detector, "deadline exceeded": never} {
		provision(t, c)
		if err := startErr(t, c); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}

// TestHealthCheck verifies health_check readiness: any response is ready by
// default (401, 503); a health_check status narrows it, reporting the last status at
// the deadline; a redirect is not followed (its target answers 503). The
// probe is HEAD unless the method says otherwise, with the request's Host
// and none of its headers (the app answers 400 to any other "<method> <host>"
// and to the request's X-Leak header).
func TestHealthCheck(t *testing.T) {
	for _, tc := range []struct {
		answer, method, sees string
		want                 int
		err                  string
	}{
		{"401", "", "", 0, ""}, {"503", "", "", 0, ""}, {"503", "", "", 2, "HEAD /health: status 503, want 2"}, {"302", "", "", 3, ""},
		{"200", "", "HEAD example.test", 2, ""}, {"200", "GET", "GET example.test", 2, ""}, {"200", "", "GET example.test", 2, "status 400"},
	} {
		c := testApp(t, "TEST_APP_HEALTH="+tc.answer, "TEST_APP_HEALTH_WANT="+tc.sees)
		c.HealthPath, c.HealthMethod, c.HealthStatus, c.HealthTimeoutMS = "/health", tc.method, tc.want, 1000
		provision(t, c)
		if tc.err == "" && use(t, c, "/pid") == "" || tc.err != "" && !strings.Contains(fmt.Sprint(startErr(t, c)), tc.err) {
			t.Errorf("%+v: want ready=%v", tc, tc.err == "")
		}
	}
}

// TestRetry verifies the proxy's retry call: it reuses the request's lease,
// so a failed start is not run again; and it rechecks the app, replacing one
// that is alive but no longer listening and keeping one that listens or is
// merely slow.
func TestRetry(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	failing := &ReverseBin{DynamicProxyDetector: []string{"sh", "-c", `echo run >> "$0"; exit 1`, runs}}
	provision(t, failing)
	r := request(nil)
	_, err1 := failing.GetUpstreams(r)
	_, err2 := failing.GetUpstreams(r) // the retry
	if out, _ := os.ReadFile(runs); err1 == nil || err2 != err1 || string(out) != "run\n" {
		t.Fatalf("errors %v, %v; detector runs %q; want one failed start", err1, err2, out)
	}
	c := testApp(t)
	provision(t, c)
	r = request(nil)
	pid := func() string {
		ups, err := c.GetUpstreams(r)
		if err != nil {
			t.Fatal(err)
		}
		return get(ups[0].Dial, "/pid")
	}
	first := pid()
	if kept := pid(); kept != first {
		t.Fatalf("retry replaced a listening app: %s then %s", first, kept)
	}
	get(c.ReverseProxyTo, "/close")
	if replaced := pid(); replaced == "" || replaced == first {
		t.Fatalf("retry after the app stopped listening got %q, want a new app (was %s)", replaced, first)
	}
	// Only "nobody listens" replaces an app; a timeout may be a full backlog.
	stale, _ := net.Listen("unix", filepath.Join(t.TempDir(), "stale.sock"))
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	_, missing := net.Dial("unix", filepath.Join(t.TempDir(), "none.sock"))
	_, refused := net.Dial("unix", stale.Addr().String())
	if !notListening(missing) || !notListening(refused) || notListening(context.DeadlineExceeded) || notListening(os.ErrDeadlineExceeded) {
		t.Fatalf("notListening: missing %v, refused %v, want true; timeouts want false", missing, refused)
	}
}

// TestEnvironmentIsExactlyEnv verifies the app sees env and nothing of Caddy's.
func TestEnvironmentIsExactlyEnv(t *testing.T) {
	t.Setenv("TEST_CADDY_ONLY", "1")
	c := testApp(t, "A=1")
	provision(t, c)
	got := strings.Split(use(t, c, "/env"), "\n")
	if slices.Sort(got); !slices.Equal(got, []string{"A=1", "TEST_APP_DIAL=" + c.ReverseProxyTo}) {
		t.Fatalf("app env = %q, want A and TEST_APP_DIAL only", got)
	}
}

// TestNinthStartWaits verifies starts beyond maxConcurrentStarts wait for a
// slot instead of running: the detector does not run while all slots are taken.
func TestNinthStartWaits(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	app := testApp(t)
	out, _ := json.Marshal(app.App)
	c := &ReverseBin{DynamicProxyDetector: []string{"sh", "-c", `touch "$0"; cat "$1"`, marker, writeFile(t, string(out))}}
	provision(t, c)
	for range cap(c.starts) {
		c.starts <- struct{}{} // eight starts of other apps in flight
	}
	s, _ := c.hold(request(nil))
	done := make(chan error)
	go func() { _, err := s.upstream(t.Context(), false, ""); done <- err }()
	time.Sleep(200 * time.Millisecond) // ample time for an unblocked detector
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("detector ran while all start slots were taken")
	}
	<-c.starts // one slot frees up; the queued start proceeds
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.release(s)
}

// TestClaim verifies the single-instance registry: a claim takes all its ids
// or none, a waiter proceeds once the holder releases, and a waiter whose
// context ends gets an error naming the busy id.
func TestClaim(t *testing.T) {
	releaseA, _ := claim(t.Context(), "test:a")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := claim(ctx, "test:b", "test:a"); err == nil || !strings.Contains(err.Error(), "another copy of test:a") {
		t.Fatalf("claim of a busy id: err = %v", err)
	}
	releaseB, err := claim(t.Context(), "test:b") // not taken by the failed claim
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func())
	go func() { release, _ := claim(t.Context(), "test:a", "test:c"); got <- release }()
	releaseA()
	if release := <-got; release == nil {
		t.Fatal("waiter got no claim after the holder released")
	} else {
		release()
	}
	releaseB()
}

// TestSingleInstance verifies at most one copy of an app runs across two
// module instances (as during a config reload), whether they share dir or
// dial: the second start fails at its deadline while the first app runs,
// and succeeds once the first has exited.
func TestSingleInstance(t *testing.T) {
	check := func(t *testing.T, id string, first, second *ReverseBin) {
		provision(t, first)
		second.HealthTimeoutMS = 300
		provision(t, second)
		pid := use(t, first, "/pid")
		if err := startErr(t, second); err == nil || !strings.Contains(err.Error(), "another copy of "+id+":") {
			t.Fatalf("second start err = %v, want another copy of %s", err, id)
		}
		use(t, first, "/exit")
		first.wg.Wait()
		if again := use(t, second, "/pid"); again == "" || again == pid {
			t.Fatalf("start after the first exited got %q (first %s)", again, pid)
		}
	}
	t.Run("dir", func(t *testing.T) {
		first, second := testApp(t), testApp(t)
		first.WorkingDirectory = t.TempDir()
		second.WorkingDirectory = first.WorkingDirectory
		check(t, "dir", first, second)
	})
	t.Run("dial", func(t *testing.T) {
		first, second := testApp(t), testApp(t)
		second.ReverseProxyTo, second.Envs = first.ReverseProxyTo, first.Envs
		check(t, "dial", first, second)
	})
}
