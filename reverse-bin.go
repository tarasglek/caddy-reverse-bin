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
	c.mu.Lock()
	c.activeRequests++
	c.logger.Debug("incremented active requests", zap.Int64("count", c.activeRequests))
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		c.activeRequests--
		c.logger.Debug("decremented active requests", zap.Int64("count", c.activeRequests))
		if c.activeRequests == 0 {
			c.idleTimer = time.AfterFunc(30*time.Second, func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				if c.activeRequests == 0 && c.process != nil {
					c.terminationMsg = "idle timeout"
					c.killProcessGroup()
					c.process = nil
				}
			})
		}
	}()

	if c.reverseProxy == nil {
		return fmt.Errorf("reverse proxy not initialized")
	}

	return c.reverseProxy.ServeHTTP(w, r, next)
}

// GetUpstreams implements reverseproxy.UpstreamSource which allows dynamic selection of backend process
// ensures process is running before returning the upstream address to the proxy.
func (c *ReverseBin) GetUpstreams(r *http.Request) ([]*reverseproxy.Upstream, error) {
	var overrides *proxyOverrides
	if c.DynamicProxyDetector != "" {
		detectorCmd := exec.Command(c.DynamicProxyDetector, r.URL.String())
		output, err := detectorCmd.Output()
		if err != nil {
			return nil, fmt.Errorf("dynamic proxy detector failed: %v", err)
		}

		overrides = new(proxyOverrides)
		if err := json.Unmarshal(output, overrides); err != nil {
			return nil, fmt.Errorf("failed to unmarshal detector output: %v", err)
		}
	}

	c.mu.Lock()
	if c.process == nil {
		if err := c.startProcess(overrides); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}

	// Stop idle timer if running
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}
	c.mu.Unlock()

	toAddr := c.ReverseProxyTo
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

func (c *ReverseBin) killProcessGroup() {
	if c.process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		// Kill the process group
		syscall.Kill(-c.process.Pid, syscall.SIGKILL)
	} else {
		c.process.Kill()
	}
}

type proxyOverrides struct {
	Executable       *string   `json:"executable"`
	WorkingDirectory *string   `json:"working_directory"`
	Args             *[]string `json:"args"`
	Envs             *[]string `json:"envs"`
	ReverseProxyTo   *string   `json:"reverse_proxy_to"`
	ReadinessMethod  *string   `json:"readiness_method"`
	ReadinessPath    *string   `json:"readiness_path"`
}

func (c *ReverseBin) startProcess(overrides *proxyOverrides) error {
	// no get rid of this duplication..just fill in nil fields directly in  proxyOverrides AI!
	executable := c.Executable
	args := c.Args
	workingDir := c.WorkingDirectory
	envs := c.Envs
	proxyTo := c.ReverseProxyTo
	readinessMethod := c.ReadinessMethod
	readinessPath := c.ReadinessPath

	if overrides != nil {
		if overrides.Executable != nil {
			executable = *overrides.Executable
		}
		if overrides.Args != nil {
			args = *overrides.Args
		}
		if overrides.WorkingDirectory != nil {
			workingDir = *overrides.WorkingDirectory
		}
		if overrides.Envs != nil {
			envs = *overrides.Envs
		}
		if overrides.ReverseProxyTo != nil {
			proxyTo = *overrides.ReverseProxyTo
		}
		if overrides.ReadinessMethod != nil {
			readinessMethod = *overrides.ReadinessMethod
		}
		if overrides.ReadinessPath != nil {
			readinessPath = *overrides.ReadinessPath
		}
	}

	cmd := exec.Command(executable, args...)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Dir = workingDir
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
	cmdEnv = append(cmdEnv, envs...)
	cmd.Env = cmdEnv

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	c.logger.Info("starting proxy subprocess",
		zap.String("executable", cmd.Path),
		zap.Strings("args", cmd.Args))

	if err := cmd.Start(); err != nil {
		c.logger.Error("failed to start proxy subprocess",
			zap.String("executable", cmd.Path),
			zap.Error(err))
		return err
	}
	c.process = cmd.Process

	exitChan := make(chan error, 1)
	go func() {
		var wg sync.WaitGroup
		drain := func(name string, pipe io.ReadCloser) {
			defer wg.Done()
			reader := bufio.NewReader(pipe)
			for {
				line, err := reader.ReadString('\n')
				if len(line) > 0 {
					c.logger.Info("reverse proxy process "+name, zap.String("msg", strings.TrimSuffix(line, "\n")))
				}
				if err != nil {
					break
				}
			}
		}

		wg.Add(2)
		go drain("stdout", stdoutPipe)
		go drain("stderr", stderrPipe)

		wg.Wait()
		err := cmd.Wait()

		c.mu.Lock()
		reason := c.terminationMsg
		if reason == "" {
			reason = "unexpected exit"
		}
		c.terminationMsg = ""
		if c.process == cmd.Process {
			c.process = nil
		}
		c.mu.Unlock()

		c.logger.Info("proxy subprocess terminated",
			zap.String("executable", cmd.Path),
			zap.String("reason", reason),
			zap.Error(err))
		exitChan <- err
	}()

	// Readiness check
	// might be able to use caddy health check here instead https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#active-health-checks
	expected := proxyTo
	if strings.HasPrefix(expected, ":") {
		expected = "127.0.0.1" + expected
	}
	expected = strings.TrimPrefix(expected, "http://")
	expected = strings.TrimPrefix(expected, "https://")

	readyChan := make(chan bool, 1)
	if readinessMethod != "" {
		scheme := "http"
		if strings.HasPrefix(proxyTo, "https://") {
			scheme = "https"
		}
		checkURL := fmt.Sprintf("%s://%s%s", scheme, expected, readinessPath)
		c.logger.Info("waiting for reverse proxy process readiness via HTTP polling",
			zap.String("method", readinessMethod),
			zap.String("url", checkURL))

		go func() {
			client := &http.Client{Timeout: 500 * time.Millisecond}
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					req, _ := http.NewRequest(readinessMethod, checkURL, nil)
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
		c.logger.Info("reverse proxy process ready", zap.String("address", expected))
		return nil
	case err := <-exitChan:
		return fmt.Errorf("reverse proxy process exited during readiness check: %v", err)
	case <-time.After(10 * time.Second):
		c.killProcessGroup()
		return fmt.Errorf("timeout waiting for reverse proxy process readiness")
	}
}
