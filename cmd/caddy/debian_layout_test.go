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
	acmeContent, err := os.ReadFile("../../packaging/debian/Caddyfile.acme")
	if err != nil {
		t.Fatalf("read packaged ACME Caddyfile: %v", err)
	}
	httpOnlyContent, err := os.ReadFile("../../packaging/debian/Caddyfile.http-only")
	if err != nil {
		t.Fatalf("read packaged HTTP-only Caddyfile: %v", err)
	}
	text := string(acmeContent) + "\n" + string(httpOnlyContent)
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

// TestPackagedCaddyfilesDocumentHostToAppMapping verifies packaged routing explains apex and subdomain app mapping.
func TestPackagedCaddyfilesDocumentHostToAppMapping(t *testing.T) {
	for _, path := range []string{
		"../../packaging/debian/Caddyfile.acme",
		"../../packaging/debian/Caddyfile.http-only",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read packaged Caddyfile %s: %v", path, err)
		}
		text := string(content)
		for _, want := range []string{
			"# Map request host to the app directory consumed by discover-app.py.",
			"# The apex domain has no subdomain label, so serve it from the conventional \"www\" app.",
			"map {host} {reverse_bin_app_dir}",
			"{$DOMAIN_SUFFIX} /var/lib/reverse-bin/apps/www",
			"default /var/lib/reverse-bin/apps/{http.request.host.labels.2}",
			`dynamic_proxy_detector "/usr/lib/reverse-bin/discover-app.py" "{reverse_bin_app_dir}"`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("packaged Caddyfile %s missing documented host mapping fragment %q", path, want)
			}
		}
	}
}

func TestPackagedCaddyfilesUseSandboxedDiscovery(t *testing.T) {
	for _, path := range []string{
		"../../packaging/debian/Caddyfile.acme",
		"../../packaging/debian/Caddyfile.http-only",
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read packaged Caddyfile %s: %v", path, err)
		}
		if strings.Contains(string(content), "--no-sandbox") {
			t.Fatalf("packaged Caddyfile %s must not disable app sandboxing", path)
		}
	}
}

func TestPackagedServiceUsesDebianPaths(t *testing.T) {
	content, err := os.ReadFile("../../packaging/debian/reverse-bin.service")
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"ExecStart=/usr/bin/reverse-bin-caddy run --config ${REVERSE_BIN_CADDYFILE} --adapter caddyfile",
		"WorkingDirectory=/var/lib/reverse-bin/home",
		"RuntimeDirectory=reverse-bin",
		"Environment=PATH=/usr/lib/reverse-bin:/usr/bin:/bin",
		"Environment=SOPS_AGE_KEY_FILE=/var/lib/reverse-bin/keys/age.key",
		"EnvironmentFile=-/etc/default/reverse-bin",
		"TimeoutStopSec=45s",
		"User=reverse-bin",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("service file missing %q", want)
		}
	}
}

// TestDebianPackagingBundlesSopsAndAge verifies encrypted env decryption does not rely on host-installed tools.
func TestDebianPackagingBundlesSopsAndAge(t *testing.T) {
	content, err := os.ReadFile("../../debian/install")
	if err != nil {
		t.Fatalf("read debian/install: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"build/sops usr/lib/reverse-bin/",
		"build/age usr/lib/reverse-bin/",
		"build/age-keygen usr/lib/reverse-bin/",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debian/install missing %q", want)
		}
	}

	rulesContent, err := os.ReadFile("../../debian/rules")
	if err != nil {
		t.Fatalf("read debian/rules: %v", err)
	}
	rules := string(rulesContent)
	for _, want := range []string{
		`install -m 0755 "$(shell command -v sops)" build/sops`,
		`install -m 0755 "$(shell command -v age)" build/age`,
		`install -m 0755 "$(shell command -v age-keygen)" build/age-keygen`,
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("debian/rules missing %q", want)
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

// TestDebianPostinstCreatesAppsSopsConfig verifies installs seed app secrets recipient config from the server public key.
func TestDebianPostinstCreatesAppsSopsConfig(t *testing.T) {
	content, err := os.ReadFile("../../debian/postinst")
	if err != nil {
		t.Fatalf("read postinst: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"if [ ! -f /var/lib/reverse-bin/apps/.sops.yaml ]; then",
		"# See /usr/share/doc/reverse-bin/README.md.gz section \"Encrypted app env files\".",
		"path_regex: secrets\\.enc\\.json$",
		"reverse-bin server key: /var/lib/reverse-bin/keys/age.pub",
		"sed 's/^/        - /' /var/lib/reverse-bin/keys/age.pub >> /var/lib/reverse-bin/apps/.sops.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("postinst missing %q", want)
		}
	}
}

// TestDebianPostinstCreatesAgeIdentity verifies the package creates but never overwrites the SOPS age keypair.
func TestDebianPostinstCreatesAgeIdentity(t *testing.T) {
	content, err := os.ReadFile("../../debian/postinst")
	if err != nil {
		t.Fatalf("read postinst: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"install -d -m 0755 -o reverse-bin -g reverse-bin /var/lib/reverse-bin/keys",
		"if [ ! -f /var/lib/reverse-bin/keys/age.key ]; then",
		"/usr/lib/reverse-bin/age-keygen -o /var/lib/reverse-bin/keys/age.key",
		"/usr/lib/reverse-bin/age-keygen -y /var/lib/reverse-bin/keys/age.key > /var/lib/reverse-bin/keys/age.pub",
		"chown reverse-bin:reverse-bin /var/lib/reverse-bin/keys/age.key",
		"chmod 600 /var/lib/reverse-bin/keys/age.key",
		"chmod 644 /var/lib/reverse-bin/keys/age.pub",
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
