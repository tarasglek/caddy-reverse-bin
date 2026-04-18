package main

import "testing"

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
