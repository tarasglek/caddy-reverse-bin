# Repo Split Hosting Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Split the project so `caddy-reverse-bin` contains only the Caddy plugin and plugin tests, while a new sibling repo `reverse-bin-hosting` owns the opinionated Debian/hosting product.

**Architecture:** Keep a clean dependency boundary: the plugin repo publishes/builds the Caddy module, and the hosting repo consumes that module when assembling its packaged Caddy binary. Hosting-specific packaging, systemd, bundled runtime, deployment docs, and app examples move out of the plugin repo. The plugin repo keeps focused Go source, Caddy directive documentation, and Go tests.

**Tech Stack:** Go, Caddy modules, Git, Debian packaging, systemd, GitHub Actions.

---

## Design

### Repository responsibilities

`caddy-reverse-bin` remains the Caddy plugin repository.

It owns:
- Caddy module registration and handler implementation.
- Caddyfile directive behavior.
- Process launch/proxy behavior that is intrinsic to the plugin.
- Unit and integration tests for plugin behavior.
- Go lint/test CI.
- A minimal README for plugin usage and development.

`reverse-bin-hosting` becomes a new sibling repository.

It owns:
- Debian packaging and release flow.
- systemd units and `/etc/default/reverse-bin` conventions.
- Packaged Caddyfiles for ACME and HTTP-only deployments.
- Bundled runtime/helper assumptions under `/usr/lib/reverse-bin`.
- Opinionated app directory layout under `/var/lib/reverse-bin/apps`.
- Deployment examples and hosted-app documentation.

### Boundary contract

The hosting repo depends on the plugin repo; the plugin repo must not depend on the hosting repo.

The stable interface between them is:
- Go module path for the Caddy plugin, currently `github.com/tarasglek/reverse-bin`.
- Caddy module/directive names exposed by the plugin.
- Runtime environment variables and process behavior implemented by the plugin.

The hosting repo can pin a plugin version or commit when building a Caddy binary. The plugin repo should not contain Debian-specific paths, package metadata, service files, or hosted deployment workflow.

### File split

Move to `../reverse-bin-hosting`:
- `packaging/`
- `debian/`
- `examples/reverse-bin-apps-config/`
- `RELESE-PROCESS.md`
- hosted deployment sections from `README.md`
- helper scripts under `utils/` if they only support packaged hosting
- generated package/build artifacts only if they are truly source inputs; otherwise ignore/regenerate them

Keep in `caddy-reverse-bin`:
- `go.mod`
- `go.sum`
- `*.go`
- `cmd/caddy/*` only when required for plugin integration tests
- `reverse-bin_test.go`
- `cmd/caddy/*_test.go`
- `.github/workflows/test-go.yaml`
- `.github/workflows/lint-go.yaml`
- `DESIGN.md` if it documents plugin internals
- `CONTRIBUTING.md` if still applicable to plugin development
- minimal plugin examples such as `examples/reverse-proxy/` if they demonstrate raw plugin usage

Remove or ignore from `caddy-reverse-bin`:
- committed local Caddy binary `caddy`
- generated `build/` contents
- generated Debian build outputs

### Documentation outcome

`caddy-reverse-bin/README.md` should explain:
- What the Caddy plugin does.
- Basic Caddyfile syntax.
- How to run Go tests.
- How to build a Caddy binary with the plugin for development.
- That opinionated Debian hosting lives in `reverse-bin-hosting`.

`reverse-bin-hosting/README.md` should explain:
- What the hosting product provides.
- Relationship to `caddy-reverse-bin`.
- Debian package layout.
- Build/test/release commands.
- Deployment flow.

## Implementation Tasks

### Task 1: Create the sibling hosting repository

**Files:**
- Create directory: `../reverse-bin-hosting/`
- Create repository metadata in `../reverse-bin-hosting/.git/`

**Step 1: Create the directory and initialize Git**

Run:
```bash
mkdir -p ../reverse-bin-hosting
git -C ../reverse-bin-hosting init
```

Expected: Git initializes an empty repo.

**Step 2: Add a minimal ignore file**

Create `../reverse-bin-hosting/.gitignore` with generated outputs ignored:
```gitignore
/build/
*.deb
*.buildinfo
*.changes
*.tar.*
.DS_Store
```

**Step 3: Commit**

Run:
```bash
git -C ../reverse-bin-hosting add .gitignore
git -C ../reverse-bin-hosting commit -m "chore: initialize hosting repo"
```

### Task 2: Copy hosting-owned source files into the hosting repo

**Files:**
- Copy: `packaging/` to `../reverse-bin-hosting/packaging/`
- Copy: `debian/` to `../reverse-bin-hosting/debian/`
- Copy: `examples/reverse-bin-apps-config/` to `../reverse-bin-hosting/examples/reverse-bin-apps-config/`
- Copy: `RELESE-PROCESS.md` to `../reverse-bin-hosting/RELESE-PROCESS.md`
- Review/copy: `utils/` to `../reverse-bin-hosting/utils/` if Debian-hosting-specific

**Step 1: Copy directories preserving file metadata**

Run:
```bash
cp -a packaging ../reverse-bin-hosting/
cp -a debian ../reverse-bin-hosting/
mkdir -p ../reverse-bin-hosting/examples
cp -a examples/reverse-bin-apps-config ../reverse-bin-hosting/examples/
cp -a RELESE-PROCESS.md ../reverse-bin-hosting/
```

**Step 2: Decide whether `utils/` is hosting-only**

Inspect:
```bash
find utils -maxdepth 2 -type f -print
```

If all files are packaging/deployment helpers, run:
```bash
cp -a utils ../reverse-bin-hosting/
```

If some are plugin development helpers, keep those in `caddy-reverse-bin` and copy only hosting-specific files.

**Step 3: Commit copied hosting files**

Run:
```bash
git -C ../reverse-bin-hosting add packaging debian examples RELESE-PROCESS.md utils
git -C ../reverse-bin-hosting commit -m "chore: import reverse-bin hosting files"
```

If `utils/` was not copied, omit it from `git add`.

### Task 3: Create hosting README from current deployment docs

**Files:**
- Create: `../reverse-bin-hosting/README.md`
- Reference: current `README.md`

**Step 1: Extract hosting sections**

Use the current plugin repo `README.md` as source for:
- Debian package layout.
- Runtime model.
- App lifecycle model.
- Example deployment flow.
- TLS and Cloudflare Tunnel modes.
- Packaged examples.

**Step 2: Write `../reverse-bin-hosting/README.md`**

Include this structure:
```markdown
# reverse-bin-hosting

Opinionated Debian/systemd hosting package for apps served through the `caddy-reverse-bin` Caddy plugin.

## Relationship to caddy-reverse-bin

This repo packages and deploys a Caddy binary that includes the plugin from `github.com/tarasglek/reverse-bin`. Plugin behavior and tests live in `caddy-reverse-bin`; Debian packaging and hosted app conventions live here.

## Debian package layout

...

## Runtime model

...

## App lifecycle model

...

## Build the Debian package

...

## Deployment flow

...

## TLS and tunnel modes

...
```

**Step 3: Commit**

Run:
```bash
git -C ../reverse-bin-hosting add README.md
git -C ../reverse-bin-hosting commit -m "docs: describe hosting package"
```

### Task 4: Simplify plugin README

**Files:**
- Modify: `README.md`

**Step 1: Rewrite around plugin-only content**

Keep the README concise:
```markdown
# caddy-reverse-bin

`caddy-reverse-bin` is a Caddy plugin that starts an app process on demand and reverse-proxies requests to it.

## Scope

This repository contains the Caddy plugin and its tests. Opinionated Debian/systemd hosting lives in the sibling `reverse-bin-hosting` repository.

## Development

Run tests:

```bash
go test ./...
```

## Caddyfile usage

...
```

Move Debian/package/deployment detail to the hosting README instead of keeping it here.

**Step 2: Verify README no longer claims this repo is the Debian package**

Run:
```bash
rg "Debian|systemd|/etc/reverse-bin|/var/lib/reverse-bin|reverse-bin.service" README.md
```

Expected: No matches, or only a short pointer to `reverse-bin-hosting`.

**Step 3: Commit**

Run:
```bash
git add README.md
git commit -m "docs: focus readme on caddy plugin"
```

### Task 5: Remove hosting-owned files from plugin repo

**Files:**
- Delete: `packaging/`
- Delete: `debian/`
- Delete: `examples/reverse-bin-apps-config/`
- Delete: `RELESE-PROCESS.md`
- Delete or trim: `utils/` depending on Task 2 decision

**Step 1: Remove moved files**

Run:
```bash
git rm -r packaging debian examples/reverse-bin-apps-config RELESE-PROCESS.md
```

If `utils/` is hosting-only:
```bash
git rm -r utils
```

**Step 2: Verify no packaging files remain unexpectedly**

Run:
```bash
find . -maxdepth 3 \( -path './.git' -o -path './docs' \) -prune -o -type f -print | sort | rg 'debian|packaging|systemd|reverse-bin.service|Caddyfile.acme|Caddyfile.http-only'
```

Expected: No matches outside docs/plans, unless intentionally retained test fixtures exist.

**Step 3: Commit**

Run:
```bash
git commit -m "refactor: remove hosting files from plugin repo"
```

### Task 6: Ignore generated binaries and build outputs in plugin repo

**Files:**
- Modify: `.gitignore`
- Possibly remove from Git index: `caddy`, `build/`

**Step 1: Add generated outputs to `.gitignore`**

Ensure `.gitignore` contains:
```gitignore
/caddy
/build/
*.deb
*.buildinfo
*.changes
```

**Step 2: Stop tracking generated artifacts if currently tracked**

Run:
```bash
git ls-files caddy build
```

If files are tracked and generated, run:
```bash
git rm --cached -r caddy build
```

If they are source inputs, do not remove them; document why they remain tracked.

**Step 3: Commit**

Run:
```bash
git add .gitignore
git commit -m "chore: ignore generated build artifacts"
```

If tracked generated files were removed from the index, include them in the same commit.

### Task 7: Update CI to match plugin-only scope

**Files:**
- Review/modify: `.github/workflows/test-go.yaml`
- Review/modify: `.github/workflows/lint-go.yaml`
- Move/delete from plugin repo if packaging-only: `.github/workflows/release.yml`

**Step 1: Inspect workflows**

Run:
```bash
find .github/workflows -type f -maxdepth 1 -print -exec sh -c 'echo --- "$1"; sed -n "1,220p" "$1"' sh {} \;
```

**Step 2: Keep plugin CI only**

Ensure plugin repo workflows run only relevant checks:
```bash
go test ./...
```

Move packaging/release workflow logic to `../reverse-bin-hosting/.github/workflows/` if it releases Debian packages or bundled hosting artifacts.

**Step 3: Run workflow-equivalent commands locally**

Run:
```bash
go test ./...
```

Expected: all plugin tests pass.

**Step 4: Commit**

Run:
```bash
git add .github/workflows
git commit -m "ci: keep plugin workflows focused"
```

### Task 8: Verify both repositories

**Files:**
- No planned edits unless verification reveals real issues.

**Step 1: Verify plugin repo status and tests**

Run:
```bash
git status --short --branch
go test ./...
```

Expected:
- Clean plugin repo except intentional uncommitted work before the final commit.
- Go tests pass.

**Step 2: Verify hosting repo status**

Run:
```bash
git -C ../reverse-bin-hosting status --short --branch
find ../reverse-bin-hosting -maxdepth 3 -type f | sort | head -200
```

Expected:
- Clean hosting repo.
- Hosting files present.

**Step 3: Optionally run hosting package checks**

If package dependencies are installed, run:
```bash
git -C ../reverse-bin-hosting status --short --branch
```

Do not add flaky retry loops. If packaging tests fail because of missing local dependencies, document the missing dependency instead of weakening checks.

### Task 9: Final commit and summary

**Files:**
- Commit any remaining intentional changes in both repos.

**Step 1: Check diffs**

Run:
```bash
git status --short --branch
git -C ../reverse-bin-hosting status --short --branch
```

**Step 2: Commit remaining plugin changes**

If needed:
```bash
git add <paths>
git commit -m "refactor: split hosting packaging from plugin repo"
```

**Step 3: Commit remaining hosting changes**

If needed:
```bash
git -C ../reverse-bin-hosting add <paths>
git -C ../reverse-bin-hosting commit -m "chore: import reverse-bin hosting package"
```

**Step 4: Report results**

Summarize:
- Files moved to `reverse-bin-hosting`.
- Files kept in `caddy-reverse-bin`.
- Test commands run and outcomes.
- Commit hashes for both repositories.
