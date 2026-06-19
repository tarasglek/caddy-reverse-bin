# GitHub Actions Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add low-maintenance GitHub Actions hardening via automated updates and workflow lint/security checks.

**Architecture:** Keep existing CI and release workflow behavior unchanged. Add Dependabot GitHub Actions updates and a standalone Gabo-style workflow that checks workflow YAML with actionlint and zizmor.

**Tech Stack:** GitHub Actions, Dependabot, actionlint, zizmor, YAML.

---

### Task 1: Add GitHub Actions Dependabot updates

**Files:**
- Create: `.github/dependabot.yml`

**Step 1: Create Dependabot config**

```yaml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
```

**Step 2: Validate YAML syntax**

Run: `ruby -e 'require "yaml"; YAML.load_file(".github/dependabot.yml")'`
Expected: exits 0.

### Task 2: Add Gabo-style workflow linting

**Files:**
- Create: `.github/workflows/lint-github-actions.yaml`

**Step 1: Create workflow**

Add a workflow that runs on workflow/dependabot config changes, checks out only `.github`, runs `reviewdog/action-actionlint`, runs `rhysd/actionlint` in Docker, and runs zizmor offline.

**Step 2: Validate YAML syntax**

Run: `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/lint-github-actions.yaml")'`
Expected: exits 0.

### Task 3: Verify repository state

**Step 1: Run local validation**

Run: `ruby -e 'require "yaml"; Dir[".github/**/*.y{a,}ml"].each { |f| YAML.load_file(f) }'`
Expected: exits 0.

**Step 2: Run git diff review**

Run: `git diff -- .github docs/plans`
Expected: only intended additions.

### Task 4: Commit

**Step 1: Commit changes**

Run:

```bash
git add .github/dependabot.yml .github/workflows/lint-github-actions.yaml docs/plans/2026-06-19-github-actions-hardening-design.md docs/plans/2026-06-19-github-actions-hardening.md
git commit -m "ci: add github actions hardening"
```

Expected: local commit created.
