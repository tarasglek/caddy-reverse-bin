# Debian Package Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Debian-native `reverse-bin` package that replaces the current bundle deployment model with a package-installed binary, config, service, helper assets, example apps, and writable state under `/var/lib/reverse-bin`.

**Architecture:** Add Debian packaging metadata under `debian/`, add package-owned runtime assets under a new package layout source directory, update docs/tests to use Debian absolute paths, and remove bundle-oriented deployment artifacts. The package installs `/usr/bin/reverse-bin-caddy`, helper assets in `/usr/lib/reverse-bin`, a conffile at `/etc/reverse-bin/Caddyfile`, examples under `/usr/share/doc/reverse-bin/examples`, and a systemd service that runs as the `reverse-bin` system user.

**Tech Stack:** Go, Python helper scripts, Debian packaging (`debhelper`, `dh_systemd`), systemd, file capabilities, existing repo tests.

---

### Task 1: Inventory bundle assumptions before changing packaging

**Files:**
- Inspect: `examples/reverse-bin-apps-config/README.md`
- Inspect: `examples/reverse-bin-apps-config/Caddyfile`
- Inspect: `examples/reverse-bin-apps-config/run.sh`
- Inspect: `examples/reverse-bin-apps-config/setup-systemd.py`
- Inspect: `examples/reverse-proxy/Caddyfile`
- Inspect: `utils/discover-app/discover-app.py`
- Inspect: `cmd/caddy/integration_test.go`

**Step 1: Write the failing test**

Add a new packaging-path test file:

- Create: `cmd/caddy/debian_layout_test.go`

Start with a focused table-driven test that documents the approved Debian paths.

```go
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
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run TestDebianLayoutConstants -v
```

Expected: FAIL because `DebianLayout` does not exist yet.

**Step 3: Write minimal implementation**

- Create: `cmd/caddy/debian_layout.go`

```go
package main

type Layout struct {
	BinaryPath string
	ConfigPath string
	AppRoot    string
	HomeDir    string
	RuntimeDir string
	LibexecDir string
}

func DebianLayout() Layout {
	return Layout{
		BinaryPath: "/usr/bin/reverse-bin-caddy",
		ConfigPath: "/etc/reverse-bin/Caddyfile",
		AppRoot:    "/var/lib/reverse-bin/apps",
		HomeDir:    "/var/lib/reverse-bin/home",
		RuntimeDir: "/run/reverse-bin",
		LibexecDir: "/usr/lib/reverse-bin",
	}
}
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run TestDebianLayoutConstants -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add cmd/caddy/debian_layout.go cmd/caddy/debian_layout_test.go
git commit -m "feat(packaging): add Debian layout constants"
```

### Task 2: Generate package-owned Caddyfile and service assets from one source of truth

**Files:**
- Create: `packaging/debian/Caddyfile`
- Create: `packaging/debian/reverse-bin.service`
- Modify: `cmd/caddy/debian_layout.go`
- Test: `cmd/caddy/debian_layout_test.go`

**Step 1: Write the failing test**

Add tests that assert the packaged Caddyfile and service file use the approved Debian absolute paths.

```go
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
		"Environment=PATH=/usr/lib/reverse-bin:/usr/bin:/bin",
		"User=reverse-bin",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("service file missing %q", want)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run 'TestPackaged(Caddyfile|Service)UsesDebianPaths' -v
```

Expected: FAIL because the package-owned files do not exist yet.

**Step 3: Write minimal implementation**

Create package-owned source files with the approved Debian paths.
Keep them static and explicit.
Do not add wrapper scripts.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run 'TestPackaged(Caddyfile|Service)UsesDebianPaths' -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add packaging/debian/Caddyfile packaging/debian/reverse-bin.service cmd/caddy/debian_layout_test.go
git commit -m "feat(packaging): add packaged service assets"
```

### Task 3: Add Debian package metadata and install rules

**Files:**
- Create: `debian/control`
- Create: `debian/changelog`
- Create: `debian/compat` or `debian/debhelper-compat`
- Create: `debian/rules`
- Create: `debian/source/format`
- Create: `debian/install`
- Create: `debian/docs`
- Create: `debian/conffiles` (only if needed by packaging strategy)
- Create: `debian/reverse-bin.service`
- Create: `debian/postinst`
- Create: `debian/postrm`
- Create: `debian/preinst` (only if needed for user creation strategy)
- Test: `cmd/caddy/debian_packaging_test.go`

**Step 1: Write the failing test**

Create a shell-out integration test that stages the package and inspects the built `.deb` contents.

```go
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
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run TestDebBuildContainsExpectedPaths -v
```

Expected: FAIL because `debian/` packaging metadata does not exist yet.

**Step 3: Write minimal implementation**

Add standard Debian packaging files.
Install these assets:

- `/usr/bin/reverse-bin-caddy`
- `/usr/lib/reverse-bin/discover-app.py`
- `/usr/lib/reverse-bin/allow-domain.py`
- `/usr/lib/reverse-bin/landrun`
- `/etc/reverse-bin/Caddyfile`
- `/usr/share/doc/reverse-bin/examples/...`
- systemd service unit

The package scripts should:

- create the `reverse-bin` system user/group
- create `/var/lib/reverse-bin`, `/var/lib/reverse-bin/apps`, `/var/lib/reverse-bin/home`
- set ownership to `reverse-bin:reverse-bin`
- apply `cap_net_bind_service=+ep` to `/usr/bin/reverse-bin-caddy`
- avoid enabling/starting the service automatically

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run TestDebBuildContainsExpectedPaths -v
```

Expected: PASS.

Then inspect the built package manually:

```bash
dpkg-deb -c ../reverse-bin_*_*.deb
```

Expected entries include:

- `./usr/bin/reverse-bin-caddy`
- `./usr/lib/reverse-bin/discover-app.py`
- `./etc/reverse-bin/Caddyfile`
- `./usr/lib/systemd/system/reverse-bin.service`
- `./usr/share/doc/reverse-bin/examples/`

**Step 5: Commit**

```bash
git add debian packaging/debian cmd/caddy/debian_packaging_test.go
git commit -m "feat(packaging): add Debian package metadata"
```

### Task 4: Replace bundle-oriented docs and examples with Debian-first docs

**Files:**
- Modify: `README.md`
- Modify: `doc/document.md`
- Modify: `CONTRIBUTING.md`
- Modify: `RELESE-PROCESS.md` (only if release artifacts need `.deb` coverage)
- Delete or replace: `examples/reverse-bin-apps-config/README.md`
- Delete or replace: `examples/reverse-bin-apps-config/Caddyfile`
- Delete or replace: `examples/reverse-bin-apps-config/run.sh`
- Delete or replace: `examples/reverse-bin-apps-config/setup-systemd.py`
- Add: `packaging/examples/python3-unix-echo/...`
- Test: `cmd/caddy/debian_docs_test.go`

**Step 1: Write the failing test**

Create a doc-path test that asserts public docs describe the Debian deployment layout, not the bundle layout.

```go
// TestReadmeReferencesDebianPaths verifies the README points to the package-first deployment layout.
func TestReadmeReferencesDebianPaths(t *testing.T) {
	content, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"/usr/bin/reverse-bin-caddy",
		"/etc/reverse-bin/Caddyfile",
		"/var/lib/reverse-bin/apps/",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("README missing %q", want)
		}
	}
	for _, banned := range []string{".bin/run.sh", "$HOME/reverse-bin"} {
		if strings.Contains(text, banned) {
			t.Fatalf("README still references obsolete bundle path %q", banned)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run TestReadmeReferencesDebianPaths -v
```

Expected: FAIL because current docs still describe the old layout.

**Step 3: Write minimal implementation**

Update docs to describe:

- package installation
- config at `/etc/reverse-bin/Caddyfile`
- app deployment under `/var/lib/reverse-bin/apps/`
- manual enable/start flow
- example apps shipped as package docs/examples

Delete or rewrite bundle-specific deployment examples so they no longer represent the recommended deployment path.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run TestReadmeReferencesDebianPaths -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add README.md doc/document.md CONTRIBUTING.md RELESE-PROCESS.md examples/reverse-bin-apps-config packaging/examples cmd/caddy/debian_docs_test.go
git commit -m "docs(packaging): document Debian deployment"
```

### Task 5: Add package examples and verify example layout

**Files:**
- Create: `packaging/examples/python3-unix-echo/.env`
- Create: `packaging/examples/python3-unix-echo/README.md`
- Create: `packaging/examples/python3-unix-echo/main.py`
- Create: `packaging/examples/python3-unix-echo/run.py` (only if still needed)
- Possibly add: `packaging/examples/deno-echo/...`
- Test: `cmd/caddy/debian_examples_test.go`

**Step 1: Write the failing test**

Create a test that asserts the packaged example source tree is suitable for installation into `/usr/share/doc/reverse-bin/examples`.

```go
// TestPackagedExampleContainsMinimalAppFiles verifies the documented example app is complete.
func TestPackagedExampleContainsMinimalAppFiles(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "packaging", "examples", "python3-unix-echo"))
	for _, name := range []string{".env", "README.md", "main.py"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("missing packaged example file %q: %v", name, err)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run TestPackagedExampleContainsMinimalAppFiles -v
```

Expected: FAIL because the package-specific example tree does not exist yet.

**Step 3: Write minimal implementation**

Create a package-owned examples source tree based on the existing minimal app example.
Make sure `.env` and docs use Debian deployment wording.

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run TestPackagedExampleContainsMinimalAppFiles -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add packaging/examples cmd/caddy/debian_examples_test.go
git commit -m "feat(packaging): add package example apps"
```

### Task 6: Add package build target and verification commands

**Files:**
- Modify: `Makefile`
- Possibly create: `scripts/build-deb.sh`
- Test: `cmd/caddy/debian_packaging_test.go`

**Step 1: Write the failing test**

Add a test that verifies the documented build command exists in the Makefile.

```go
// TestMakefileContainsDebBuildTarget verifies the repo exposes a standard .deb build command.
func TestMakefileContainsDebBuildTarget(t *testing.T) {
	content, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "deb:") {
		t.Fatalf("Makefile missing deb target")
	}
	if !strings.Contains(text, "dpkg-buildpackage") {
		t.Fatalf("Makefile missing dpkg-buildpackage invocation")
	}
}
```

**Step 2: Run test to verify it fails**

Run:

```bash
go test ./cmd/caddy -run TestMakefileContainsDebBuildTarget -v
```

Expected: FAIL because the target does not exist yet.

**Step 3: Write minimal implementation**

Add a `deb` target to `Makefile` that builds the package with `dpkg-buildpackage`.
Keep the target small and obvious.

Example:

```make
.PHONY: deb

deb:
	dpkg-buildpackage -us -uc -b
```

**Step 4: Run test to verify it passes**

Run:

```bash
go test ./cmd/caddy -run TestMakefileContainsDebBuildTarget -v
```

Expected: PASS.

Then run the target:

```bash
make deb
```

Expected: a `.deb` file is produced in the parent directory.

**Step 5: Commit**

```bash
git add Makefile cmd/caddy/debian_packaging_test.go
git commit -m "build(packaging): add deb build target"
```

### Task 7: Verify full package build and installation behavior

**Files:**
- Verify: built `.deb` artifact
- Verify: `debian/postinst`
- Verify: `debian/postrm`
- Verify: `debian/reverse-bin.service`
- Verify: docs and examples

**Step 1: Build and inspect the package**

Run:

```bash
make deb
```

Expected: `.deb` artifact is produced successfully.

Run:

```bash
dpkg-deb -c ../reverse-bin_*_*.deb
```

Expected package contents include exactly the Debian-first layout.

**Step 2: Verify service metadata**

Run:

```bash
dpkg-deb -x ../reverse-bin_*_*.deb /tmp/reverse-bin-deb-root
find /tmp/reverse-bin-deb-root -maxdepth 4 | sort
```

Expected:

- `/tmp/reverse-bin-deb-root/usr/bin/reverse-bin-caddy`
- `/tmp/reverse-bin-deb-root/etc/reverse-bin/Caddyfile`
- `/tmp/reverse-bin-deb-root/usr/lib/reverse-bin/`
- `/tmp/reverse-bin-deb-root/usr/lib/systemd/system/reverse-bin.service`
- `/tmp/reverse-bin-deb-root/usr/share/doc/reverse-bin/examples/`

**Step 3: Verify maintainer script behavior**

Review the maintainer scripts to confirm they:

- create `reverse-bin` user/group
- create `/var/lib/reverse-bin/apps` and `/var/lib/reverse-bin/home`
- set ownership correctly
- apply `cap_net_bind_service=+ep`
- do not auto-enable or auto-start the service

**Step 4: Run repo tests**

Run:

```bash
go test ./...
```

Expected: PASS.

**Step 5: Commit final verification-only adjustments**

If verification required changes:

```bash
git add <exact files changed>
git commit -m "fix(packaging): align Debian package verification"
```

If no files changed, do not create an extra commit.
