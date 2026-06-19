# GitHub Actions Hardening Design

## Goal

Get the practical benefits of safer GitHub Actions with minimal maintenance overhead.

## Approach

Keep existing workflow action references readable and tag-based for now, add automated dependency update coverage for GitHub Actions, and add a Gabo-style lint/security workflow for workflow files.

## Trade-offs

- Dependabot keeps Actions up to date without manual SHA maintenance.
- actionlint catches workflow syntax and expression issues before they break CI.
- zizmor flags risky Actions patterns, including places where future SHA pinning may matter most.
- This does not provide full immutability from SHA pinning, but avoids its readability and update burden.

## Files

- Add `.github/dependabot.yml` for weekly GitHub Actions update PRs.
- Add `.github/workflows/lint-github-actions.yaml` adapted from Gabo for this repository layout.

## Verification

Validate workflow YAML locally and run actionlint if available.
