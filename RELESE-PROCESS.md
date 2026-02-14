# Release Process (caddy-reverse-bin)

This document defines how releases should work for publishing `caddy-reverse-bin` binaries to GitHub Releases.

## Goals

- Publish reproducible Go binaries for Linux, macOS, and Windows.
- Trigger releases in a predictable way.
- Generate clear release notes with minimal manual work.
- Keep release steps simple and modern.

## Proposed Release Model

Use **Git tags + GitHub Actions + GoReleaser**.

- Trigger on SemVer tags: `vX.Y.Z` (example: `v1.2.0`).
- CI builds binaries for supported platforms.
- GoReleaser creates the GitHub Release and uploads assets.
- GitHub auto-generates base release notes.
- Optional manual edits can be added before publishing if draft releases are used.

## Trigger

A release starts when a maintainer pushes a version tag:

```bash
git tag -a v1.2.0 -m "v1.2.0"
git push origin v1.2.0
```

Expected workflow trigger:

- `on.push.tags: ["v*"]`

This ensures releases are explicit and controlled.

## What Gets Built

For each tag, build binaries for common targets:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

Artifacts should include:

- compressed binary archives (`.tar.gz`/`.zip`)
- checksums file (e.g. `checksums.txt`)

## GitHub Release Creation

GoReleaser should:

1. Detect the tag version.
2. Build all configured binaries.
3. Package artifacts.
4. Create (or update) the GitHub Release for that tag.
5. Upload all artifacts as release assets.

## Release Notes

### Default (recommended)

Use **GitHub generated release notes** as the base.

This gives:

- list of merged PRs
- list of contributors
- compare link

### Curation

Maintainers should add a short manual summary at top:

- highlights
- breaking changes (if any)
- upgrade notes

Suggested structure:

1. Highlights
2. Breaking changes
3. Upgrade notes
4. Full changelog (auto-generated)

## Versioning Rules

Use SemVer tags:

- `vMAJOR.MINOR.PATCH`
- Patch: bug fixes
- Minor: backward-compatible features
- Major: breaking changes

## Maintainer Checklist

1. Ensure tests/lint are green on `main`.
2. Merge release-ready changes.
3. Create and push SemVer tag.
4. Verify release workflow passed.
5. Verify all expected assets are attached.
6. Verify release notes are present and readable.
7. If draft mode is enabled, review and publish the draft release.

## Failure Handling

If release job fails:

1. Fix root cause in code/workflow.
2. Re-run workflow if possible.
3. If artifacts were partially uploaded, delete the broken release/tag and recreate cleanly.

Avoid manual one-off uploads to keep releases reproducible.

## Future Enhancements (optional)

- Sign binaries/checksums (Cosign).
- Attach SBOMs.
- Publish container images in the same release flow.
- Add Homebrew/Scoop/Nix metadata generation.
