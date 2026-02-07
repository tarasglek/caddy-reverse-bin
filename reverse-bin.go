/*
 * Copyright (c) 2017 Kurt Jung (Gmail: kurt.w.jung)
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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// passAll returns a slice of strings made up of each environment key
func passAll() (list []string) {
	envList := os.Environ() // ["HOME=/home/foo", "LVL=2", ...]
	for _, str := range envList {
		pos := strings.Index(str, "=")
		if pos > 0 {
			list = append(list, str[:pos])
		}
	}
	return
}

// ServeHTTP implements caddyhttp.MiddlewareHandler; it handles the HTTP request
// manages idle process killing
func (c *ReverseBin) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	c.logger.Debug("ServeHTTP", zap.String("uri", r.RequestURI))
	key := c.getProcessKey(r)
	ps := c.getOrCreateProcessState(key)

	ps.incrementRequests(c.logger, key)
	defer ps.decrementRequests(c.logger, key)

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

	ps.mu.Lock()
	if ps.process == nil {
		overrides, err := c.startProcess(r, ps, key)
		if err != nil {
			ps.mu.Unlock()
			return nil, err
		}
		ps.overrides = overrides
	}

	// Stop idle timer if running
	if ps.idleTimer != nil {
		ps.idleTimer.Stop()
		ps.idleTimer = nil
	}
	overrides := ps.overrides
	ps.mu.Unlock()

	toAddr := c.ReverseProxyTo
	if overrides != nil && overrides.ReverseProxyTo != nil {
		toAddr = *overrides.ReverseProxyTo
	}
	if strings.HasPrefix(toAddr, ":") {
		toAddr = "127.0.0.1" + toAddr
	}
	if !strings.HasPrefix(toAddr, "http://") && !strings.HasPrefix(toAddr, "https://") {
		toAddr = "http://" + toAddr
	}

	target, err := url.Parse(toAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid reverse_proxy_to address: %v", err)
	}

	return []*reverseproxy.Upstream{
		{Dial: target.Host},
	}, nil
}

func (c *ReverseBin) killProcessGroup(proc *os.Process) {
	if proc == nil {
		return
	}
	if runtime.GOOS != "windows" {
		// Kill the process group
		syscall.Kill(-proc.Pid, syscall.SIGKILL)
	} else {
		proc.Kill()
	}
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

		if runtime.GOOS == "linux" {
			detectorCmd.SysProcAttr = &syscall.SysProcAttr{
				Pdeathsig: syscall.SIGTERM,
				Setpgid:   true,
			}
		}

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

	ctx, cancel := context.WithCancel(c.ctx)
	cmd := exec.CommandContext(ctx, execPath, execArgs...)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
		if runtime.GOOS == "linux" {
			cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
		}
	}
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
		checkURL := fmt.Sprintf("%s://%s%s", scheme, expected, *overrides.ReadinessPath)
		c.logger.Info("waiting for reverse proxy process readiness via HTTP polling",
			zap.String("method", *overrides.ReadinessMethod),
			zap.String("url", checkURL))

		go func() {
			client := &http.Client{Timeout: 500 * time.Millisecond}
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					req, _ := http.NewRequest(*overrides.ReadinessMethod, checkURL, nil)
					resp, err := client.Do(req)
					if err == nil {
						resp.Body.Close()
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
	} else {
		// If no HTTP check, we assume it's ready immediately as we are draining stdout
		// (The previous stdout-substring logic was fragile and blocked the drainer)
		readyChan <- true
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
