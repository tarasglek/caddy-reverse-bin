# Authored Release Notes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Require structured, authored notes for every tagged Caddy plugin release and publish those notes unchanged through GoReleaser.
**Architecture:** A POSIX shell validator enforces note structure. A second shell regression check asserts the tagged workflow invokes the validator and passes the matching file to GoReleaser; both checks run from `make check`.
**Tech Stack:** POSIX shell, Make, GitHub Actions, GoReleaser v2.

---

- [ ] Task 1: Add failing validator and workflow regression tests
  - Files: `scripts/test-release-notes.sh`, `scripts/check-release-gate.sh`, `Makefile`
  - Test first: cover a valid note plus missing file, invalid heading, empty required sections, and missing required bullets; assert workflow validation and `--release-notes` wiring.
  - Verify RED: run both scripts; expect failures because the validator and workflow gate do not exist yet.

- [ ] Task 2: Implement authored-note validation and release wiring
  - Files: `scripts/check-release-notes.sh`, `.github/workflows/release.yml`, `.goreleaser.yaml`
  - Implement: validate `release-notes/$GITHUB_REF_NAME.md` before GoReleaser and pass it through `--release-notes`; disable obsolete generated changelog configuration.
  - Verify GREEN: run both regression scripts and `make check`.

- [ ] Task 3: Add v0.3.2 notes and release documentation
  - Files: `release-notes/v0.3.2.md`, `CONTRIBUTING.md`
  - Change: accurately summarize the managed Unix socket directory lifecycle fix and Caddy/security dependency updates; document required format and local check.
  - Verify: validate `release-notes/v0.3.2.md`, run workflow lint where locally available, and inspect the final diff.
