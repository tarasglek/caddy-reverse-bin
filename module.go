// Package reversebin is a reverse_proxy upstream source that starts an app
// on demand and stops it when idle. See README.md.
package reversebin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
)

func init() { caddy.RegisterModule(new(ReverseBin)) }

// stopGrace is the time from SIGTERM to SIGKILL: Docker's default. A
// variable only so tests can shorten it.
var stopGrace = 10 * time.Second

const (
	killWait            = time.Second // SIGKILL to giving up
	probeInterval       = 100 * time.Millisecond
	probeTimeout        = 5 * time.Second // a first response can be slow
	maxConcurrentStarts = 8               // bounds forks when many new hosts arrive at once
)

// App is what gets run. Static config and detector output are both exactly
// this, with the field names detectors already print.
type App struct {
	Executable       []string `json:"executable,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	Envs             []string `json:"envs,omitempty"` // KEY=value: the app's entire environment
	ReverseProxyTo   string   `json:"reverse_proxy_to,omitempty"`
	// Optional readiness request: HealthMethod (default HEAD) of HealthPath
	// must get a response, with a status matching HealthStatus if set (a
	// code, or a class such as 2 for 2xx).
	HealthMethod string `json:"health_method,omitempty"`
	HealthPath   string `json:"health_path,omitempty"`
	HealthStatus int    `json:"health_status,omitempty"`
}

// ReverseBin returns the address of the app a request needs, starting it if
// needed. Strings may hold placeholders; each distinct expansion is one app.
type ReverseBin struct {
	App
	DynamicProxyDetector []string `json:"dynamic_proxy_detector,omitempty"` // prints an App as JSON
	IdleTimeoutMS        int      `json:"idleTimeoutMs,omitempty"`
	HealthTimeoutMS      int      `json:"healthTimeoutMs,omitempty"` // detector, launch and readiness

	ctx         caddy.Context
	leaseKey    string // request var holding this instance's lease
	logger      *zap.Logger
	starts      chan struct{} // semaphore
	mu          sync.Mutex
	supervisors map[string]*supervisor // by key; guarded by mu
	wg          sync.WaitGroup         // supervisor goroutines
}

func (*ReverseBin) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.upstreams.reverse-bin",
		New: func() caddy.Module { return new(ReverseBin) },
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (c *ReverseBin) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // module name
	for d.NextBlock(0) {
		name, args := d.Val(), d.RemainingArgs()
		var err error
		switch n := len(args); {
		case name == "exec" && n > 0:
			c.Executable = args
		case name == "dynamic_proxy_detector" && n > 0:
			c.DynamicProxyDetector = args
		case name == "dir" && n == 1:
			c.WorkingDirectory = args[0]
		case name == "reverse_proxy_to" && n == 1:
			c.ReverseProxyTo = args[0]
		case name == "env" && n > 0:
			c.Envs = args
		case name == "health_check" && (n == 2 || n == 3):
			c.HealthMethod, c.HealthPath = args[0], args[1]
			if n == 3 {
				c.HealthStatus, err = strconv.Atoi(strings.TrimSuffix(args[2], "xx")) // 200 or 2xx
			}
		case name == "idle_timeout_ms" && n == 1:
			c.IdleTimeoutMS, err = strconv.Atoi(args[0])
		case name == "health_timeout_ms" && n == 1:
			c.HealthTimeoutMS, err = strconv.Atoi(args[0])
		default:
			return d.Errf("unknown subdirective or wrong number of arguments: %s %q", name, args)
		}
		if err != nil {
			return d.Errf("%s: %v", name, err)
		}
	}
	return nil
}

// Provision implements caddy.Provisioner.
func (c *ReverseBin) Provision(ctx caddy.Context) error {
	c.ctx, c.logger = ctx, ctx.Logger()
	c.starts, c.supervisors = make(chan struct{}, maxConcurrentStarts), map[string]*supervisor{}
	c.leaseKey = fmt.Sprintf("reverse-bin.lease.%p", c)
	c.IdleTimeoutMS = cmp.Or(c.IdleTimeoutMS, 300000)
	c.HealthTimeoutMS = cmp.Or(c.HealthTimeoutMS, 15000)
	if c.IdleTimeoutMS < 0 || c.HealthTimeoutMS < 0 {
		return errors.New("reverse-bin: idle_timeout_ms and health_timeout_ms must not be negative")
	}
	static := len(c.Executable) > 0 || c.WorkingDirectory != "" || len(c.Envs) > 0 || c.ReverseProxyTo != "" ||
		c.HealthPath != "" || c.HealthMethod != "" || c.HealthStatus != 0
	if len(c.DynamicProxyDetector) > 0 && static {
		return errors.New("reverse-bin: dynamic_proxy_detector excludes the app fields (exec, dir, env, reverse_proxy_to, health_check)")
	}
	if len(c.DynamicProxyDetector) == 0 && (len(c.Executable) == 0 || c.ReverseProxyTo == "") {
		return errors.New("reverse-bin: need exec and reverse_proxy_to, or dynamic_proxy_detector")
	}
	return nil
}

// Cleanup implements caddy.CleanerUpper. Caddy has cancelled ctx, which makes
// every supervisor stop its app; Cleanup returns once they are gone.
func (c *ReverseBin) Cleanup() error {
	c.mu.Lock()   // a hold that saw ctx live adds to wg under mu; let it finish
	c.mu.Unlock() //nolint:staticcheck // SA2001: the empty critical section is a barrier
	c.wg.Wait()
	return nil
}

// lease is a request's claim on its app, kept in the request's vars so that
// the proxy's retry reuses it.
type lease struct {
	s   *supervisor
	err error // a failed start; a retry must not run it again
}

// GetUpstreams implements reverseproxy.UpstreamSource. The request holds its
// app until the request context ends, which keeps the app from idling. Caddy
// calls again to retry; the app is then rechecked.
func (c *ReverseBin) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	l, retry := caddyhttp.GetVar(r.Context(), c.leaseKey).(*lease)
	if !retry {
		s, err := c.hold(r)
		if err != nil {
			return nil, err
		}
		context.AfterFunc(r.Context(), func() { c.release(s) })
		l = &lease{s: s}
		caddyhttp.SetVar(r.Context(), c.leaseKey, l)
	}
	if l.err != nil {
		return nil, l.err
	}
	var dial string
	if dial, l.err = l.s.upstream(r.Context(), retry, r.Host); l.err != nil {
		return nil, l.err
	}
	return []*reverseproxy.Upstream{{Dial: dial}}, nil
}

// hold expands the config for r and counts r against the supervisor of the
// resulting app, creating it if needed, until release.
func (c *ReverseBin) hold(r *http.Request) (*supervisor, error) {
	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	var err error
	expand := func(in ...string) (out []string) {
		for _, s := range in {
			// A known placeholder that expands to empty fails; unknown ones,
			// such as a shell's ${PORT}, stay as written.
			v, e := repl.ReplaceOrErr(s, true, false)
			out, err = append(out, v), errors.Join(err, e)
		}
		return out
	}
	app := App{Executable: expand(c.Executable...), WorkingDirectory: expand(c.WorkingDirectory)[0],
		Envs: expand(c.Envs...), ReverseProxyTo: expand(c.ReverseProxyTo)[0],
		HealthMethod: expand(c.HealthMethod)[0], HealthPath: expand(c.HealthPath)[0], HealthStatus: c.HealthStatus}
	detector := expand(c.DynamicProxyDetector...)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%q%q%q%q%q%q%d%q", app.Executable, app.WorkingDirectory, app.Envs, app.ReverseProxyTo,
		app.HealthMethod, app.HealthPath, app.HealthStatus, detector)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	s := c.supervisors[key]
	if s == nil {
		s = &supervisor{c: c, key: key, app: app, detector: detector,
			asks: make(chan ask), released: make(chan struct{}, 1)}
		c.supervisors[key] = s
		c.wg.Go(s.run)
	}
	s.refs++
	return s, nil
}

func (c *ReverseBin) release(s *supervisor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.refs--; s.refs == 0 {
		select { // the buffer holds one; more would say nothing new
		case s.released <- struct{}{}:
		default:
		}
	}
}

// unheld reports whether no request holds s. With retire it also forgets s,
// whose goroutine must then return.
func (c *ReverseBin) unheld(s *supervisor, retire bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.refs == 0 && retire {
		delete(c.supervisors, s.key)
	}
	return s.refs == 0
}
