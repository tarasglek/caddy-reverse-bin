package main

import (
	"os"
	"strings"
	"testing"
)

// TestDebianLayoutConstants documents the package-owned install paths.
func TestDebianLayoutConstants(t *testing.T) {
	layout := DebianLayout()

	if layout.BinaryPath != "/usr/bin/reverse-bin-caddy" {
		t.Fatalf("binary path = %q, want %q", layout.BinaryPath, "/usr/bin/reverse-bin-caddy")
	}
	if layout.ConfigPath != "/etc/reverse-bin/Caddyfile" {
		t.Fatalf("config path = %q, want %q", layout.ConfigPath, "/etc/reverse-bin/Caddyfile")
	}
	if layout.AppRoot != "/var/lib/reverse-bin/apps" {
		t.Fatalf("app root = %q, want %q", layout.AppRoot, "/var/lib/reverse-bin/apps")
	}
}

// TestPackagedCaddyfileUsesDebianPaths verifies the packaged config uses the approved absolute paths.
func TestPackagedCaddyfileUsesDebianPaths(t *testing.T) {
	content, err := os.ReadFile("../../packaging/debian/Caddyfile")
	if err != nil {
		t.Fatalf("read packaged Caddyfile: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"/usr/lib/reverse-bin/allow-domain.py",
		"/usr/lib/reverse-bin/discover-app.py",
		"/var/lib/reverse-bin/apps",
		"/run/reverse-bin",
		"idle_timeout_ms {$REVERSE_BIN_IDLE_TIMEOUT_MS:300000}",
		"health_timeout_ms {$REVERSE_BIN_HEALTH_TIMEOUT_MS:15000}",
		"termination_grace_ms {$REVERSE_BIN_TERMINATION_GRACE_MS:5000}",
		"termination_kill_wait_ms {$REVERSE_BIN_TERMINATION_KILL_WAIT_MS:1000}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("packaged Caddyfile missing %q", want)
		}
	}
}

// TestPackagedServiceUsesDebianPaths verifies the packaged service uses the approved binary, PATH, and home dir.
func TestPackagedServiceUsesDebianPaths(t *testing.T) {
	content, err := os.ReadFile("../../packaging/debian/reverse-bin.service")
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"ExecStart=/usr/bin/reverse-bin-caddy run --config /etc/reverse-bin/Caddyfile --adapter caddyfile",
		"WorkingDirectory=/var/lib/reverse-bin/home",
		"RuntimeDirectory=reverse-bin",
		"Environment=PATH=/usr/lib/reverse-bin:/usr/bin:/bin",
		"EnvironmentFile=-/etc/default/reverse-bin",
		"TimeoutStopSec=45s",
		"User=reverse-bin",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("service file missing %q", want)
		}
	}
}

// TestDebianPostinstReloadsSystemdAndRestartsEnabledService verifies package upgrades pick up unit changes without starting disabled installs.
func TestDebianPostinstReloadsSystemdAndRestartsEnabledService(t *testing.T) {
	content, err := os.ReadFile("../../debian/postinst")
	if err != nil {
		t.Fatalf("read postinst: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"if command -v systemctl >/dev/null; then",
		"systemctl daemon-reload",
		"if systemctl is-enabled --quiet reverse-bin.service; then",
		"systemctl restart reverse-bin.service",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("postinst missing %q", want)
		}
	}
}

// TestPackagedDefaultFileDefinesLifecycleDefaults verifies shared lifecycle defaults are configurable in one place.
func TestPackagedDefaultFileDefinesLifecycleDefaults(t *testing.T) {
	content, err := os.ReadFile("../../packaging/debian/reverse-bin")
	if err != nil {
		t.Fatalf("read defaults file: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"REVERSE_BIN_IDLE_TIMEOUT_MS=300000",
		"REVERSE_BIN_HEALTH_TIMEOUT_MS=15000",
		"REVERSE_BIN_TERMINATION_GRACE_MS=5000",
		"REVERSE_BIN_TERMINATION_KILL_WAIT_MS=1000",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("defaults file missing %q", want)
		}
	}
}
