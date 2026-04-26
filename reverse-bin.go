/*
 * Copyright (c) 2017 Kurt Jung (Gmail: kurt.w.jung)
 * Copyright (c) 2020 Andreas Schneider
 * Copyright (c) 2025 Taras Glek
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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"go.uber.org/zap"
)

const (
	defaultIdleTimeoutMS         = 300000
	defaultReadinessTimeoutMS    = 15000
	defaultTerminationGraceMS    = 5000
	defaultTerminationKillWaitMS = 1000
)

func (c *ReverseBin) readinessTimeout() time.Duration {
	return time.Duration(c.ReadinessTimeoutMS) * time.Millisecond
}

func (c *ReverseBin) terminationGrace() time.Duration {
	return time.Duration(c.TerminationGraceMS) * time.Millisecond
}

func (c *ReverseBin) terminationKillWait() time.Duration {
	return time.Duration(c.TerminationKillWaitMS) * time.Millisecond
}

// ServeHTTP implements caddyhttp.MiddlewareHandler; it handles the HTTP request
// manages idle process killing
func (c *ReverseBin) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	c.logger.Debug("ServeHTTP", zap.String("uri", r.RequestURI))
	key := c.getProcessKey(r)
	ps := c.getOrCreateProcessState(key)

	if err := c.sendSupervisorCommand(ps, supervisorRequestStarted, "request started"); err != nil {
		return err
	}
	defer func() { _ = c.sendSupervisorCommand(ps, supervisorRequestDone, "request done") }()

	if c.reverseProxy == nil {
		return fmt.Errorf("reverse proxy not initialized")
	}

	return c.reverseProxy.ServeHTTP(w, r, next)
}

func (c *ReverseBin) getProcessKey(r *http.Request) string {
	if len(c.DynamicProxyDetector) == 0 {
		return ""
	}
	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	var sb strings.Builder
	for i, arg := range c.DynamicProxyDetector {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(repl.ReplaceAll(arg, ""))
	}
	return sb.String()
}

// GetUpstreams implements reverseproxy.UpstreamSource which allows dynamic selection of backend process
// ensures process is running before returning the upstream address to the proxy.
// Note: In Caddy's reverse_proxy, GetUpstreams is called before ServeHTTP. For the very first
// request that triggers a process start, the request tracking must be initialized here
// to ensure the idle timer starts correctly after the first request completes.
func (c *ReverseBin) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	c.logger.Debug("GetUpstreams", zap.String("uri", r.RequestURI))
	key := c.getProcessKey(r)
	ps := c.getOrCreateProcessState(key)

	toAddr, err := c.getUpstreamFromSupervisor(r, ps)
	if err != nil {
		return nil, err
	}

	dialAddr, err := resolveDialAddress(toAddr)
	if err != nil {
		return nil, err
	}

	c.logger.Debug("selected upstream", zap.String("dial", dialAddr))
	return []*reverseproxy.Upstream{{Dial: dialAddr}}, nil
}

func (c *ReverseBin) getUpstreamFromSupervisor(r *http.Request, ps *processState) (string, error) {
	reply := make(chan supervisorResult, 1)
	select {
	case ps.requests <- supervisorRequest{request: r, reply: reply}:
	case <-r.Context().Done():
		return "", r.Context().Err()
	case <-c.done():
		return "", c.doneErr()
	}

	select {
	case result := <-reply:
		return result.upstream, result.err
	case <-r.Context().Done():
		return "", r.Context().Err()
	case <-c.done():
		return "", c.doneErr()
	}
}

func resolveDialAddress(toAddr string) (string, error) {
	if isUnixUpstream(toAddr) {
		socketPath := strings.TrimPrefix(toAddr, "unix/")
		if !isUnixSocketReady(socketPath) {
			return "", fmt.Errorf("unix socket not ready: %s", socketPath)
		}
		return toAddr, nil
	}

	if strings.HasPrefix(toAddr, ":") {
		toAddr = "127.0.0.1" + toAddr
	}
	if !strings.HasPrefix(toAddr, "http://") && !strings.HasPrefix(toAddr, "https://") {
		toAddr = "http://" + toAddr
	}
	target, err := url.Parse(toAddr)
	if err != nil {
		return "", fmt.Errorf("invalid reverse_proxy_to address: %v", err)
	}
	if target.Host == "" {
		return "", fmt.Errorf("invalid reverse_proxy_to address: missing host")
	}
	return target.Host, nil
}

func isUnixSocketReady(socketPath string) bool {
	info, err := os.Stat(socketPath)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

type killPlan struct {
	pid    int
	signal syscall.Signal
}

func processKillPlan(goos string, pid int, sig syscall.Signal) killPlan {
	if goos == "windows" {
		return killPlan{pid: pid, signal: sig}
	}
	return killPlan{pid: -pid, signal: sig}
}

func signalProcessGroup(proc *os.Process, sig syscall.Signal) error {
	if proc == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		if sig == syscall.SIGKILL {
			return proc.Kill()
		}
		return proc.Signal(sig)
	}
	plan := processKillPlan(runtime.GOOS, proc.Pid, sig)
	return syscall.Kill(plan.pid, plan.signal)
}

func isProcessAlive(proc *os.Process) bool {
	if proc == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// Best-effort on Windows; cmd.Wait() watcher will eventually clear state.
		return true
	}
	// Signal(0) means "existence check only" (no signal delivered).
	// It returns nil when the PID still exists in the process table.
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	// Linux nuance: Signal(0) can still succeed for zombie processes.
	// A zombie PID exists but cannot accept work, so treat it as dead.
	if runtime.GOOS == "linux" && isZombiePID(proc.Pid) {
		return false
	}
	return true
}

func isZombiePID(pid int) bool {
	// Reads /proc/<pid>/stat to detect zombie state ('Z').
	// This prevents us from considering a reaped-but-not-collected child
	// process as "alive" during restart checks.
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return false
	}
	// /proc/<pid>/stat format: "pid (comm) state ..."
	// The state character is located immediately after the final ') '.
	closeIdx := bytes.LastIndexByte(data, ')')
	if closeIdx == -1 || closeIdx+2 >= len(data) {
		return false
	}
	state := data[closeIdx+2]
	return state == 'Z'
}

type proxyOverrides struct {
	Executable       *[]string `json:"executable"`
	WorkingDirectory *string   `json:"working_directory"`
	Envs             *[]string `json:"envs"`
	ReverseProxyTo   *string   `json:"reverse_proxy_to"`
	ReadinessMethod  *string   `json:"readiness_method"`
	ReadinessPath    *string   `json:"readiness_path"`
}

type resolvedConfig struct {
	Executable       []string
	WorkingDirectory string
	Envs             []string
	ReverseProxyTo   string
	ReadinessMethod  string
	ReadinessPath    string
}

type runningBackend struct {
	cmd     *exec.Cmd
	process *os.Process
	done    chan error
	cancel  context.CancelFunc
	config  resolvedConfig
}

func (c *ReverseBin) resolveConfig(overrides *proxyOverrides) resolvedConfig {
	cfg := resolvedConfig{
		Executable:       c.Executable,
		WorkingDirectory: c.WorkingDirectory,
		Envs:             c.Envs,
		ReverseProxyTo:   c.ReverseProxyTo,
		ReadinessMethod:  c.ReadinessMethod,
		ReadinessPath:    c.ReadinessPath,
	}
	if overrides == nil {
		return cfg
	}
	if overrides.Executable != nil && len(*overrides.Executable) > 0 {
		cfg.Executable = *overrides.Executable
	}
	if overrides.WorkingDirectory != nil {
		cfg.WorkingDirectory = *overrides.WorkingDirectory
	}
	if overrides.Envs != nil {
		cfg.Envs = *overrides.Envs
	}
	if overrides.ReverseProxyTo != nil {
		cfg.ReverseProxyTo = *overrides.ReverseProxyTo
	}
	if overrides.ReadinessMethod != nil {
		cfg.ReadinessMethod = *overrides.ReadinessMethod
	}
	if overrides.ReadinessPath != nil {
		cfg.ReadinessPath = *overrides.ReadinessPath
	}
	return cfg
}

func (c *ReverseBin) launchBackend(ctx context.Context, cfg resolvedConfig, reason string) (*runningBackend, error) {
	if len(cfg.Executable) == 0 {
		return nil, fmt.Errorf("exec (executable) is required")
	}

	backendCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(backendCtx, cfg.Executable[0], cfg.Executable[1:]...)
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd.Process, syscall.SIGTERM)
	}
	cmd.WaitDelay = c.terminationGrace()
	configureBackendProcAttrs(cmd)
	cmd.Dir = cfg.WorkingDirectory
	if cmd.Dir == "" {
		cmd.Dir = "."
	}

	var cmdEnv []string
	if c.PassAll {
		cmdEnv = os.Environ()
	} else {
		for _, key := range c.PassEnvs {
			if val, ok := os.LookupEnv(key); ok {
				cmdEnv = append(cmdEnv, key+"="+val)
			}
		}
	}
	cmdEnv = append(cmdEnv, cfg.Envs...)
	cmd.Env = cmdEnv

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	if err := cmd.Start(); err != nil {
		cancel()
		c.logger.Error("failed to start proxy subprocess",
			zap.String("executable", cmd.Path),
			zap.Strings("args", cmd.Args),
			zap.String("reason", reason),
			zap.Error(err))
		return nil, err
	}

	pid := cmd.Process.Pid
	c.logger.Info("started proxy subprocess",
		zap.Int("pid", pid),
		zap.String("executable", cmd.Path),
		zap.Strings("args", cmd.Args),
		zap.String("reason", reason))

	logPipe := func(pipe io.ReadCloser, label string) {
		defer wg.Done()
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			c.logger.Info("", zap.Int("pid", pid), zap.String(label, scanner.Text()))
		}
	}

	go logPipe(stdoutPipe, "stdout")
	go logPipe(stderrPipe, "stderr")

	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		wg.Wait()
		c.logger.Info("proxy subprocess terminated",
			zap.Int("pid", pid),
			zap.String("reason", reason),
			zap.Error(err))
		done <- err
	}()

	return &runningBackend{
		cmd:     cmd,
		process: cmd.Process,
		done:    done,
		cancel:  cancel,
		config:  cfg,
	}, nil
}

func (c *ReverseBin) resolveRequestConfig(r *http.Request, key string) (resolvedConfig, error) {
	overrides := new(proxyOverrides)
	if len(c.DynamicProxyDetector) > 0 {
		args := strings.Split(key, " ")
		if len(args) == 0 || args[0] == "" {
			return resolvedConfig{}, fmt.Errorf("dynamic proxy detector command is empty")
		}

		c.logger.Debug("running dynamic proxy detector",
			zap.String("command", args[0]),
			zap.Strings("args", args[1:]))

		detCtx, detCancel := context.WithTimeout(r.Context(), c.readinessTimeout())
		defer detCancel()

		detectorCmd := exec.CommandContext(detCtx, args[0], args[1:]...)
		configureDetectorProcAttrs(detectorCmd)

		var outBuf, errBuf bytes.Buffer
		detectorCmd.Stdout = &outBuf
		detectorCmd.Stderr = &errBuf

		err := detectorCmd.Run()
		if errBuf.Len() > 0 {
			c.logger.Info("dynamic proxy detector stderr", zap.String("stderr", errBuf.String()))
		}
		if detCtx.Err() == context.DeadlineExceeded {
			return resolvedConfig{}, fmt.Errorf("dynamic proxy detector timed out")
		}
		if err != nil {
			return resolvedConfig{}, fmt.Errorf("dynamic proxy detector failed: %v\nOutput: %s", err, outBuf.String())
		}
		if err := json.Unmarshal(outBuf.Bytes(), overrides); err != nil {
			return resolvedConfig{}, fmt.Errorf("failed to unmarshal detector output: %v\nOutput: %s", err, outBuf.String())
		}
	}

	cfg := c.resolveConfig(overrides)
	if len(cfg.Executable) == 0 {
		return resolvedConfig{}, fmt.Errorf("exec (executable) is required")
	}
	if !isUnixUpstream(cfg.ReverseProxyTo) && !readinessConfigured(cfg.ReadinessMethod, cfg.ReadinessPath) {
		return resolvedConfig{}, fmt.Errorf("readiness_check is required for non-unix reverse_proxy_to targets")
	}
	if isUnixUpstream(cfg.ReverseProxyTo) {
		socketPath := strings.TrimPrefix(cfg.ReverseProxyTo, "unix/")
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return resolvedConfig{}, fmt.Errorf("failed to remove pre-existing unix socket %s: %w", socketPath, err)
		}
	}
	return cfg, nil
}

func (c *ReverseBin) probeReady(ctx context.Context, cfg resolvedConfig) (bool, error) {
	if cfg.ReadinessMethod == "" {
		if !isUnixUpstream(cfg.ReverseProxyTo) {
			return false, fmt.Errorf("readiness_check is required for non-unix reverse_proxy_to targets")
		}
		return isUnixSocketReady(strings.TrimPrefix(cfg.ReverseProxyTo, "unix/")), nil
	}

	scheme := "http"
	if strings.HasPrefix(cfg.ReverseProxyTo, "https://") {
		scheme = "https"
	}

	target := cfg.ReverseProxyTo
	if strings.HasPrefix(target, ":") {
		target = "127.0.0.1" + target
	}
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimPrefix(target, "https://")

	var checkURL string
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if isUnixUpstream(cfg.ReverseProxyTo) {
		socketPath := strings.TrimPrefix(cfg.ReverseProxyTo, "unix/")
		checkURL = fmt.Sprintf("%s://localhost%s", scheme, cfg.ReadinessPath)
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		}
	} else {
		checkURL = fmt.Sprintf("%s://%s%s", scheme, target, cfg.ReadinessPath)
	}

	req, err := http.NewRequestWithContext(ctx, cfg.ReadinessMethod, checkURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 400, nil
}

func (c *ReverseBin) waitReady(ctx context.Context, rb *runningBackend, cfg resolvedConfig) error {
	tickerInterval := 200 * time.Millisecond
	if isUnixUpstream(cfg.ReverseProxyTo) && cfg.ReadinessMethod == "" {
		tickerInterval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		var done <-chan error
		if rb != nil {
			done = rb.done
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if rb != nil {
				rb.done <- err
			}
			return fmt.Errorf("reverse proxy process exited during readiness check: %v", err)
		case <-ticker.C:
			ready, err := c.probeReady(ctx, cfg)
			if err != nil {
				continue
			}
			if ready {
				if rb != nil && rb.process != nil {
					c.logger.Info("reverse proxy process ready", zap.Int("pid", rb.process.Pid), zap.String("address", cfg.ReverseProxyTo))
				}
				return nil
			}
		}
	}
}

func (c *ReverseBin) stopBackend(rb *runningBackend, reason string, grace time.Duration) error {
	if rb == nil || rb.process == nil {
		return nil
	}
	c.logger.Info("terminating proxy subprocess",
		zap.Int("pid", rb.process.Pid),
		zap.String("reason", reason),
		zap.Duration("grace", grace))

	_ = signalProcessGroup(rb.process, syscall.SIGTERM)
	if rb.cancel != nil {
		rb.cancel()
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case err := <-rb.done:
		return err
	case <-timer.C:
		c.logger.Warn("proxy subprocess did not exit before grace timeout; killing",
			zap.Int("pid", rb.process.Pid),
			zap.String("reason", reason))
		_ = signalProcessGroup(rb.process, syscall.SIGKILL)
		select {
		case err := <-rb.done:
			return err
		case <-time.After(c.terminationKillWait()):
			return fmt.Errorf("timeout waiting for process %d after SIGKILL", rb.process.Pid)
		}
	}
}

type supervisorRequest struct {
	request *http.Request
	reply   chan supervisorResult
}

type supervisorResult struct {
	upstream string
	err      error
}

type supervisorCommandKind int

const (
	supervisorRequestStarted supervisorCommandKind = iota
	supervisorRequestDone
	supervisorStop
	supervisorShutdown
)

type supervisorCommand struct {
	kind   supervisorCommandKind
	reason string
	reply  chan error
}

func (c *ReverseBin) sendSupervisorCommand(ps *processState, kind supervisorCommandKind, reason string) error {
	reply := make(chan error, 1)
	cmd := supervisorCommand{kind: kind, reason: reason, reply: reply}
	select {
	case ps.commands <- cmd:
	case <-c.done():
		return c.doneErr()
	}
	select {
	case err := <-reply:
		return err
	case <-c.done():
		return c.doneErr()
	}
}

func (c *ReverseBin) moduleContext() context.Context {
	if c.ctx.Context == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *ReverseBin) done() <-chan struct{} {
	if c.ctx.Context == nil {
		return nil
	}
	return c.ctx.Done()
}

func (c *ReverseBin) doneErr() error {
	if c.ctx.Context == nil || c.ctx.Err() == nil {
		return context.Canceled
	}
	return c.ctx.Err()
}

func backendExited(rb *runningBackend) bool {
	if rb == nil {
		return true
	}
	select {
	case <-rb.done:
		return true
	default:
		return false
	}
}

func stopTimer(timer **time.Timer, idleC *<-chan time.Time) {
	if *timer != nil {
		if !(*timer).Stop() {
			select {
			case <-(*timer).C:
			default:
			}
		}
		*timer = nil
	}
	*idleC = nil
}

func (c *ReverseBin) runSupervisor(ps *processState) {
	var backend *runningBackend
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	activeRequests := int64(0)
	idleTimeout := time.Duration(c.IdleTimeoutMS) * time.Millisecond

	startIdleTimer := func() {
		if backend == nil || activeRequests != 0 {
			return
		}
		stopTimer(&idleTimer, &idleC)
		idleTimer = time.NewTimer(idleTimeout)
		idleC = idleTimer.C
		c.logger.Debug("starting idle timer", zap.String("key", ps.key), zap.Duration("duration", idleTimeout))
	}

	shutdown := func(reason string) error {
		stopTimer(&idleTimer, &idleC)
		err := c.stopBackend(backend, reason, c.terminationGrace())
		backend = nil
		return err
	}

	for {
		select {
		case req := <-ps.requests:
			stopTimer(&idleTimer, &idleC)

			if backend != nil && backendExited(backend) {
				backend = nil
			}
			if backend != nil && isUnixUpstream(backend.config.ReverseProxyTo) {
				socketPath := strings.TrimPrefix(backend.config.ReverseProxyTo, "unix/")
				if !isUnixSocketReady(socketPath) {
					c.logger.Warn("backend process alive but unix socket unavailable; restarting",
						zap.String("key", ps.key),
						zap.Int("pid", backend.process.Pid),
						zap.String("socket", socketPath))
					_ = c.stopBackend(backend, "unix socket unavailable", c.terminationGrace())
					backend = nil
					_ = os.Remove(socketPath)
				}
			}

			if backend == nil {
				cfg, err := c.resolveRequestConfig(req.request, ps.key)
				if err != nil {
					req.reply <- supervisorResult{err: err}
					continue
				}
				startCtx, cancel := context.WithTimeout(req.request.Context(), c.readinessTimeout())
				rb, err := c.launchBackend(c.moduleContext(), cfg, "request")
				if err == nil {
					err = c.waitReady(startCtx, rb, cfg)
				}
				cancel()
				if err != nil {
					_ = c.stopBackend(rb, "readiness failed", c.terminationGrace())
					req.reply <- supervisorResult{err: err}
					continue
				}
				backend = rb
			}
			req.reply <- supervisorResult{upstream: backend.config.ReverseProxyTo}

		case cmd := <-ps.commands:
			var err error
			switch cmd.kind {
			case supervisorRequestStarted:
				activeRequests++
				stopTimer(&idleTimer, &idleC)
			case supervisorRequestDone:
				if activeRequests > 0 {
					activeRequests--
				}
				if activeRequests == 0 {
					startIdleTimer()
				}
			case supervisorStop:
				err = shutdown(cmd.reason)
			case supervisorShutdown:
				err = shutdown(cmd.reason)
				if cmd.reply != nil {
					cmd.reply <- err
				}
				return
			}
			if cmd.reply != nil {
				cmd.reply <- err
			}

		case <-idleC:
			c.logger.Info("idle timer fired, terminating process", zap.String("key", ps.key))
			_ = c.stopBackend(backend, "idle timeout", c.terminationGrace())
			backend = nil
			idleTimer = nil
			idleC = nil

		case <-c.done():
			_ = shutdown("context done")
			return
		}
	}
}
