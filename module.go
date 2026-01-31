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
	// Binary to run to determine proxy parameters dynamically
	DynamicProxyDetector string `json:"dynamic_proxy_detector,omitempty"`

	// Internal state for proxy mode
	process        *os.Process
	activeRequests int64
	idleTimer      *time.Timer
	terminationMsg string
	mu             sync.Mutex
	reverseProxy   *reverseproxy.Handler
	ctx            caddy.Context

	logger *zap.Logger
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
				if !d.Args(&c.DynamicProxyDetector) {
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

	if c.DynamicProxyDetector == "" {
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
func (c *ReverseBin) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}

	if c.process != nil {
		c.logger.Info("cleaning up proxy subprocess", zap.Int("pid", c.process.Pid))
		c.killProcessGroup()
		c.process = nil
	}

	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	c := new(ReverseBin)
	err := c.UnmarshalCaddyfile(h.Dispenser)
	return c, err
}
