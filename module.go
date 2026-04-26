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
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(&ReverseBin{})
	// RegisterHandlerDirective associates the "reverse-bin" directive in the Caddyfile
	// with the parseCaddyfile function to create a reverse-bin handler instance.
	httpcaddyfile.RegisterHandlerDirective("reverse-bin", parseCaddyfile)
	// RegisterDirectiveOrder ensures the "reverse-bin" handler is executed before the
	// "respond" handler in the HTTP middleware chain. This makes the "order"
	// block in the Caddyfile redundant.
	httpcaddyfile.RegisterDirectiveOrder("reverse-bin", httpcaddyfile.Before, "respond")
}

// ReverseBin supervises executable backends and proxies HTTP traffic to them.
type ReverseBin struct {
	// Name of executable script or binary and its arguments
	Executable []string `json:"executable"`
	// Working directory (default, current Caddy working directory)
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	// Environment key value pairs (key=value) for this particular app
	Envs []string `json:"envs,omitempty"`
	// Environment keys to pass through for all apps
	PassEnvs []string `json:"passEnvs,omitempty"`
	// True to pass all environment variables to the executable
	PassAll bool `json:"passAllEnvs,omitempty"`

	// Address to proxy to (for proxy mode)
	ReverseProxyTo string `json:"reverse_proxy_to,omitempty"`
	// Readiness check method (GET or HEAD)
	ReadinessMethod string `json:"readinessMethod,omitempty"`
	// Readiness check path
	ReadinessPath string `json:"readinessPath,omitempty"`
	// Binary and arguments to run to determine proxy parameters dynamically
	DynamicProxyDetector []string `json:"dynamic_proxy_detector,omitempty"`
	// Idle timeout in milliseconds before stopping backend process after last request
	IdleTimeoutMS int `json:"idleTimeoutMs,omitempty"`

	// Internal state for proxy mode
	processes map[string]*processState
	mu        sync.Mutex

	reverseProxy *reverseproxy.Handler
	ctx          caddy.Context

	logger *zap.Logger
}

type processState struct {
	key      string
	requests chan supervisorRequest
	commands chan supervisorCommand
}

func isUnixUpstream(addr string) bool {
	return strings.HasPrefix(addr, "unix/")
}

func readinessConfigured(method, path string) bool {
	return strings.TrimSpace(method) != "" && strings.TrimSpace(path) != ""
}

// Interface guards
var (
	_ caddyhttp.MiddlewareHandler = (*ReverseBin)(nil)
	_ caddyfile.Unmarshaler       = (*ReverseBin)(nil)
	_ caddy.Provisioner           = (*ReverseBin)(nil)
	_ caddy.CleanerUpper          = (*ReverseBin)(nil)
)

func (c *ReverseBin) CaddyModule() caddy.ModuleInfo {
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
				c.Executable = d.RemainingArgs()
				if len(c.Executable) < 1 {
					return d.Err("an executable needs to be specified")
				}
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
				args := d.RemainingArgs()
				if len(args) == 1 && strings.EqualFold(args[0], "null") {
					c.ReadinessMethod = ""
					c.ReadinessPath = ""
					continue
				}
				if len(args) != 2 {
					return d.ArgErr()
				}
				c.ReadinessMethod = strings.ToUpper(args[0])
				c.ReadinessPath = args[1]
			case "dynamic_proxy_detector":
				c.DynamicProxyDetector = d.RemainingArgs()
				if len(c.DynamicProxyDetector) == 0 {
					return d.ArgErr()
				}
			case "idle_timeout_ms":
				if !d.NextArg() {
					return d.ArgErr()
				}
				v, err := strconv.Atoi(d.Val())
				if err != nil || v <= 0 {
					return d.Err("idle_timeout_ms must be a positive integer")
				}
				c.IdleTimeoutMS = v
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

	c.logger.Info("reverse-bin module provisioned",
		zap.String("version", Version),
		zap.String("commit", Commit),
		zap.String("build_date", BuildDate))

	if len(c.DynamicProxyDetector) == 0 {
		if len(c.Executable) == 0 {
			return fmt.Errorf("exec (executable) is required when dynamic_proxy_detector is not set")
		}

		if c.ReverseProxyTo == "" {
			return fmt.Errorf("reverse_proxy_to is required when dynamic_proxy_detector is not set")
		}
	}

	if c.ReadinessMethod != "" {
		c.ReadinessMethod = strings.ToUpper(c.ReadinessMethod)
	}
	if c.IdleTimeoutMS <= 0 {
		c.IdleTimeoutMS = 5000
	}

	if !isUnixUpstream(c.ReverseProxyTo) && c.ReverseProxyTo != "" && !readinessConfigured(c.ReadinessMethod, c.ReadinessPath) {
		return fmt.Errorf("readiness_check is required for non-unix reverse_proxy_to targets")
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
		ps = &processState{
			key:      key,
			requests: make(chan supervisorRequest),
			commands: make(chan supervisorCommand),
		}
		c.processes[key] = ps
		go c.runSupervisor(ps)
	}
	return ps
}

func (c *ReverseBin) Cleanup() error {
	c.mu.Lock()
	states := make([]*processState, 0, len(c.processes))
	for _, ps := range c.processes {
		states = append(states, ps)
	}
	c.mu.Unlock()

	var firstErr error
	for _, ps := range states {
		if err := c.sendSupervisorCommand(ps, supervisorShutdown, "cleanup"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	c := new(ReverseBin)
	err := c.UnmarshalCaddyfile(h.Dispenser)
	return c, err
}
