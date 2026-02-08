// Minimal Caddy integration test harness for this repository.
//
// Adapted from Caddy's caddytest harness:
// https://github.com/caddyserver/caddy/blob/master/caddytest/caddytest.go
// Original work Copyright (c) Caddy Authors.
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	caddycmd "github.com/caddyserver/caddy/v2/cmd"
)

const adminPort = 2999

// Tester is a tiny wrapper around an HTTP client and config loading helpers.
type Tester struct {
	Client       *http.Client
	t            testing.TB
	configLoaded bool
}

func NewTester(t testing.TB) *Tester {
	return &Tester{
		Client: &http.Client{
			Transport: createTestingTransport(),
			Timeout:   10 * time.Second,
		},
		t: t,
	}
}

// InitServer loads a config into the Caddy admin endpoint.
func (tc *Tester) InitServer(rawConfig, configType string) {
	tc.t.Helper()
	if testing.Short() {
		tc.t.SkipNow()
	}

	if err := tc.ensureCaddyRunning(); err != nil {
		tc.t.Fatalf("unable to start caddy admin endpoint: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", fmt.Sprintf("http://localhost:%d/load", adminPort), strings.NewReader(rawConfig))
	if err != nil {
		tc.t.Fatalf("failed to create config load request: %v", err)
	}
	if configType == "json" {
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Set("Content-Type", "text/"+configType)
	}

	resp, err := client.Do(req)
	if err != nil {
		tc.t.Fatalf("unable to contact caddy admin endpoint: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		tc.t.Fatalf("config load failed (status %d): %s", resp.StatusCode, string(body))
	}

	tc.configLoaded = true

	// Give Caddy a brief moment to swap handlers after config load.
	time.Sleep(100 * time.Millisecond)
}

// InitServerWithDefaults wraps site blocks with common integration-test global options.
func (tc *Tester) InitServerWithDefaults(httpPort, httpsPort int, siteBlocks string) {
	tc.t.Helper()
	config := fmt.Sprintf(`{
	skip_install_trust
	admin localhost:%d
	http_port %d
	https_port %d
	grace_period 1ns
}

%s
`, adminPort, httpPort, httpsPort, siteBlocks)
	tc.InitServer(config, "caddyfile")
}

func (tc *Tester) ensureCaddyRunning() error {
	if tc.isCaddyAdminRunning() == nil {
		return nil
	}

	f, err := os.CreateTemp("", "caddy-config-*.caddyfile")
	if err != nil {
		return err
	}
	defer f.Close()
	tc.t.Cleanup(func() { _ = os.Remove(f.Name()) })

	if _, err := fmt.Fprintf(f, "{\n\tadmin localhost:%d\n}\n", adminPort); err != nil {
		return err
	}

	os.Args = []string{"caddy", "run", "--config", f.Name(), "--adapter", "caddyfile"}
	go caddycmd.Main()

	for i := 0; i < 10; i++ {
		if tc.isCaddyAdminRunning() == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return tc.isCaddyAdminRunning()
}

func (tc *Tester) isCaddyAdminRunning() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/config/", adminPort))
	if err != nil {
		return fmt.Errorf("caddy not running on localhost:%d", adminPort)
	}
	_ = resp.Body.Close()
	return nil
}

func createTestingTransport() *http.Transport {
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Second}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		parts := strings.Split(addr, ":")
		destAddr := fmt.Sprintf("127.0.0.1:%s", parts[len(parts)-1])
		return dialer.DialContext(ctx, network, destAddr)
	}
	return &http.Transport{DialContext: dialContext}
}

func (tc *Tester) AssertGetResponse(requestURI string, expectedStatusCode int, expectedBodyContains string) (*http.Response, string) {
	tc.t.Helper()

	var (
		resp *http.Response
		err  error
	)
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err = tc.Client.Get(requestURI)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			tc.t.Fatalf("failed to call server: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		tc.t.Fatalf("unable to read response body: %v", err)
	}
	body := string(bodyBytes)

	if resp.StatusCode != expectedStatusCode {
		tc.t.Fatalf("requesting %q expected status %d but got %d (body: %s)", requestURI, expectedStatusCode, resp.StatusCode, body)
	}
	if expectedBodyContains != "" && !strings.Contains(body, expectedBodyContains) {
		tc.t.Fatalf("requesting %q expected body to contain %q but got %q", requestURI, expectedBodyContains, body)
	}
	return resp, body
}
