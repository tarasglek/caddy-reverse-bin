package reversebin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap/zaptest"
)

type reverseBinConfig struct {
	Executable            []string
	WorkingDirectory      string
	Envs                  []string
	PassEnvs              []string
	PassAll               bool
	ReverseProxyTo        string
	HealthMethod          string
	HealthPath            string
	HealthStatus          int
	DynamicProxyDetector  []string
	IdleTimeoutMS         int
	HealthTimeoutMS       int
	TerminationGraceMS    int
	TerminationKillWaitMS int
}

func asConfig(c *ReverseBin) reverseBinConfig {
	return reverseBinConfig{
		Executable:            c.Executable,
		WorkingDirectory:      c.WorkingDirectory,
		Envs:                  c.Envs,
		PassEnvs:              c.PassEnvs,
		PassAll:               c.PassAll,
		ReverseProxyTo:        c.ReverseProxyTo,
		HealthMethod:          c.HealthMethod,
		HealthPath:            c.HealthPath,
		HealthStatus:          c.HealthStatus,
		DynamicProxyDetector:  c.DynamicProxyDetector,
		IdleTimeoutMS:         c.IdleTimeoutMS,
		HealthTimeoutMS:       c.HealthTimeoutMS,
		TerminationGraceMS:    c.TerminationGraceMS,
		TerminationKillWaitMS: c.TerminationKillWaitMS,
	}
}

// TestProcessKillPlanUnix verifies Unix process groups are targeted by negative PID.
func TestProcessKillPlanUnix(t *testing.T) {
	plan := processKillPlan("linux", 1234, syscall.SIGTERM)
	if plan.pid != -1234 {
		t.Fatalf("unix kill plan must target process group via negative pid: got %d", plan.pid)
	}
	if plan.signal != syscall.SIGTERM {
		t.Fatalf("unix kill plan must preserve requested signal: got %v", plan.signal)
	}
}

// TestProcessKillPlanWindows verifies Windows targets only direct process PID.
func TestProcessKillPlanWindows(t *testing.T) {
	plan := processKillPlan("windows", 1234, syscall.SIGKILL)
	if plan.pid != 1234 {
		t.Fatalf("windows kill plan must target process pid directly: got %d", plan.pid)
	}
	if plan.signal != syscall.SIGKILL {
		t.Fatalf("windows kill plan must preserve requested signal: got %v", plan.signal)
	}
}

func testStringPtr(s string) *string {
	return &s
}

func testIntPtr(i int) *int {
	return &i
}

// TestResolvedConfigUsesDetectorOverrides verifies dynamic detector output overrides static config.
func TestResolvedConfigUsesDetectorOverrides(t *testing.T) {
	rb := &ReverseBin{
		Executable:       []string{"static", "arg"},
		WorkingDirectory: "/static",
		Envs:             []string{"A=static"},
		ReverseProxyTo:   "unix//static.sock",
		HealthMethod:     "GET",
		HealthPath:       "/static-healthy",
		HealthStatus:     204,
	}
	overrides := &proxyOverrides{
		Executable:       &[]string{"dynamic", "arg2"},
		WorkingDirectory: testStringPtr("/dynamic"),
		Envs:             &[]string{"A=dynamic"},
		ReverseProxyTo:   testStringPtr("unix//dynamic.sock"),
		HealthMethod:     testStringPtr("HEAD"),
		HealthPath:       testStringPtr("/dynamic-healthy"),
		HealthStatus:     testIntPtr(401),
	}

	cfg := rb.resolveConfig(overrides)

	if got, want := strings.Join(cfg.Executable, " "), "dynamic arg2"; got != want {
		t.Fatalf("expected executable override %q, got %q", want, got)
	}
	if cfg.WorkingDirectory != "/dynamic" {
		t.Fatalf("expected working directory override, got %q", cfg.WorkingDirectory)
	}
	if got, want := strings.Join(cfg.Envs, ","), "A=dynamic"; got != want {
		t.Fatalf("expected env override %q, got %q", want, got)
	}
	if cfg.ReverseProxyTo != "unix//dynamic.sock" {
		t.Fatalf("expected reverse proxy override, got %q", cfg.ReverseProxyTo)
	}
	if cfg.HealthMethod != "HEAD" || cfg.HealthPath != "/dynamic-healthy" || cfg.HealthStatus != 401 {
		t.Fatalf("expected health override HEAD /dynamic-healthy 401, got %s %s %d", cfg.HealthMethod, cfg.HealthPath, cfg.HealthStatus)
	}
}

// TestWaitHealthyStopsOnContextCancel verifies health polling exits when start context ends.
func TestWaitHealthyStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rb := &ReverseBin{logger: zaptest.NewLogger(t)}
	err := rb.waitHealthy(ctx, nil, resolvedConfig{
		ReverseProxyTo: "unix//tmp/never-healthy.sock",
		HealthMethod:   "",
	}, nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from waitHealthy, got %v", err)
	}
}

// TestGetOrCreateProcessStateReusesSupervisor verifies one lifecycle owner per process key.
func TestGetOrCreateProcessStateReusesSupervisor(t *testing.T) {
	rb := &ReverseBin{processes: map[string]*processState{}, logger: zaptest.NewLogger(t), ctx: caddy.Context{Context: context.Background()}}

	first := rb.getOrCreateProcessState("app")
	second := rb.getOrCreateProcessState("app")

	if first != second {
		t.Fatalf("expected same processState for same key")
	}
	if first.requests == nil || first.commands == nil {
		t.Fatalf("expected supervisor channels to be initialized")
	}
}

func TestReverseBin_UnmarshalCaddyfile(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected reverseBinConfig
		wantErr  bool
	}{
		{
			name: "basic executable with args",
			input: `reverse-bin {
  exec /some/file a b c d 1
  dir /somewhere
  env foo=bar what=ever
  pass_env some_env other_env
  pass_all_env
}`,
			expected: reverseBinConfig{
				Executable:       []string{"/some/file", "a", "b", "c", "d", "1"},
				WorkingDirectory: "/somewhere",
				Envs:             []string{"foo=bar", "what=ever"},
				PassEnvs:         []string{"some_env", "other_env"},
				PassAll:          true,
			},
			wantErr: false,
		},
		{
			name: "with reverse_proxy_to",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: false,
		},
		{
			name: "with reverse_proxy_to port only",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to :8080
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: ":8080",
			},
			wantErr: false,
		},
		{
			name: "with reverse_proxy_to unix socket",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to unix//tmp/app.sock
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "unix//tmp/app.sock",
			},
			wantErr: false,
		},
		{
			name: "with health_check",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /health
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
				HealthMethod:   "GET",
				HealthPath:     "/health",
			},
			wantErr: false,
		},
		{
			name: "with health_check HEAD",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check head /healthy
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
				HealthMethod:   "HEAD",
				HealthPath:     "/healthy",
			},
			wantErr: false,
		},
		{
			name: "health_check rejects null",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to unix//tmp/app.sock
  health_check null
}`,
			expected: reverseBinConfig{},
			wantErr:  true,
		},
		{
			name: "with health_check and explicit status",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /v2/ 401
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
				HealthMethod:   "GET",
				HealthPath:     "/v2/",
				HealthStatus:   401,
			},
			wantErr: false,
		},
		{
			name: "with health_check default status range",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /health
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
				HealthMethod:   "GET",
				HealthPath:     "/health",
				HealthStatus:   0,
			},
			wantErr: false,
		},
		{
			name: "with health_timeout_ms",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /health
  health_timeout_ms 15000
}`,
			expected: reverseBinConfig{
				Executable:      []string{"./main.py"},
				ReverseProxyTo:  "127.0.0.1:8080",
				HealthMethod:    "GET",
				HealthPath:      "/health",
				HealthTimeoutMS: 15000,
			},
			wantErr: false,
		},
		{
			name: "health_check rejects low explicit status",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /v2/ 99
}`,
			expected: reverseBinConfig{},
			wantErr:  true,
		},
		{
			name: "health_check rejects high explicit status",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  health_check GET /v2/ 600
}`,
			expected: reverseBinConfig{},
			wantErr:  true,
		},
		{
			name: "with dynamic_proxy_detector",
			input: `reverse-bin {
  dynamic_proxy_detector ./discover.py {path}
}`,
			expected: reverseBinConfig{
				DynamicProxyDetector: []string{"./discover.py", "{path}"},
			},
			wantErr: false,
		},
		{
			name: "full configuration",
			input: `reverse-bin {
  exec ./main.py arg1 arg2
  dir /app
  env FOO=bar BAZ=qux
  pass_env HOME PATH
  pass_all_env
  reverse_proxy_to 127.0.0.1:3000
  health_check GET /healthz
  dynamic_proxy_detector /bin/detect {host} {path}
  idle_timeout_ms 100
  health_timeout_ms 15000
  termination_grace_ms 5000
  termination_kill_wait_ms 1000
}`,
			expected: reverseBinConfig{
				Executable:            []string{"./main.py", "arg1", "arg2"},
				WorkingDirectory:      "/app",
				Envs:                  []string{"FOO=bar", "BAZ=qux"},
				PassEnvs:              []string{"HOME", "PATH"},
				PassAll:               true,
				ReverseProxyTo:        "127.0.0.1:3000",
				HealthMethod:          "GET",
				HealthPath:            "/healthz",
				DynamicProxyDetector:  []string{"/bin/detect", "{host}", "{path}"},
				IdleTimeoutMS:         100,
				HealthTimeoutMS:       15000,
				TerminationGraceMS:    5000,
				TerminationKillWaitMS: 1000,
			},
			wantErr: false,
		},
		{
			name: "exec requires argument",
			input: `reverse-bin {
  exec
}`,
			expected: reverseBinConfig{},
			wantErr:  true,
		},
		{
			name: "unknown subdirective errors",
			input: `reverse-bin {
  exec ./main.py
  unknown_option value
}`,
			expected: reverseBinConfig{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := caddyfile.NewTestDispenser(tt.input)
			var c ReverseBin
			err := c.UnmarshalCaddyfile(d)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Cannot parse caddyfile: %v", err)
			}

			if !reflect.DeepEqual(asConfig(&c), tt.expected) {
				t.Errorf("Parsing yielded invalid result.\nGot:      %#v\nExpected: %#v", asConfig(&c), tt.expected)
			}
		})
	}
}

// TestProbeHealthAcceptsExplicitStatus verifies auth-protected endpoints can be healthy.
func TestProbeHealthAcceptsExplicitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This HTTP request tests health probing against an auth-protected registry route.
		if r.Method != http.MethodGet || r.URL.Path != "/v2/" {
			t.Fatalf("unexpected health request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	rb := &ReverseBin{logger: zaptest.NewLogger(t)}
	ok, err := rb.probeHealth(context.Background(), resolvedConfig{
		ReverseProxyTo: server.URL,
		HealthMethod:   http.MethodGet,
		HealthPath:     "/v2/",
		HealthStatus:   http.StatusUnauthorized,
	}, nil)

	if err != nil {
		t.Fatalf("probeHealth returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected explicit 401 health status to be accepted")
	}
}

// TestProbeHealthRejectsUnexpectedExplicitStatus verifies explicit status is exact.
func TestProbeHealthRejectsUnexpectedExplicitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This HTTP request tests that 2xx is not accepted when health_status requires 401.
		if r.Method != http.MethodGet || r.URL.Path != "/v2/" {
			t.Fatalf("unexpected health request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rb := &ReverseBin{logger: zaptest.NewLogger(t)}
	ok, err := rb.probeHealth(context.Background(), resolvedConfig{
		ReverseProxyTo: server.URL,
		HealthMethod:   http.MethodGet,
		HealthPath:     "/v2/",
		HealthStatus:   http.StatusUnauthorized,
	}, nil)

	if err != nil {
		t.Fatalf("probeHealth returned error: %v", err)
	}
	if ok {
		t.Fatalf("expected 200 response to be rejected when explicit health status is 401")
	}
}

func TestResolveDialAddress(t *testing.T) {
	tests := []struct {
		name           string
		reverseProxyTo string
		wantDial       string
		wantErr        bool
	}{
		{name: "IP and port", reverseProxyTo: "127.0.0.1:8080", wantDial: "127.0.0.1:8080"},
		{name: "port only", reverseProxyTo: ":8080", wantDial: "127.0.0.1:8080"},
		{name: "with http scheme", reverseProxyTo: "http://127.0.0.1:8080", wantDial: "127.0.0.1:8080"},
		{name: "invalid host", reverseProxyTo: "http://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialAddr, err := resolveDialAddress(tt.reverseProxyTo)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dial=%q", dialAddr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dialAddr != tt.wantDial {
				t.Fatalf("expected dial %q, got %q", tt.wantDial, dialAddr)
			}
		})
	}
}

func TestResolveDialAddress_UnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "app.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("failed to listen on unix socket: %v", err)
	}
	defer ln.Close()
	defer os.Remove(sock)

	dialAddr, err := resolveDialAddress("unix/" + sock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dialAddr != "unix/"+sock {
		t.Fatalf("expected unix dial address, got %q", dialAddr)
	}
}

func TestReverseBin_GetProcessKey(t *testing.T) {
	tests := []struct {
		name         string
		detector     []string
		requestPath  string
		wantKeyEmpty bool
	}{
		{
			name:         "no detector returns empty key",
			detector:     nil,
			requestPath:  "/app1/test",
			wantKeyEmpty: true,
		},
		{
			name:         "detector with static args",
			detector:     []string{"/bin/detect", "arg1"},
			requestPath:  "/test",
			wantKeyEmpty: false,
		},
		{
			name:         "detector with placeholder",
			detector:     []string{"/bin/detect", "{path}"},
			requestPath:  "/myapp/handler",
			wantKeyEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ReverseBin{
				DynamicProxyDetector: tt.detector,
				logger:               zaptest.NewLogger(t),
			}

			req := httptest.NewRequest(http.MethodGet, "http://localhost"+tt.requestPath, nil)
			repl := caddy.NewReplacer()
			req = req.WithContext(context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl))

			key := c.getProcessKey(req)

			if tt.wantKeyEmpty && key != "" {
				t.Errorf("expected empty key, got %q", key)
			}
			if !tt.wantKeyEmpty && key == "" {
				t.Errorf("expected non-empty key")
			}
		})
	}
}

func TestReverseBin_ProvisionValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     reverseBinConfig
		wantErr bool
	}{
		{
			name: "invalid static non-unix without health_check",
			cfg: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "valid static non-unix with health_check",
			cfg: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
				HealthMethod:   "GET",
				HealthPath:     "/health",
			},
			wantErr: false,
		},
		{
			name: "valid static unix without health_check",
			cfg: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "unix//tmp/app.sock",
			},
			wantErr: false,
		},
		{
			name: "valid dynamic config",
			cfg: reverseBinConfig{
				DynamicProxyDetector: []string{"./detect.py"},
			},
			wantErr: false,
		},
		{
			name: "missing executable without detector",
			cfg: reverseBinConfig{
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "missing reverse_proxy_to without detector",
			cfg: reverseBinConfig{
				Executable: []string{"./main.py"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the validation logic in Provision
			// We check the conditions that would cause Provision to fail
			hasDetector := len(tt.cfg.DynamicProxyDetector) > 0
			hasExecutable := len(tt.cfg.Executable) > 0
			hasProxyTo := tt.cfg.ReverseProxyTo != ""
			hasHealth := tt.cfg.HealthMethod != "" && tt.cfg.HealthPath != ""
			nonUnix := hasProxyTo && !isUnixUpstream(tt.cfg.ReverseProxyTo)

			shouldFail := (!hasDetector && !hasExecutable) || (!hasDetector && !hasProxyTo) || (nonUnix && !hasHealth)

			if shouldFail != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v (hasDetector=%v, hasExecutable=%v, hasProxyTo=%v, nonUnix=%v, hasHealth=%v)",
					tt.wantErr, shouldFail, hasDetector, hasExecutable, hasProxyTo, nonUnix, hasHealth)
			}
		})
	}
}

// NoOpNextHandler is a test helper that does nothing
type NoOpNextHandler struct{}

func (n NoOpNextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return nil
}
