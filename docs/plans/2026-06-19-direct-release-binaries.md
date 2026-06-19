# Direct Release Binaries Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Publish GoReleaser release assets as direct executable binaries instead of tar archives.

**Architecture:** Update `.goreleaser.yaml` archive configuration to use GoReleaser's `binary` archive format. Verify with a snapshot release using GoReleaser v2 and inspect generated `dist/` artifacts.

**Tech Stack:** GoReleaser v2, YAML, Go.

---

### Task 1: Update GoReleaser archive format

**Files:**
- Modify: `.goreleaser.yaml`

**Step 1: Change archive format**

Replace:

```yaml
    formats:
      - tar.gz
```

with:

```yaml
    formats:
      - binary
```

**Step 2: Validate YAML syntax**

Run: `ruby -e 'require "yaml"; YAML.load_file(".goreleaser.yaml")'`
Expected: exits 0.

### Task 2: Verify snapshot release artifacts

**Files:**
- Generated then removed: `dist/`

**Step 1: Run GoReleaser snapshot**

Run: `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish`
Expected: exits 0.

**Step 2: Inspect artifact files**

Run: `find dist -maxdepth 1 -type f -printf '%f\n' | sort`
Expected output includes platform binaries and `checksums.txt`, with no `.tar.gz` files.

**Step 3: Verify no tar archives exist**

Run: `find dist -maxdepth 1 -name '*.tar.gz' -print -quit | grep -q . && exit 1 || exit 0`
Expected: exits 0.

**Step 4: Clean generated artifacts**

Run: `rm -rf dist`
Expected: `dist/` removed.

### Task 3: Commit

**Files:**
- `.goreleaser.yaml`
- `docs/plans/2026-06-19-direct-release-binaries-design.md`
- `docs/plans/2026-06-19-direct-release-binaries.md`

**Step 1: Review diff**

Run: `git diff -- .goreleaser.yaml docs/plans/2026-06-19-direct-release-binaries-design.md docs/plans/2026-06-19-direct-release-binaries.md`
Expected: only intended archive format and docs additions.

**Step 2: Commit changes**

Run:

```bash
git add .goreleaser.yaml docs/plans/2026-06-19-direct-release-binaries-design.md docs/plans/2026-06-19-direct-release-binaries.md
git commit -m "ci: publish release binaries directly"
```

Expected: local commit created.
