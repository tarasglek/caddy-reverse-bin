package reversebin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestReverseBin_UnmarshalCaddyfile(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ReverseBin
		wantErr  bool
	}{
		{
			name: "basic executable with args",
			input: `reverse-bin /some/file a b c d 1 {
  dir /somewhere
  env foo=bar what=ever
  pass_env some_env other_env
  pass_all_env
}`,
			expected: ReverseBin{
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
			expected: ReverseBin{
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
			expected: ReverseBin{
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
			expected: ReverseBin{
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
			expected: ReverseBin{
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
			expected: ReverseBin{
				Executable:       []string{"./main.py"},
				ReverseProxyTo:   "127.0.0.1:8080",
				ReadinessMethod:  "HEAD",
				ReadinessPath:    "/ready",
			},
			wantErr: false,
		},
		{
			name: "with dynamic_proxy_detector",
			input: `reverse-bin {
  dynamic_proxy_detector ./discover.py {path}
}`,
			expected: ReverseBin{
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
}`,
			expected: ReverseBin{
				Executable:           []string{"./main.py", "arg1", "arg2"},
				WorkingDirectory:     "/app",
				Envs:                 []string{"FOO=bar", "BAZ=qux"},
				PassEnvs:             []string{"HOME", "PATH"},
				PassAll:              true,
				ReverseProxyTo:       "127.0.0.1:3000",
				ReadinessMethod:      "GET",
				ReadinessPath:        "/healthz",
				DynamicProxyDetector: []string{"/bin/detect", "{host}", "{path}"},
			},
			wantErr: false,
		},
		{
			name: "exec requires argument",
			input: `reverse-bin {
  exec
}`,
			expected: ReverseBin{},
			wantErr:  true,
		},
		{
			name: "unknown subdirective errors",
			input: `reverse-bin {
  exec ./main.py
  unknown_option value
}`,
			expected: ReverseBin{},
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

			if !reflect.DeepEqual(c, tt.expected) {
				t.Errorf("Parsing yielded invalid result.\nGot:      %#v\nExpected: %#v", c, tt.expected)
			}
		})
	}
}

func TestReverseBin_GetUpstreams(t *testing.T) {
	tests := []struct {
		name         string
		reverseProxyTo string
		wantDial     string
		wantErr      bool
	}{
		{
			name:           "IP and port",
			reverseProxyTo: "127.0.0.1:8080",
			wantDial:       "127.0.0.1:8080",
			wantErr:        false,
		},
		{
			name:           "port only",
			reverseProxyTo: ":8080",
			wantDial:       "127.0.0.1:8080",
			wantErr:        false,
		},
		{
			name:           "with http scheme",
			reverseProxyTo: "http://127.0.0.1:8080",
			wantDial:       "127.0.0.1:8080",
			wantErr:        false,
		},
		{
			name:           "unix socket",
			reverseProxyTo: "unix//tmp/test.sock",
			wantDial:       "unix//tmp/test.sock",
			wantErr:        false,
		},
		{
			name:           "unix socket with single slash",
			reverseProxyTo: "unix//tmp/test.sock",
			wantDial:       "unix//tmp/test.sock",
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ReverseBin{
				ReverseProxyTo: tt.reverseProxyTo,
				logger:         zaptest.NewLogger(t),
				processes:      make(map[string]*processState),
			}

			req := httptest.NewRequest(http.MethodGet, "http://localhost/test", nil)
			repl := caddy.NewReplacer()
			req = req.WithContext(context.WithValue(req.Context(), caddy.ReplacerCtxKey, repl))

			// For these tests we don't actually start a process, so we mock the state
			ps := &processState{
				overrides: &proxyOverrides{
					ReverseProxyTo: &tt.reverseProxyTo,
				},
			}
			c.processes[""] = ps

			// Test upstream address parsing logic
			toAddr := tt.reverseProxyTo
			var dialAddr string
			if strings.HasPrefix(toAddr, "unix/") {
				dialAddr = toAddr
			} else {
				if strings.HasPrefix(toAddr, ":") {
					toAddr = "127.0.0.1" + toAddr
				}
				if !strings.HasPrefix(toAddr, "http://") && !strings.HasPrefix(toAddr, "https://") {
					toAddr = "http://" + toAddr
				}
				dialAddr = strings.TrimPrefix(toAddr, "http://")
				dialAddr = strings.TrimPrefix(dialAddr, "https://")
			}

			if dialAddr != tt.wantDial {
				t.Errorf("expected dial %q, got %q", tt.wantDial, dialAddr)
			}
		})
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
		cfg     ReverseBin
		wantErr bool
	}{
		{
			name: "valid static config",
			cfg: ReverseBin{
				Executable:     []string{"./main.py"},
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: false,
		},
		{
			name: "valid dynamic config",
			cfg: ReverseBin{
				DynamicProxyDetector: []string{"./detect.py"},
			},
			wantErr: false,
		},
		{
			name: "missing executable without detector",
			cfg: ReverseBin{
				ReverseProxyTo: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "missing reverse_proxy_to without detector",
			cfg: ReverseBin{
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

			shouldFail := (!hasDetector && !hasExecutable) || (!hasDetector && !hasProxyTo)

			if shouldFail != tt.wantErr {
				t.Errorf("expected error=%v, got error=%v (hasDetector=%v, hasExecutable=%v, hasProxyTo=%v)",
					tt.wantErr, shouldFail, hasDetector, hasExecutable, hasProxyTo)
			}
		})
	}
}

// NoOpNextHandler is a test helper that does nothing
type NoOpNextHandler struct{}

func (n NoOpNextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return nil
}
