# CI Action Dependency Bump Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Update GitHub Actions dependencies to remove the release warning about Node.js 20 and keep shared CI actions current.

**Architecture:** This is a workflow-only change. Replace pinned major-version action references for checkout, setup-go, and goreleaser with the latest released tags discovered via GitHub API.

**Tech Stack:** GitHub Actions, GoReleaser, `gh` CLI.

---

### Task 1: Update workflow action versions

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `.github/workflows/lint-go.yaml`
- Modify: `.github/workflows/test-go.yaml`
- Modify: `.github/workflows/lint-github-actions.yaml`

**Step 1: Edit action references**

Replace:
- `actions/checkout@v6` with `actions/checkout@v7.0.0`
- `actions/setup-go@v6` with `actions/setup-go@v6.4.0`
- `goreleaser/goreleaser-action@v6` with `goreleaser/goreleaser-action@v7.2.2`

**Step 2: Verify references**

Run:
```bash
rg 'uses: (actions/checkout|actions/setup-go|goreleaser/goreleaser-action)@' .github/workflows
```

Expected: only the new versions appear.

**Step 3: Validate workflow syntax enough for local review**

Run:
```bash
git diff --check
```

Expected: no output and exit code 0.

**Step 4: Commit**

Run:
```bash
git add .github/workflows docs/plans/2026-06-20-ci-action-dependency-bump.md
git commit -m "ci: bump github action dependencies"
```
