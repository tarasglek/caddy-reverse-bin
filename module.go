/*
 * Copyright (c) 2020 Andreas Schneider
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package reversebin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(ReverseBin{})
	// RegisterHandlerDirective associates the "reverse-bin" directive in the Caddyfile
	// with the parseCaddyfile function to create a CGI handler instance.
	httpcaddyfile.RegisterHandlerDirective("reverse-bin", parseCaddyfile)
	// RegisterDirectiveOrder ensures the "reverse-bin" handler is executed before the
	// "respond" handler in the HTTP middleware chain. This makes the "order"
	// block in the Caddyfile redundant.
	httpcaddyfile.RegisterDirectiveOrder("reverse-bin", httpcaddyfile.Before, "respond")
}

// ReverseBin implements a CGI handler that executes binary files following the
// CGI protocol, passing parameters via environment variables and evaluating
// the response as the HTTP response.
type ReverseBin struct {
	// Name of executable script or binary
	Executable string `json:"executable"`
	// Working directory (default, current Caddy working directory)
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	// Arguments to submit to executable
	Args []string `json:"args,omitempty"`
	// Environment key value pairs (key=value) for this particular app
	Envs []string `json:"envs,omitempty"`
	// Environment keys to pass through for all apps
	PassEnvs []string `json:"passEnvs,omitempty"`
	// True to pass all environment variables to CGI executable
	PassAll bool `json:"passAllEnvs,omitempty"`

	// Address to proxy to (for proxy mode)
	ReverseProxyTo string `json:"reverse_proxy_to,omitempty"`
	// Readiness check method (GET or HEAD)
	ReadinessMethod string `json:"readinessMethod,omitempty"`
	// Readiness check path
	ReadinessPath string `json:"readinessPath,omitempty"`
	// Binary and arguments to run to determine proxy parameters dynamically
	DynamicProxyDetector []string `json:"dynamic_proxy_detector,omitempty"`

	// Internal state for proxy mode
	processes map[string]*processState
	mu        sync.Mutex

	reverseProxy *reverseproxy.Handler
	ctx          caddy.Context

	logger *zap.Logger
}

type processState struct {
	process        *os.Process
	cancel         context.CancelFunc
	activeRequests int64
	idleTimer      *time.Timer
	terminationMsg string
	overrides      *proxyOverrides
	mu             sync.Mutex
}

// Interface guards
var (
	_ caddyhttp.MiddlewareHandler = (*ReverseBin)(nil)
	_ caddyfile.Unmarshaler       = (*ReverseBin)(nil)
	_ caddy.Provisioner           = (*ReverseBin)(nil)
	_ caddy.CleanerUpper          = (*ReverseBin)(nil)
)

func (c ReverseBin) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.reverse-bin",
		New: func() caddy.Module { return &ReverseBin{} },
	}
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler; it parses the
// reverse-bin directive and its subdirectives from the Caddyfile.
func (c *ReverseBin) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// Consume 'em all. Matchers should be used to differentiate multiple instantiations.
	// If they are not used, we simply combine them first-to-last.
	for d.Next() {
		d.RemainingArgs() // consume matcher if present
		for d.NextBlock(0) {
			switch d.Val() {
			case "exec":
				args := d.RemainingArgs()
				if len(args) < 1 {
					return d.Err("an executable needs to be specified")
				}
				c.Executable = args[0]
				c.Args = args[1:]
			case "dir":
				if !d.Args(&c.WorkingDirectory) {
					return d.ArgErr()
				}
			case "env":
				c.Envs = d.RemainingArgs()
				if len(c.Envs) == 0 {
					return d.ArgErr()
				}
			case "pass_env":
				c.PassEnvs = d.RemainingArgs()
				if len(c.PassEnvs) == 0 {
					return d.ArgErr()
				}
			case "pass_all_env":
				c.PassAll = true
			case "reverse_proxy_to":
				if !d.Args(&c.ReverseProxyTo) {
					return d.ArgErr()
				}
			case "readiness_check":
				if !d.Args(&c.ReadinessMethod, &c.ReadinessPath) {
					return d.ArgErr()
				}
				c.ReadinessMethod = strings.ToUpper(c.ReadinessMethod)
			case "dynamic_proxy_detector":
				c.DynamicProxyDetector = d.RemainingArgs()
				if len(c.DynamicProxyDetector) == 0 {
					return d.ArgErr()
				}
			default:
				return d.Errf("unknown subdirective: %q", d.Val())
			}
		}
	}
	return nil
}

// Provision implements caddy.Provisioner; it sets up the module's
// internal state and provisions the underlying reverse proxy handler.
func (c *ReverseBin) Provision(ctx caddy.Context) error {
	c.ctx = ctx
	c.logger = ctx.Logger(c)
	c.processes = make(map[string]*processState)

	if len(c.DynamicProxyDetector) == 0 {
		if c.Executable == "" {
			return fmt.Errorf("exec (executable) is required when dynamic_proxy_detector is not set")
		}

		if c.ReverseProxyTo == "" {
			return fmt.Errorf("reverse_proxy_to is required when dynamic_proxy_detector is not set")
		}
	}

	if c.ReadinessMethod == "" {
		c.ReadinessMethod = "GET"
	}

	rp := &reverseproxy.Handler{
		DynamicUpstreams: c,
	}
	if err := rp.Provision(ctx); err != nil {
		return fmt.Errorf("failed to provision reverse proxy: %v", err)
	}
	c.reverseProxy = rp

	return nil
}

// Cleanup implements caddy.CleanerUpper; it ensures that any running
// backend process is terminated when the module is unloaded.
func (c *ReverseBin) getOrCreateProcessState(key string) *processState {
	c.mu.Lock()
	defer c.mu.Unlock()
	ps, ok := c.processes[key]
	if !ok {
		c.logger.Debug("creating new process state", zap.String("key", key))
		ps = &processState{}
		c.processes[key] = ps
	}
	return ps
}

func (ps *processState) incrementRequests(logger *zap.Logger, key string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.activeRequests++
	logger.Debug("incremented active requests",
		zap.String("key", key),
		zap.Int64("count", ps.activeRequests),
		zap.Bool("timer_stopped", ps.idleTimer != nil))
	if ps.idleTimer != nil {
		ps.idleTimer.Stop()
		ps.idleTimer = nil
	}
}

func (ps *processState) decrementRequests(logger *zap.Logger, key string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.activeRequests--
	logger.Debug("decremented active requests", zap.String("key", key), zap.Int64("count", ps.activeRequests))

	if ps.activeRequests == 0 {
		logger.Debug("starting idle timer", zap.String("key", key), zap.Duration("duration", 30*time.Second))
		ps.idleTimer = time.AfterFunc(30*time.Second, func() {
			ps.mu.Lock()
			defer ps.mu.Unlock()
			if ps.activeRequests == 0 && ps.process != nil {
				logger.Info("idle timer fired, terminating process", zap.String("key", key), zap.Int("pid", ps.process.Pid))
				ps.terminationMsg = "idle timeout"
				if ps.cancel != nil {
					ps.cancel()
				}
				ps.process = nil
			} else {
				logger.Debug("idle timer fired but process active or already gone",
					zap.String("key", key),
					zap.Int64("active_requests", ps.activeRequests),
					zap.Bool("process_nil", ps.process == nil))
			}
		})
	}
}

func (c *ReverseBin) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, ps := range c.processes {
		ps.mu.Lock()
		if ps.idleTimer != nil {
			ps.idleTimer.Stop()
			ps.idleTimer = nil
		}
		if ps.process != nil {
			c.logger.Info("cleaning up proxy subprocess", zap.Int("pid", ps.process.Pid))
			c.killProcessGroup(ps.process)
			ps.process = nil
		}
		ps.mu.Unlock()
	}

	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	c := new(ReverseBin)
	err := c.UnmarshalCaddyfile(h.Dispenser)
	return c, err
}
