# Fix Detector Schema Module Graph Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `github.com/tarasglek/caddy-reverse-bin@v0.3.1` buildable by downstream `xcaddy` without local `replace` hacks, while keeping `../reverse-bin-detector` able to consume `github.com/tarasglek/caddy-reverse-bin/detectorschema` as independent module.

**Architecture:** Keep `detectorschema` as nested Go module because `../reverse-bin-detector` imports it directly. Fix bad `caddy-reverse-bin` module graph by requiring real tagged `detectorschema` version, not fake `v0.0.0`, and deleting local `replace`. Tag `detectorschema/v0.3.1` first, then tag `caddy-reverse-bin` `v0.3.1`.

**Tech Stack:** Go modules, nested module tags, Caddy, xcaddy, GitHub Actions, GoReleaser.

**Already done:** Release tag order documented and committed in `CONTRIBUTING.md` + `.goreleaser.yaml` as `d31b8f5 docs: document release tag order`.

---

### Task 1: Confirm current state and breakage

**Files:**
- No edits.

**Step 1: Check caddy-reverse-bin worktree**

Run:
```bash
cd /home/taras/Documents/caddy-reverse-bin
git status --short --branch
git log --oneline -5
```

Expected:
- branch is `main`
- release docs commit `d31b8f5` appears
- no modified tracked files before starting module fix
- untracked plan files may exist; leave them alone unless committing plan explicitly

**Step 2: Reproduce downstream xcaddy failure for v0.3.0**

Run:
```bash
rm -f /tmp/reverse-bin-caddy-broken
xcaddy build \
  --output /tmp/reverse-bin-caddy-broken \
  --with github.com/tarasglek/caddy-reverse-bin@v0.3.0
```

Expected: FAIL with:
```text
unknown revision detectorschema/v0.0.0
```

**Step 3: Confirm detector consumes detectorschema submodule**

Run:
```bash
cd /home/taras/Documents/reverse-bin-detector
rg -n 'caddy-reverse-bin/detectorschema' go.mod internal cmd
```

Expected: `go.mod` requires:
```go
github.com/tarasglek/caddy-reverse-bin/detectorschema v0.2.2
```

**Step 4: Return to caddy-reverse-bin**

Run:
```bash
cd /home/taras/Documents/caddy-reverse-bin
```

---

### Task 2: Add local downstream xcaddy check

**Files:**
- Create: `scripts/check-xcaddy-downstream.sh`

**Step 1: Write script**

```sh
#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
out=${TMPDIR:-/tmp}/caddy-reverse-bin-downstream-check
rm -f "$out"

xcaddy build \
  --output "$out" \
  --with "github.com/tarasglek/caddy-reverse-bin=$repo_root" \
  --replace "github.com/tarasglek/caddy-reverse-bin/detectorschema=$repo_root/detectorschema"

"$out" list-modules | grep -qx 'http.handlers.reverse-bin'
"$out" version
```

**Step 2: Make executable**

Run:
```bash
chmod +x scripts/check-xcaddy-downstream.sh
```

**Step 3: Run script**

Run:
```bash
./scripts/check-xcaddy-downstream.sh
```

Expected: PASS. This proves local checkout builds. Published-style check happens after `detectorschema/v0.3.1` exists.

**Step 4: Commit script only**

```bash
git add scripts/check-xcaddy-downstream.sh
git commit -m "test: add downstream xcaddy build check"
```

Do not include untracked plan files unless explicitly requested.

---

### Task 3: Tag detectorschema v0.3.1

**Files:**
- Keep: `detectorschema/go.mod`
- No code edits unless schema changed since `detectorschema/v0.2.2`.

**Step 1: Test detectorschema module**

Run:
```bash
cd /home/taras/Documents/caddy-reverse-bin/detectorschema
go test ./...
cd ..
```

Expected: PASS or `? ... [no test files]`.

**Step 2: Check detectorschema changes since previous schema tag**

Run:
```bash
git diff --stat detectorschema/v0.2.2..HEAD -- detectorschema
```

Expected: any schema diff visible. Tag `detectorschema/v0.3.1` even if diff empty, because `caddy-reverse-bin v0.3.1` will require that release-line version.

**Step 3: Create detectorschema tag locally**

Run:
```bash
git tag -a detectorschema/v0.3.1 -m "detectorschema v0.3.1"
```

Expected: tag created. If tag already exists locally, verify it points at intended commit:
```bash
git rev-parse detectorschema/v0.3.1
git rev-parse HEAD
```

**Step 4: Push detectorschema tag before tidy**

Run:
```bash
git push origin detectorschema/v0.3.1
```

Expected: push success. Needed so `go mod tidy` can resolve `detectorschema v0.3.1` without local replace.

---

### Task 4: Fix caddy-reverse-bin go.mod

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Edit caddy-reverse-bin `go.mod`**

Change:
```go
github.com/tarasglek/caddy-reverse-bin/detectorschema v0.0.0
```

to:
```go
github.com/tarasglek/caddy-reverse-bin/detectorschema v0.3.1
```

Delete local replace:
```go
replace github.com/tarasglek/caddy-reverse-bin/detectorschema => ./detectorschema
```

**Step 2: Tidy**

Run:
```bash
go clean -modcache
go mod tidy
```

Expected final `go.mod`:
- requires `github.com/tarasglek/caddy-reverse-bin/detectorschema v0.3.1`
- has no `replace` for `detectorschema`

**Step 3: Run caddy-reverse-bin tests**

Run:
```bash
go test ./...
```

Expected: PASS.

**Step 4: Run local downstream xcaddy check**

Run:
```bash
./scripts/check-xcaddy-downstream.sh
```

Expected: PASS.

**Step 5: Commit module graph fix**

```bash
git add go.mod go.sum
git commit -m "fix: require tagged detectorschema module"
```

---

### Task 5: Verify published-style caddy-reverse-bin build before release

**Files:**
- No edits.

**Step 1: Push caddy-reverse-bin commits**

Run:
```bash
git push origin main
```

Expected: push success.

**Step 2: Build by commit SHA with plain xcaddy**

Run:
```bash
tmp=$(mktemp -d)
cd "$tmp"
sha=$(git -C /home/taras/Documents/caddy-reverse-bin rev-parse HEAD)
GONOSUMDB=github.com/tarasglek/caddy-reverse-bin \
GOPRIVATE=github.com/tarasglek/caddy-reverse-bin \
xcaddy build \
  --output ./reverse-bin-caddy \
  --with github.com/tarasglek/caddy-reverse-bin@"$sha"
./reverse-bin-caddy list-modules | grep -qx 'http.handlers.reverse-bin'
./reverse-bin-caddy version
```

Expected: PASS. No `unknown revision detectorschema/v0.0.0`.

---

### Task 6: Verify reverse-bin-detector still works

**Files:**
- No edits in detector for this release.

**Step 1: Test existing pinned detector**

Run:
```bash
cd /home/taras/Documents/reverse-bin-detector
go test ./...
```

Expected: PASS. This proves old `detectorschema v0.2.2` still works.

**Step 2: Optional detector bump test only if needed**

Only if schema changes matter for detector now:
```bash
go get github.com/tarasglek/caddy-reverse-bin/detectorschema@v0.3.1
go test ./...
git checkout -- go.mod go.sum
```

Expected: PASS, then detector worktree restored. Skip this unless needed.

---

### Task 7: Release caddy-reverse-bin v0.3.1

**Files:**
- No edits unless CI needs fix.

**Step 1: Ensure clean caddy-reverse-bin worktree**

Run:
```bash
cd /home/taras/Documents/caddy-reverse-bin
git status --short
git log --oneline -5
```

Expected: no modified tracked files. Untracked plan files may remain; do not tag if unsure about intended release commit.

**Step 2: Ensure detectorschema tag exists on origin**

Run:
```bash
git ls-remote --tags origin 'refs/tags/detectorschema/v0.3.1'
```

Expected: tag exists.

**Step 3: Tag caddy-reverse-bin release**

Run:
```bash
git tag -a v0.3.1 -m "v0.3.1"
git push origin v0.3.1
```

Expected: push success. Pushing `v*` triggers GoReleaser.

**Step 4: Babysit GitHub Actions release**

Run:
```bash
gh run list --repo tarasglek/caddy-reverse-bin --limit 10
gh run watch --repo tarasglek/caddy-reverse-bin --exit-status
```

If fail:
```bash
gh run view <run-id> --repo tarasglek/caddy-reverse-bin --log-failed
```

Fix root cause. No guessing.

**Step 5: Verify release exists**

Run:
```bash
gh release view v0.3.1 --repo tarasglek/caddy-reverse-bin --json tagName,url,publishedAt
```

Expected: `tagName` is `v0.3.1`.

---

### Task 8: Update reverse-bin-hosting to consume v0.3.1

**Files:**
- Modify: `/home/taras/Documents/reverse-bin-hosting/packaging/runtime-versions.env`
- Modify: `/home/taras/Documents/reverse-bin-hosting/debian/changelog`

**Step 1: Remove failed workaround from reverse-bin-hosting if present**

Run:
```bash
cd /home/taras/Documents/reverse-bin-hosting
git diff -- packaging/runtime-versions.env debian/changelog
```

If `CADDY_REVERSE_BIN_REPLACE` exists, remove it. Set plugin only:
```env
CADDY_REVERSE_BIN_PLUGIN=github.com/tarasglek/caddy-reverse-bin@v0.3.1
```

**Step 2: Add Debian changelog entry**

Add top entry:
```text
reverse-bin (0.0.0-19) unstable; urgency=medium

  * chore(caddy): update caddy-reverse-bin plugin to v0.3.1.

 -- Taras Glek <taras@example.com>  Tue, 23 Jun 2026 00:00:00 +0000
```

Use real current timestamp if desired.

**Step 3: Build downstream Caddy**

Run:
```bash
make build
```

Expected: PASS. Output includes `http.handlers.reverse-bin`. No `unknown revision detectorschema/v0.0.0`.

**Step 4: Continue reverse-bin-hosting release flow**

Run package verification/release per `/home/taras/Documents/reverse-bin-hosting/RELESE-PROCESS.md` and project CI.

---

## Done Criteria

- `detectorschema/go.mod` still exists.
- Tag `detectorschema/v0.3.1` exists on origin.
- `caddy-reverse-bin` `go.mod` requires `github.com/tarasglek/caddy-reverse-bin/detectorschema v0.3.1`.
- `caddy-reverse-bin` `go.mod` has no local `replace` for `detectorschema`.
- `go test ./...` passes in `caddy-reverse-bin`.
- Plain `xcaddy --with github.com/tarasglek/caddy-reverse-bin@<sha-or-v0.3.1>` passes.
- `go test ./...` passes in `reverse-bin-detector` with current pinned `detectorschema v0.2.2`.
- GitHub release `v0.3.1` exists for `caddy-reverse-bin`.
- `reverse-bin-hosting make build` works with `caddy-reverse-bin@v0.3.1` and no replace workaround.
