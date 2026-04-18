package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDebBuildContainsExpectedPaths verifies the .deb installs the approved runtime layout.
func TestDebBuildContainsExpectedPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping package build test in short mode")
	}

	cmd := exec.Command("dpkg-buildpackage", "-us", "-uc", "-b")
	cmd.Dir = filepath.Clean(filepath.Join("..", ".."))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-buildpackage failed: %v\n%s", err, output)
	}

	matches, err := filepath.Glob(filepath.Clean(filepath.Join("..", "..", "..", "reverse-bin_*_*.deb")))
	if err != nil || len(matches) == 0 {
		t.Fatalf("expected built .deb, got err=%v matches=%v", err, matches)
	}

	debPath, err := filepath.Abs(matches[0])
	if err != nil {
		t.Fatalf("resolve built .deb path: %v", err)
	}

	inspect := exec.Command("dpkg-deb", "-c", debPath)
	listing, err := inspect.CombinedOutput()
	if err != nil {
		t.Fatalf("dpkg-deb -c failed: %v\n%s", err, listing)
	}
	for _, want := range []string{
		"./usr/bin/reverse-bin-caddy",
		"./usr/lib/reverse-bin/discover-app.py",
		"./usr/lib/reverse-bin/allow-domain.py",
		"./etc/reverse-bin/Caddyfile",
		"./usr/share/doc/reverse-bin/examples/",
	} {
		if !strings.Contains(string(listing), want) {
			t.Fatalf("package listing missing %q\n%s", want, listing)
		}
	}
	if !strings.Contains(string(listing), "./lib/systemd/system/reverse-bin.service") && !strings.Contains(string(listing), "./usr/lib/systemd/system/reverse-bin.service") {
		t.Fatalf("package listing missing systemd service path\n%s", listing)
	}
}
