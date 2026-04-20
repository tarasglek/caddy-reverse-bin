# Debian defaults config Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a Debian-native `/etc/default/reverse-bin` configuration path for `OPS_EMAIL` and `DOMAIN_SUFFIX` and wire the packaged systemd unit to load it.

**Architecture:** Keep the packaged Caddyfile templated with `{$OPS_EMAIL}` and `{$DOMAIN_SUFFIX}`. Add a packaged defaults file installed at `/etc/default/reverse-bin`, then update the systemd unit to read it through `EnvironmentFile=-/etc/default/reverse-bin`.

**Tech Stack:** Go tests, Debian packaging metadata, systemd unit files, Markdown docs.

---

### Task 1: Add failing packaging tests for the defaults file

**Files:**
- Modify: `cmd/caddy/debian_layout_test.go`
- Modify: `cmd/caddy/debian_packaging_test.go`

**Step 1: Write the failing tests**

Add assertions that the packaged service contains `EnvironmentFile=-/etc/default/reverse-bin` and that the built package contains `./etc/default/reverse-bin`.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/caddy -run 'TestPackagedServiceUsesDebianPaths|TestDebBuildContainsExpectedPaths' -v`
Expected: FAIL because the service file and package listing do not yet include `/etc/default/reverse-bin`.

**Step 3: Write minimal implementation**

No implementation in this task.

**Step 4: Run test to verify it still reflects the missing behavior**

Run: `go test ./cmd/caddy -run 'TestPackagedServiceUsesDebianPaths|TestDebBuildContainsExpectedPaths' -v`
Expected: FAIL for the new assertions.

**Step 5: Commit**

Commit after implementation tasks are complete.

### Task 2: Add the packaged defaults file and wire the service to load it

**Files:**
- Create: `packaging/debian/reverse-bin.default`
- Modify: `debian/install`
- Modify: `debian/reverse-bin.service`
- Modify: `packaging/debian/reverse-bin.service`

**Step 1: Write minimal implementation**

Create a defaults file with commented guidance plus concrete default keys:

```sh
OPS_EMAIL=admin@example.com
DOMAIN_SUFFIX=example.com
```

Update both service files to include:

```ini
EnvironmentFile=-/etc/default/reverse-bin
```

Add the defaults file to `debian/install` so it lands at `etc/default/reverse-bin`.

**Step 2: Run focused tests**

Run: `go test ./cmd/caddy -run 'TestPackagedServiceUsesDebianPaths' -v`
Expected: PASS

**Step 3: Run package-content test**

Run: `go test ./cmd/caddy -run 'TestDebBuildContainsExpectedPaths' -v`
Expected: PASS

**Step 4: Commit**

```bash
git add packaging/debian/reverse-bin.default packaging/debian/reverse-bin.service debian/reverse-bin.service debian/install cmd/caddy/debian_layout_test.go cmd/caddy/debian_packaging_test.go
git commit -m "feat(packaging): add defaults-based config"
```

### Task 3: Update docs and changelog

**Files:**
- Modify: `README.md`
- Modify: `doc/document.md`
- Modify: `debian/changelog`

**Step 1: Update docs**

Document `/etc/default/reverse-bin` as the supported admin configuration path and show restart instructions.

**Step 2: Bump Debian changelog**

Add a new top entry describing the defaults-based configuration support.

**Step 3: Run verification**

Run: `go test ./cmd/caddy -v`
Expected: PASS

**Step 4: Commit**

```bash
git add README.md doc/document.md debian/changelog
git commit -m "docs(packaging): document defaults config"
```
