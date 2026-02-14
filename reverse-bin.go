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


// ServeHTTP implements caddyhttp.MiddlewareHandler; it handles the HTTP request
// manages idle process killing
func (c *ReverseBin) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	c.logger.Debug("ServeHTTP", zap.String("uri", r.RequestURI))
	key := c.getProcessKey(r)
	ps := c.getOrCreateProcessState(key)

	ps.incrementRequests(c.logger, key)
	defer ps.decrementRequests(c.logger, key, time.Duration(c.IdleTimeoutMS)*time.Millisecond)

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

	toAddr, err := c.ensureProcessRunningAndResolveUpstream(r, ps, key)
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

func (c *ReverseBin) ensureProcessRunningAndResolveUpstream(r *http.Request, ps *processState, key string) (string, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.process != nil {
		if !isProcessAlive(ps.process) {
			c.handleDeadProcessLocked(ps, key)
		} else {
			currentAddr := c.ReverseProxyTo
			if ps.overrides != nil && ps.overrides.ReverseProxyTo != nil {
				currentAddr = *ps.overrides.ReverseProxyTo
			}
			if isUnixUpstream(currentAddr) && !isUnixSocketReady(strings.TrimPrefix(currentAddr, "unix/")) {
				c.logger.Warn("backend process alive but unix socket unavailable; restarting",
					zap.String("key", key),
					zap.Int("pid", ps.process.Pid),
					zap.String("socket", strings.TrimPrefix(currentAddr, "unix/")))
				c.handleDeadProcessLocked(ps, key)
			}
		}
	}
	if ps.process == nil {
		overrides, err := c.startProcess(r, ps, key)
		if err != nil {
			return "", err
		}
		ps.overrides = overrides
	}

	if ps.idleTimer != nil {
		ps.idleTimer.Stop()
		ps.idleTimer = nil
	}

	toAddr := c.ReverseProxyTo
	if ps.overrides != nil && ps.overrides.ReverseProxyTo != nil {
		toAddr = *ps.overrides.ReverseProxyTo
	}
	return toAddr, nil
}

func (c *ReverseBin) handleDeadProcessLocked(ps *processState, key string) {
	c.logger.Warn("detected dead backend process before proxying; restarting",
		zap.String("key", key),
		zap.Int("pid", ps.process.Pid))
	ps.process = nil
	ps.cancel = nil

	staleAddr := c.ReverseProxyTo
	if ps.overrides != nil && ps.overrides.ReverseProxyTo != nil {
		staleAddr = *ps.overrides.ReverseProxyTo
	}
	if isUnixUpstream(staleAddr) {
		socketPath := strings.TrimPrefix(staleAddr, "unix/")
		_ = os.Remove(socketPath)
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

func (c *ReverseBin) killProcessGroup(proc *os.Process) {
	if proc == nil {
		return
	}
	if runtime.GOOS != "windows" {
		// Kill the process group
		_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
	} else {
		_ = proc.Kill()
	}
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

func (c *ReverseBin) startProcess(r *http.Request, ps *processState, key string) (*proxyOverrides, error) {
	overrides := new(proxyOverrides)
	// If a dynamic proxy detector is configured, execute it to determine
	// the specific parameters (executable, args, env, etc.) for the backend
	// process based on the request context.
	if len(c.DynamicProxyDetector) > 0 {
		args := strings.Split(key, " ")

		c.logger.Debug("running dynamic proxy detector",
			zap.String("command", args[0]),
			zap.Strings("args", args[1:]))

		// Use a timeout for the detector to prevent hanging the request indefinitely
		detCtx, detCancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer detCancel()

		detectorCmd := exec.CommandContext(detCtx, args[0], args[1:]...)

		configureDetectorProcAttrs(detectorCmd)

		var outBuf, errBuf bytes.Buffer
		detectorCmd.Stdout = &outBuf
		detectorCmd.Stderr = &errBuf

		err := detectorCmd.Run()

		if errBuf.Len() > 0 {
			c.logger.Info("dynamic proxy detector stderr",
				zap.String("stderr", errBuf.String()))
		}

		if detCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("dynamic proxy detector timed out")
		}

		if err != nil {
			return nil, fmt.Errorf("dynamic proxy detector failed: %v\nOutput: %s", err, outBuf.String())
		}

		if err := json.Unmarshal(outBuf.Bytes(), overrides); err != nil {
			return nil, fmt.Errorf("failed to unmarshal detector output: %v\nOutput: %s", err, outBuf.String())
		}
	}
	var execPath string
	var execArgs []string

	if overrides.Executable != nil && len(*overrides.Executable) > 0 {
		execPath = (*overrides.Executable)[0]
		execArgs = (*overrides.Executable)[1:]
	} else if len(c.Executable) > 0 {
		execPath = c.Executable[0]
		execArgs = c.Executable[1:]
	}
	if overrides.WorkingDirectory == nil {
		overrides.WorkingDirectory = &c.WorkingDirectory
	}
	if overrides.Envs == nil {
		overrides.Envs = &c.Envs
	}
	if overrides.ReverseProxyTo == nil {
		overrides.ReverseProxyTo = &c.ReverseProxyTo
	}
	if overrides.ReadinessMethod == nil {
		overrides.ReadinessMethod = &c.ReadinessMethod
	}
	if overrides.ReadinessPath == nil {
		overrides.ReadinessPath = &c.ReadinessPath
	}

	if !isUnixUpstream(*overrides.ReverseProxyTo) && !readinessConfigured(*overrides.ReadinessMethod, *overrides.ReadinessPath) {
		return nil, fmt.Errorf("readiness_check is required for non-unix reverse_proxy_to targets")
	}

	if isUnixUpstream(*overrides.ReverseProxyTo) {
		socketPath := strings.TrimPrefix(*overrides.ReverseProxyTo, "unix/")
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to remove pre-existing unix socket %s: %w", socketPath, err)
		}
	}

	ctx, cancel := context.WithCancel(c.ctx)
	cmd := exec.CommandContext(ctx, execPath, execArgs...)
	configureBackendProcAttrs(cmd)
	cmd.Dir = *overrides.WorkingDirectory
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
	cmdEnv = append(cmdEnv, *overrides.Envs...)
	cmd.Env = cmdEnv

	// Set up output capturing before starting the process to ensure no output is missed.
	// We use a dummy PID placeholder until the process starts and we get the real one.
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
			zap.Error(err))
		return nil, err
	}
	ps.process = cmd.Process
	ps.cancel = cancel
	pid := ps.process.Pid

	c.logger.Info("started proxy subprocess",
		zap.Int("pid", pid),
		zap.String("executable", cmd.Path),
		zap.Strings("args", cmd.Args))

	logPipe := func(pipe io.ReadCloser, label string) {
		defer wg.Done()
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			c.logger.Info("", zap.Int("pid", pid), zap.String(label, scanner.Text()))
		}
	}

	go logPipe(stdoutPipe, "stdout")
	go logPipe(stderrPipe, "stderr")

	exitChan := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		wg.Wait()

		ps.mu.Lock()
		reason := ps.terminationMsg
		if reason == "" {
			reason = "unexpected exit"
		}
		ps.terminationMsg = ""
		if ps.process == cmd.Process {
			ps.process = nil
		}
		ps.mu.Unlock()

		c.logger.Info("proxy subprocess terminated",
			zap.Int("pid", pid),
			zap.String("reason", reason),
			zap.Error(err))
		exitChan <- err
	}()

	// Readiness check
	// might be able to use caddy health check here instead https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#active-health-checks
	expected := *overrides.ReverseProxyTo
	if strings.HasPrefix(expected, ":") {
		expected = "127.0.0.1" + expected
	}
	expected = strings.TrimPrefix(expected, "http://")
	expected = strings.TrimPrefix(expected, "https://")

	readyChan := make(chan bool, 1)
	if *overrides.ReadinessMethod != "" {
		scheme := "http"
		if strings.HasPrefix(*overrides.ReverseProxyTo, "https://") {
			scheme = "https"
		}

		var checkURL string
		var client *http.Client

		if strings.HasPrefix(*overrides.ReverseProxyTo, "unix/") {
			socketPath := strings.TrimPrefix(*overrides.ReverseProxyTo, "unix/")
			// For unix sockets, the host in the URL is ignored by the custom dialer
			checkURL = fmt.Sprintf("%s://localhost%s", scheme, *overrides.ReadinessPath)
			client = &http.Client{
				Timeout: 500 * time.Millisecond,
				Transport: &http.Transport{
					DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
						return net.Dial("unix", socketPath)
					},
				},
			}
		} else {
			checkURL = fmt.Sprintf("%s://%s%s", scheme, expected, *overrides.ReadinessPath)
			client = &http.Client{Timeout: 500 * time.Millisecond}
		}

		c.logger.Info("waiting for reverse proxy process readiness via HTTP polling",
			zap.String("method", *overrides.ReadinessMethod),
			zap.String("url", checkURL),
			zap.String("target", *overrides.ReverseProxyTo))

		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					req, _ := http.NewRequest(*overrides.ReadinessMethod, checkURL, nil)
					resp, err := client.Do(req)
					if err == nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
						if resp.StatusCode >= 200 && resp.StatusCode < 400 {
							readyChan <- true
							return
						}
					}
				case <-c.ctx.Done():
					return
				}
			}
		}()
	} else if isUnixUpstream(*overrides.ReverseProxyTo) {
		socketPath := strings.TrimPrefix(*overrides.ReverseProxyTo, "unix/")
		c.logger.Info("waiting for reverse proxy process readiness via unix socket creation",
			zap.String("target", *overrides.ReverseProxyTo))
		go func() {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					info, err := os.Stat(socketPath)
					if err == nil && info.Mode()&os.ModeSocket != 0 {
						readyChan <- true
						return
					}
				case <-c.ctx.Done():
					return
				}
			}
		}()
	} else {
		return nil, fmt.Errorf("readiness_check is required for non-unix reverse_proxy_to targets")
	}

	select {
	case <-readyChan:
		c.logger.Info("reverse proxy process ready",
			zap.Int("pid", pid),
			zap.String("address", expected))
		return overrides, nil
	case err := <-exitChan:
		return nil, fmt.Errorf("reverse proxy process exited during readiness check: %v", err)
	case <-time.After(10 * time.Second):
		if ps.cancel != nil {
			ps.cancel()
		}
		return nil, fmt.Errorf("timeout waiting for reverse proxy process readiness")
	}
}
