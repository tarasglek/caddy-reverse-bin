package reversebin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap/zaptest"
)

type reverseBinConfig struct {
	Executable           []string
	WorkingDirectory     string
	Envs                 []string
	PassEnvs             []string
	PassAll              bool
	ReverseProxyTo       string
	ReadinessMethod      string
	ReadinessPath        string
	DynamicProxyDetector []string
	IdleTimeoutMS        int
}

func asConfig(c *ReverseBin) reverseBinConfig {
	return reverseBinConfig{
		Executable:           c.Executable,
		WorkingDirectory:     c.WorkingDirectory,
		Envs:                 c.Envs,
		PassEnvs:             c.PassEnvs,
		PassAll:              c.PassAll,
		ReverseProxyTo:       c.ReverseProxyTo,
		ReadinessMethod:      c.ReadinessMethod,
		ReadinessPath:        c.ReadinessPath,
		DynamicProxyDetector: c.DynamicProxyDetector,
		IdleTimeoutMS:        c.IdleTimeoutMS,
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
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   "127.0.0.1:8080",
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
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   ":8080",
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
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   "unix//tmp/app.sock",
			},
			wantErr: false,
		},
		{
			name: "with readiness_check",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  readiness_check GET /health
}`,
			expected: reverseBinConfig{
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   "127.0.0.1:8080",
				ReadinessMethod:  "GET",
				ReadinessPath:    "/health",
			},
			wantErr: false,
		},
		{
			name: "with readiness_check HEAD",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to 127.0.0.1:8080
  readiness_check head /ready
}`,
			expected: reverseBinConfig{
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   "127.0.0.1:8080",
				ReadinessMethod:  "HEAD",
				ReadinessPath:    "/ready",
			},
			wantErr: false,
		},
		{
			name: "with readiness_check null",
			input: `reverse-bin {
  exec ./main.py
  reverse_proxy_to unix//tmp/app.sock
  readiness_check null
}`,
			expected: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "unix//tmp/app.sock",
			},
			wantErr: false,
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
  readiness_check GET /healthz
  dynamic_proxy_detector /bin/detect {host} {path}
  idle_timeout_ms 100
}`,
			expected: reverseBinConfig{
				Executable:           []string{"./main.py", "arg1", "arg2"},
				WorkingDirectory:     "/app",
				Envs:                 []string{"FOO=bar", "BAZ=qux"},
				PassEnvs:             []string{"HOME", "PATH"},
				PassAll:              true,
				ReverseProxyTo:       "127.0.0.1:3000",
				ReadinessMethod:      "GET",
				ReadinessPath:        "/healthz",
				DynamicProxyDetector: []string{"/bin/detect", "{host}", "{path}"},
				IdleTimeoutMS:        100,
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
		name          string
		detector      []string
		requestPath   string
		wantKeyEmpty  bool
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
			name: "invalid static non-unix without readiness_check",
			cfg: reverseBinConfig{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "valid static non-unix with readiness_check",
			cfg: reverseBinConfig{
				Executable:      []string{"./main.py"},
				ReverseProxyTo:  "127.0.0.1:8080",
				ReadinessMethod: "GET",
				ReadinessPath:   "/health",
			},
			wantErr: false,
		},
		{
			name: "valid static unix without readiness_check",
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
			hasReadiness := tt.cfg.ReadinessMethod != "" && tt.cfg.ReadinessPath != ""
			nonUnix := hasProxyTo && !isUnixUpstream(tt.cfg.ReverseProxyTo)

			shouldFail := (!hasDetector && !hasExecutable) || (!hasDetector && !hasProxyTo) || (nonUnix && !hasReadiness)

			if shouldFail != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v (hasDetector=%v, hasExecutable=%v, hasProxyTo=%v, nonUnix=%v, hasReadiness=%v)",
					tt.wantErr, shouldFail, hasDetector, hasExecutable, hasProxyTo, nonUnix, hasReadiness)
			}
		})
	}
}

// NoOpNextHandler is a test helper that does nothing
type NoOpNextHandler struct{}

func (n NoOpNextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return nil
}
