# Runtime Directory Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ensure the Debian-packaged `reverse-bin` systemd service always gets `/run/reverse-bin` created automatically at startup.

**Architecture:** Fix the bug at the systemd unit layer by declaring `RuntimeDirectory=reverse-bin` in both service unit sources. Cover the behavior with a packaging/unit test that asserts the packaged service contains the runtime directory directive, then make the minimal unit-file change to satisfy that test.

**Tech Stack:** Go tests, systemd unit files, Debian packaging metadata.

---

### Task 1: Add failing coverage for the runtime directory requirement

**Files:**
- Modify: `cmd/caddy/debian_layout_test.go`
- Test: `cmd/caddy/debian_layout_test.go`

**Step 1: Write the failing test**

Add `RuntimeDirectory=reverse-bin` to the expected service directives in `TestPackagedServiceUsesDebianPaths`.

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/caddy -run TestPackagedServiceUsesDebianPaths -v`
Expected: FAIL because the packaged service file does not yet declare `RuntimeDirectory=reverse-bin`.

**Step 3: Commit**

Do not commit yet; wait until the implementation change makes the test pass.

### Task 2: Add the systemd runtime directory directive

**Files:**
- Modify: `packaging/debian/reverse-bin.service`
- Modify: `debian/reverse-bin.service`
- Test: `cmd/caddy/debian_layout_test.go`

**Step 1: Write minimal implementation**

Add this line under the existing working-directory setting in both service files:

```ini
RuntimeDirectory=reverse-bin
```

**Step 2: Run test to verify it passes**

Run: `go test ./cmd/caddy -run TestPackagedServiceUsesDebianPaths -v`
Expected: PASS.

**Step 3: Run focused packaging verification**

Run: `go test ./cmd/caddy -run 'TestPackagedServiceUsesDebianPaths|TestDebBuildContainsExpectedPaths' -v`
Expected: PASS.

**Step 4: Commit**

```bash
git add cmd/caddy/debian_layout_test.go packaging/debian/reverse-bin.service debian/reverse-bin.service docs/plans/2026-04-20-runtime-directory-fix.md
git commit -m "fix(packaging): create reverse-bin runtime dir"
```
