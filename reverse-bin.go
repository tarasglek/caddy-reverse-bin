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
	key := c.getProcessKey(r)
	c.mu.Lock()
	ps, ok := c.processes[key]
	c.mu.Unlock()

	if ok {
		ps.mu.Lock()
		ps.activeRequests++
		c.logger.Debug("incremented active requests", zap.String("key", key), zap.Int64("count", ps.activeRequests))
		ps.mu.Unlock()

		defer func() {
			ps.mu.Lock()
			defer ps.mu.Unlock()

			ps.activeRequests--
			c.logger.Debug("decremented active requests", zap.String("key", key), zap.Int64("count", ps.activeRequests))
			if ps.activeRequests == 0 {
				ps.idleTimer = time.AfterFunc(30*time.Second, func() {
					ps.mu.Lock()
					defer ps.mu.Unlock()
					if ps.activeRequests == 0 && ps.process != nil {
						ps.terminationMsg = "idle timeout"
						if ps.cancel != nil {
							ps.cancel()
						}
						ps.process = nil
					}
				})
			}
		}()
	}

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
func (c *ReverseBin) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	key := c.getProcessKey(r)
	c.mu.Lock()
	ps, ok := c.processes[key]
	if !ok {
		ps = &processState{}
		c.processes[key] = ps
	}
	c.mu.Unlock()

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

type logWriter struct {
	name  string
	drain func(string, io.Reader, *sync.WaitGroup, int)
	pid   int
}

func (l *logWriter) Write(p []byte) (n int, err error) {
	l.drain(l.name, strings.NewReader(string(p)), nil, l.pid)
	return len(p), nil
}

type proxyOverrides struct {
	Executable       *[]string `json:"executable"`
	WorkingDirectory *string   `json:"working_directory"`
	Args             *[]string `json:"args"`
	Envs             *[]string `json:"envs"`
	ReverseProxyTo   *string   `json:"reverse_proxy_to"`
	ReadinessMethod  *string   `json:"readiness_method"`
	ReadinessPath    *string   `json:"readiness_path"`
}

func (c *ReverseBin) startProcess(r *http.Request, ps *processState, key string) (*proxyOverrides, error) {
	var outputMu sync.Mutex
	var recentOutput []string
	const maxRecentLines = 20

	drainPipe := func(name string, pipe io.Reader, wg *sync.WaitGroup, pid int) {
		if wg != nil {
			defer wg.Done()
		}
		reader := bufio.NewReader(pipe)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimSuffix(line, "\n")
				c.logger.Info("subprocess "+name,
					zap.Int("pid", pid),
					zap.String("msg", trimmed))
				outputMu.Lock()
				recentOutput = append(recentOutput, fmt.Sprintf("[%d][%s] %s", pid, name, trimmed))
				if len(recentOutput) > maxRecentLines {
					recentOutput = recentOutput[1:]
				}
				outputMu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}

	overrides := new(proxyOverrides)
	if len(c.DynamicProxyDetector) > 0 {
		args := strings.Split(key, " ")

		c.logger.Debug("running dynamic proxy detector",
			zap.String("command", args[0]),
			zap.Strings("args", args[1:]))

		detectorCmd := exec.Command(args[0], args[1:]...)
		stdout, _ := detectorCmd.StdoutPipe()
		stderr, _ := detectorCmd.StderrPipe()

		var outBuf strings.Builder
		if err := detectorCmd.Start(); err != nil {
			return nil, fmt.Errorf("dynamic proxy detector failed to start: %v", err)
		}
		pid := detectorCmd.Process.Pid

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			io.Copy(&outBuf, io.TeeReader(stdout, &logWriter{name: "detector-stdout", drain: drainPipe, pid: pid}))
		}()
		go drainPipe("detector-stderr", stderr, &wg, pid)
		wg.Wait()
		err := detectorCmd.Wait()

		outputMu.Lock()
		recent := strings.Join(recentOutput, "\n")
		outputMu.Unlock()

		if err != nil {
			return nil, fmt.Errorf("dynamic proxy detector failed: %v\nRecent output:\n%s", err, recent)
		}

		if err := json.Unmarshal([]byte(outBuf.String()), overrides); err != nil {
			return nil, fmt.Errorf("failed to unmarshal detector output: %v\nOutput: %s", err, outBuf.String())
		}
		// Clear recent output from detector so it doesn't clutter process launch errors
		outputMu.Lock()
		recentOutput = nil
		outputMu.Unlock()
	}
	var execPath string
	var execArgs []string

	if overrides.Executable != nil && len(*overrides.Executable) > 0 {
		execPath = (*overrides.Executable)[0]
		execArgs = (*overrides.Executable)[1:]
	} else {
		execPath = c.Executable
	}

	if overrides.Args != nil {
		execArgs = append(execArgs, *overrides.Args...)
	} else if overrides.Executable == nil {
		execArgs = append(execArgs, c.Args...)
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

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	c.logger.Info("starting proxy subprocess",
		zap.String("executable", cmd.Path),
		zap.Strings("args", cmd.Args))

	if err := cmd.Start(); err != nil {
		cancel()
		c.logger.Error("failed to start proxy subprocess",
			zap.String("executable", cmd.Path),
			zap.Error(err))
		return nil, err
	}
	ps.process = cmd.Process
	ps.cancel = cancel
	pid := ps.process.Pid

	exitChan := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	go drainPipe("stdout", stdoutPipe, &wg, pid)
	go drainPipe("stderr", stderrPipe, &wg, pid)

	go func() {
		wg.Wait()
		err := cmd.Wait()

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
			zap.String("executable", cmd.Path),
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
		outputMu.Lock()
		out := strings.Join(recentOutput, "\n")
		outputMu.Unlock()
		return nil, fmt.Errorf("reverse proxy process exited during readiness check: %v\nRecent output:\n%s", err, out)
	case <-time.After(10 * time.Second):
		if ps.cancel != nil {
			ps.cancel()
		}
		outputMu.Lock()
		out := strings.Join(recentOutput, "\n")
		outputMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for reverse proxy process readiness\nRecent output:\n%s", out)
	}
}
