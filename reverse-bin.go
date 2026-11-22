package reversebin

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

// supervisor owns one app. Only its goroutine starts or stops the app, so the
// app's lifecycle state is one local variable in run: nil or running.
type supervisor struct {
	c        *ReverseBin
	key      string
	app      App      // expanded static config
	detector []string // expanded detector command
	refs     int      // requests holding this app; guarded by c.mu
	asks     chan ask
	released chan struct{} // refs dropped to zero
}

type ask struct {
	recheck bool   // the proxy is retrying: the address may have failed
	host    string // the request's Host, which the health probe sends too
	reply   chan<- answer
}

type answer struct {
	dial string
	err  error
}

// upstream returns the address of the running app, starting it if needed.
// The caller must hold the supervisor, so run is alive to answer. Once the ask
// is taken the reply is awaited even if ctx ends: at most a stop, a start and
// a stop, after which the goroutine returns.
func (s *supervisor) upstream(ctx context.Context, recheck bool, host string) (string, error) {
	reply := make(chan answer, 1)
	select {
	case s.asks <- ask{recheck, host, reply}:
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case <-s.c.ctx.Done():
		return "", context.Cause(s.c.ctx)
	}
	a := <-reply
	return a.dial, a.err
}

func (s *supervisor) run() {
	c, idleTimeout := s.c, time.Duration(s.c.IdleTimeoutMS)*time.Millisecond
	var app *app // nil: stopped
	idle := time.NewTimer(idleTimeout)
	idle.Stop() // armed only by a release, so it never fires for an old one
	defer idle.Stop()
	for {
		var exited <-chan struct{}
		if app != nil {
			exited = app.exited
		}
		select {
		case ask := <-s.asks:
			idle.Stop() // a request is here; Go >= 1.23 drops any stale fire
			if ask.recheck && app != nil {
				if conn, err := app.spec.connect(c.ctx); err == nil {
					_ = conn.Close()
				} else if notListening(err) { // not just slow, as under load
					app.stop()
					app = nil
				}
			}
			var err error
			if app == nil {
				if app, err = s.start(ask.host); err != nil { // Caddy logs it
					ask.reply <- answer{err: err}
					break
				}
			}
			ask.reply <- answer{dial: app.spec.ReverseProxyTo}
		case <-s.released:
			if c.unheld(s, false) { // not a release that a newer request overtook
				idle.Reset(idleTimeout)
			}
		case <-idle.C:
			if app != nil && c.unheld(s, false) {
				app.stop()
				app = nil
			}
		case <-exited:
			app = nil
		case <-c.ctx.Done():
			if app != nil {
				app.stop()
			}
			return
		}
		if app == nil && c.unheld(s, true) {
			return
		}
	}
}

// appSpec is a validated App with its parsed address.
type appSpec struct {
	App
	network, address string
	ids              []string // identities of the app, for claim
}

func (spec appSpec) connect(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return new(net.Dialer).DialContext(ctx, spec.network, spec.address)
}

// notListening reports whether a connect error means nobody listens at all,
// as opposed to a slow or overloaded listener.
func notListening(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist)
}

func validate(a App) (appSpec, error) {
	// Caddy's parser, because the proxy parses the same string to dial it.
	na, err := caddy.ParseNetworkAddress(a.ReverseProxyTo)
	switch {
	case len(a.Executable) == 0:
		return appSpec{}, fmt.Errorf("exec is empty")
	case a.HealthPath != "" && a.HealthPath[0] != '/':
		return appSpec{}, fmt.Errorf("health_path %q: want a path starting with /", a.HealthPath)
	case a.HealthMethod != "" && a.HealthPath == "":
		return appSpec{}, fmt.Errorf("health_method %q needs health_path", a.HealthMethod)
	case a.HealthStatus < 0 || a.HealthStatus != 0 && (a.HealthPath == "" || a.HealthStatus > 5 && (a.HealthStatus < 100 || a.HealthStatus > 599)):
		return appSpec{}, fmt.Errorf("health_status %d: want 1-5 or 100-599, with health_path", a.HealthStatus)
	case strings.ContainsAny(a.ReverseProxyTo, "{}"):
		// Caddy expands placeholders in the address we return, so it would
		// dial something other than what was checked and probed.
		return appSpec{}, fmt.Errorf("reverse_proxy_to %q: braces are not allowed", a.ReverseProxyTo)
	case err != nil || !(na.Network == "unix" && na.Host != "" ||
		na.Network == "tcp" && na.PortRangeSize() == 1 && na.StartPort != 0):
		return appSpec{}, fmt.Errorf("reverse_proxy_to %q: want unix/<path> or host:port", a.ReverseProxyTo)
	}
	spec := appSpec{a, na.Network, na.JoinHostPort(0), []string{"dial:" + na.Network + "/" + na.JoinHostPort(0)}}
	if a.WorkingDirectory != "" { // the hosting detector picks a new port per detection: dial alone is not the app
		dir, err := filepath.Abs(a.WorkingDirectory)
		if err == nil {
			dir, err = filepath.EvalSymlinks(dir)
		}
		if err != nil {
			return appSpec{}, fmt.Errorf("dir: %w", err)
		}
		spec.ids = append(spec.ids, "dir:"+dir)
	}
	return spec, nil
}

// slot takes one of the module's start slots until free is called.
func (c *ReverseBin) slot(ctx context.Context) (free func(), err error) {
	select {
	case c.starts <- struct{}{}:
		return func() { <-c.starts }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("too many apps starting: %w", context.Cause(ctx))
	}
}

// start launches the app and waits until it is ready; on error no process is
// left. It is bound to the module, not the request: a client that goes away
// does not abort a start others may be waiting for.
func (s *supervisor) start(host string) (*app, error) {
	c := s.c
	ctx, cancel := context.WithTimeout(c.ctx, time.Duration(c.HealthTimeoutMS)*time.Millisecond)
	defer cancel()
	free, err := c.slot(ctx)
	if err != nil {
		return nil, err
	}
	a := s.app
	if len(s.detector) > 0 {
		a, err = c.detect(ctx, s.detector)
	}
	free()
	if err != nil {
		return nil, err
	}
	spec, err := validate(a)
	if err != nil {
		return nil, err
	}
	// Without a slot: waiting for another copy must not starve other starts.
	release, err := claim(ctx, spec.ids...)
	if err != nil {
		return nil, err
	}
	var app *app
	defer func() {
		if app == nil {
			release() // no process exists; once one does, its reaper releases
		}
	}()
	if free, err = c.slot(ctx); err != nil {
		return nil, err
	}
	defer free()
	// Whatever answers before launch is not our app: one outside this Caddy.
	if conn, err := spec.connect(ctx); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s already accepts connections before the app started", spec.ReverseProxyTo)
	}
	if app, err = c.launch(spec, release); err != nil {
		return nil, err
	}
	if err := app.waitReady(ctx, host); err != nil {
		app.stop()
		return nil, err // app != nil: the claim stays until the group is gone
	}
	return app, nil
}

// claims makes "at most one copy of an app" hold across keys, blocks and
// module instances (config reloads) in this process.
var claims = struct {
	sync.Mutex
	held map[string]chan struct{} // identity -> closed on release
}{held: map[string]chan struct{}{}}

// claim reserves every id or none, waiting for current holders until ctx
// ends. release frees them; it is idempotent.
func claim(ctx context.Context, ids ...string) (release func(), err error) {
	for {
		claims.Lock()
		var busy chan struct{}
		var busyID string
		for _, id := range ids {
			if ch, ok := claims.held[id]; ok {
				busy, busyID = ch, id
			}
		}
		if busy == nil {
			done := make(chan struct{})
			for _, id := range ids {
				claims.held[id] = done
			}
			claims.Unlock()
			return sync.OnceFunc(func() {
				claims.Lock()
				for _, id := range ids {
					delete(claims.held, id)
				}
				claims.Unlock()
				close(done)
			}), nil
		}
		claims.Unlock()
		select {
		case <-busy:
		case <-ctx.Done():
			return nil, fmt.Errorf("another copy of %s is still running: %w", busyID, context.Cause(ctx))
		}
	}
}

// capped keeps the first 32 KiB written to it and drops the rest.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := 32<<10 - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// detect runs the detector, with Caddy's environment, and decodes its output.
func (c *ReverseBin) detect(ctx context.Context, argv []string) (App, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = killWait
	var stderr capped
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return App{}, err
	}
	if err := cmd.Start(); err != nil {
		return App{}, fmt.Errorf("detector: %w", err)
	}
	// Bounded: a runaway detector must not exhaust Caddy's memory.
	out, _ := io.ReadAll(io.LimitReader(stdout, maxDetectorOutput+1))
	if len(out) > maxDetectorOutput {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		return App{}, fmt.Errorf("detector output over %d bytes", maxDetectorOutput)
	}
	if err := cmd.Wait(); err != nil {
		return App{}, fmt.Errorf("detector: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var a App
	d := json.NewDecoder(bytes.NewReader(out))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return App{}, fmt.Errorf("detector output: %w", err)
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return App{}, errors.New("detector output: data after the JSON object")
	}
	return a, nil
}

var errExited = errors.New("app exited before it was ready")

// app is a launched process group. It is running until exited is closed.
type app struct {
	spec   appSpec
	pid    int
	exited chan struct{} // closed once the whole process group is gone
	logger *zap.Logger
}

// launch starts the app in its own process group with exactly spec.Envs.
// The reaper calls release once the process group is gone.
func (c *ReverseBin) launch(spec appSpec, release func()) (*app, error) {
	cmd := exec.Command(spec.Executable[0], spec.Executable[1:]...)
	cmd.Dir = spec.WorkingDirectory
	cmd.Env = append([]string{}, spec.Envs...) // nil Env would inherit Caddy's
	// Its own group, to signal it as a unit. No Pdeathsig: Cleanup stops apps
	// on a normal shutdown; surviving a hard kill of Caddy is the launcher's
	// job (systemd's cgroup, or setpriv --pdeathsig KILL -- unshare
	// --kill-child), as the kernel ties Pdeathsig to the forking thread.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// The child gets the pipe itself, so Wait returns when the leader exits
	// even if a descendant still holds the pipe.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = w, w
	err = cmd.Start()
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	a := &app{spec: spec, pid: cmd.Process.Pid, exited: make(chan struct{}),
		logger: c.logger.With(zap.Int("pid", cmd.Process.Pid), zap.String("dial", spec.ReverseProxyTo))}
	a.logger.Info("app started")
	go func() { // bounded lines: a long line arrives in pieces instead of stalling the app
		defer r.Close()
		for lines := bufio.NewReaderSize(r, 64<<10); ; {
			line, _, err := lines.ReadLine()
			if err != nil {
				return
			}
			a.logger.Info("app output", zap.ByteString("line", line))
		}
	}()
	go func() {
		err := cmd.Wait()
		_ = syscall.Kill(-a.pid, syscall.SIGKILL) // the group dies with its leader
		// Another copy may start only once no member is left. Zombies count;
		// reap those reparented to us (Caddy as PID 1 or a subreaper). Only
		// this group's: every other child has its own.
		for !errors.Is(syscall.Kill(-a.pid, 0), syscall.ESRCH) {
			_, _ = syscall.Wait4(-a.pid, nil, syscall.WNOHANG, nil)
			time.Sleep(probeInterval)
		}
		a.logger.Info("app exited", zap.Error(err))
		release()
		close(a.exited)
	}()
	return a, nil
}

// waitExit reports whether the app exits within d.
func (a *app) waitExit(d time.Duration) bool {
	select {
	case <-a.exited:
		return true
	case <-time.After(d):
		return false
	}
}

// stop sends SIGTERM, then SIGKILL after stopGrace. A process that survives
// SIGKILL for killWait is abandoned; nothing more can be done.
func (a *app) stop() {
	if a.waitExit(0) {
		return
	}
	a.logger.Info("stopping app")
	_ = syscall.Kill(-a.pid, syscall.SIGTERM)
	if a.waitExit(stopGrace) {
		return
	}
	_ = syscall.Kill(-a.pid, syscall.SIGKILL)
	if !a.waitExit(killWait) {
		a.logger.Error("app survived SIGKILL; abandoned")
	}
}

// waitReady probes until the app is ready, exits, or ctx ends.
func (a *app) waitReady(ctx context.Context, host string) error {
	client := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return a.spec.connect(ctx) }},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var last error // newest probe failure not caused by ctx ending
	for {
		err := a.probe(ctx, client, host)
		if err == nil && a.waitExit(0) {
			err = errExited // whoever answered, it was not our app
		}
		if err == nil {
			a.logger.Info("app ready")
			return nil
		}
		if ctx.Err() == nil || last == nil {
			last = err
		}
		select {
		case <-a.exited:
			return errExited
		case <-ctx.Done():
			return fmt.Errorf("app not ready: %w: %w", context.Cause(ctx), last)
		case <-time.After(probeInterval):
		}
	}
}

// probe connects; with health_path it sends health_method (default HEAD) with
// the waking request's Host, as real traffic will, and needs a response, with
// a matching status if health_status is set.
func (a *app) probe(ctx context.Context, client *http.Client, host string) error {
	if a.spec.HealthPath == "" {
		conn, err := a.spec.connect(ctx)
		if err == nil {
			err = conn.Close()
		}
		return err
	}
	method := cmp.Or(a.spec.HealthMethod, http.MethodHead)
	req, err := http.NewRequestWithContext(ctx, method, "http://reverse-bin"+a.spec.HealthPath, nil)
	if err != nil {
		return err
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close() // the body is not read
	if a.spec.HealthStatus != 0 && !caddyhttp.StatusCodeMatches(resp.StatusCode, a.spec.HealthStatus) {
		return fmt.Errorf("%s %s: status %d, want %d", method, a.spec.HealthPath, resp.StatusCode, a.spec.HealthStatus)
	}
	return nil
}
