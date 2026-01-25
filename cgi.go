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

package cgi

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

var bufPool = sync.Pool{New: func() interface{} { return &bytes.Buffer{} }}

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

func (c *CGI) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if c.Mode == "proxy" {
		return c.serveProxy(w, r, next)
	}

	// For convenience: get the currently authenticated user; if some other middleware has set that.
	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)
	var username string
	if usernameVal, exists := repl.Get("http.auth.user.id"); exists {
		if usernameVal, ok := usernameVal.(string); ok {
			username = usernameVal
		}
	}

	scriptName := repl.ReplaceAll(c.ScriptName, "")
	scriptPath := strings.TrimPrefix(r.URL.Path, scriptName)

	var cgiHandler cgi.Handler

	cgiHandler.Root = "/"

	repl.Set("root", cgiHandler.Root)
	repl.Set("path", scriptPath)

	errorBuffer := bufPool.Get().(*bytes.Buffer)
	errorBuffer.Reset()
	defer bufPool.Put(errorBuffer)

	cgiHandler.Dir = c.WorkingDirectory
	cgiHandler.Path = repl.ReplaceAll(c.Executable, "")
	cgiHandler.Stderr = errorBuffer
	for _, str := range c.Args {
		cgiHandler.Args = append(cgiHandler.Args, repl.ReplaceAll(str, ""))
	}

	c.logger.Info("starting cgi subprocess",
		zap.String("executable", cgiHandler.Path),
		zap.Strings("args", cgiHandler.Args))

	envAdd := func(key, val string) {
		cgiHandler.Env = append(cgiHandler.Env, key+"="+val)
	}
	envAdd("PATH_INFO", scriptPath)
	envAdd("SCRIPT_FILENAME", cgiHandler.Path)
	envAdd("SCRIPT_NAME", scriptName)
	envAdd("SCRIPT_EXEC", fmt.Sprintf("%s %s", cgiHandler.Path, strings.Join(cgiHandler.Args, " ")))
	envAdd("REMOTE_USER", username)

	// work around Go's CGI not handling chunked transfer encodings
	// https://github.com/golang/go/issues/5613
	if len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked" {
		// buffer request in memory or temporary file if too large
		// to make it possible to calculate the CONTENT_LENGTH of the body
		defer r.Body.Close()

		buf := bufPool.Get().(*bytes.Buffer)
		buf.Reset()
		defer bufPool.Put(buf)
		if buf.Cap() < int(c.BufferLimit) {
			buf.Grow(int(c.BufferLimit) + bytes.MinRead)
		}

		size, err := io.CopyN(buf, r.Body, c.BufferLimit)
		if err != nil && err != io.EOF {
			return err
		}

		// if the buffer is full there is probably more,
		// so use a tempfile to read the rest and use that as request body
		if size == c.BufferLimit {
			tempfile, err := os.CreateTemp("", "cgi_body_*")
			if err != nil {
				return err
			}
			defer os.Remove(tempfile.Name())
			defer tempfile.Close()

			// write the already read bytes
			_, err = tempfile.Write(buf.Bytes())
			if err != nil {
				return err
			}

			// reuse the bytes slice of the buffer to copy the rest of the body to the tempfile
			remainingSize, err := io.CopyBuffer(tempfile, r.Body, buf.Bytes())
			if err != nil {
				return err
			}
			size += remainingSize

			// seek to start, so it can be read from the beginning
			_, err = tempfile.Seek(0, io.SeekStart)
			if err != nil {
				return err
			}
			r.Body = tempfile
		} else {
			r.Body = io.NopCloser(buf)
		}

		// all the request body is read, so it isn't chunked anymore
		r.TransferEncoding = nil
		r.Header.Del("Transfer-Encoding")

		// we can set the size of the request body now that we read everything
		sizeStr := strconv.FormatInt(size, 10)
		r.Header.Add("Content-Length", sizeStr)
		r.ContentLength = size
	}

	for _, e := range c.Envs {
		cgiHandler.Env = append(cgiHandler.Env, repl.ReplaceAll(e, ""))
	}

	if c.PassAll {
		cgiHandler.InheritEnv = passAll()
	} else {
		cgiHandler.InheritEnv = append(cgiHandler.InheritEnv, c.PassEnvs...)
	}

	if c.Inspect {
		inspect(cgiHandler, w, r, repl)
	} else {
		cgiWriter := w

		if c.UnbufferedOutput {
			if _, isFlusher := w.(http.Flusher); isFlusher {
				cgiWriter = instantWriter{w}
			} else {
				c.logger.Warn("Cannot write response without buffer.")
			}
		}
		cgiHandler.ServeHTTP(cgiWriter, r)

		c.logger.Info("cgi subprocess terminated",
			zap.String("executable", cgiHandler.Path),
			zap.Strings("args", cgiHandler.Args))
	}

	if errorBuffer.Len() > 0 {
		c.logger.Error("Error from CGI Application", zap.Stringer("stderr", errorBuffer))
	}

	return nil
}

func (c *CGI) killProcessGroup() {
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

type instantWriter struct {
	http.ResponseWriter
}

func (iw instantWriter) Write(b []byte) (int, error) {
	n, err := iw.ResponseWriter.Write(b)
	iw.ResponseWriter.(http.Flusher).Flush()
	return n, err
}

func (c *CGI) serveProxy(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	c.mu.Lock()
	if c.process == nil {
		if err := c.startProcess(); err != nil {
			c.mu.Unlock()
			return err
		}
	}

	// Stop idle timer if running
	if c.idleTimer != nil {
		c.idleTimer.Stop()
		c.idleTimer = nil
	}

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

	return c.reverseProxy.ServeHTTP(w, r, next)
}

func (c *CGI) startProcess() error {
	// Prepare command
	cmd := exec.Command(c.Executable, c.Args...)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Dir = c.WorkingDirectory
	if cmd.Dir == "" {
		cmd.Dir = "."
	}

	// Environment
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
	cmdEnv = append(cmdEnv, c.Envs...)
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
			zap.Strings("args", cmd.Args),
			zap.Error(err))
		return err
	}
	c.process = cmd.Process

	// Wait for readiness
	expected := c.ReverseProxyTo
	if strings.HasPrefix(expected, ":") {
		expected = "127.0.0.1" + expected
	}
	expected = strings.TrimPrefix(expected, "http://")
	expected = strings.TrimPrefix(expected, "https://")

	reader := bufio.NewReader(stdoutPipe)

	if c.ReadinessMethod != "" {
		// HTTP Polling readiness check
		scheme := "http"
		if strings.HasPrefix(c.ReverseProxyTo, "https://") {
			scheme = "https"
		}
		checkURL := fmt.Sprintf("%s://%s%s", scheme, expected, c.ReadinessPath)
		c.logger.Info("waiting for CGI process readiness via HTTP polling",
			zap.String("method", c.ReadinessMethod),
			zap.String("url", checkURL))
		client := &http.Client{Timeout: 500 * time.Millisecond}
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		// Start a goroutine to drain stdout so the process doesn't block while we poll
		readyChan := make(chan int, 1)
		go func() {
			checks := 0
			for {
				checks++
				req, _ := http.NewRequest(c.ReadinessMethod, checkURL, nil)
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					readyChan <- checks
					return
				}
				c.logger.Debug("readiness check failed",
					zap.String("url", checkURL),
					zap.Int("attempt", checks),
					zap.Error(err))
				select {
				case <-ticker.C:
					continue
				case <-c.ctx.Done():
					return
				}
			}
		}()

		select {
		case checks := <-readyChan:
			c.logger.Info("CGI process ready (http check)",
				zap.String("url", checkURL),
				zap.Int("checks", checks))
		case <-time.After(30 * time.Second):
			c.killProcessGroup()
			c.process = nil
			return fmt.Errorf("timeout waiting for CGI process readiness via HTTP at %s", checkURL)
		}
	} else {
		// Wait for readiness signal from stdout
		c.logger.Info("waiting for CGI process readiness via stdout",
			zap.String("expected_substring", expected))
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				c.killProcessGroup()
				c.process = nil
				return fmt.Errorf("failed to read readiness signal from stdout: %v", err)
			}
			if strings.Contains(line, expected) {
				c.logger.Info("CGI process ready", zap.String("address", expected))
				break
			}
			c.logger.Info("CGI process stdout (startup)", zap.String("msg", strings.TrimSpace(line)))
		}
	}

	// Handle stderr and process exit
	go func() {
		// Consume remaining stdout
		go func() {
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				c.logger.Info("CGI process stdout", zap.String("msg", strings.TrimSpace(line)))
			}
		}()

		// Consume stderr
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			c.logger.Info("CGI process stderr", zap.String("msg", scanner.Text()))
		}

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
			zap.Strings("args", cmd.Args),
			zap.String("reason", reason),
			zap.Error(err))
	}()

	return nil
}
