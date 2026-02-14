# Release Process (caddy-reverse-bin)

This document defines how releases should work for publishing `caddy-reverse-bin` binaries to GitHub Releases.

## Goals

- Publish reproducible Go binaries for Linux and macOS (no Windows builds).
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

For each tag, build binaries for supported targets:

- `linux/amd64`
- `linux/arm64`
- `darwin/arm64`

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

## Version Embedding (CLI + Logs)

Each release binary should embed version metadata at build time.

### Build-time variables

- `github.com/caddyserver/caddy/v2.CustomVersion`
- `github.com/tarasglek/reverse-bin.Version`
- `github.com/tarasglek/reverse-bin.Commit`
- `github.com/tarasglek/reverse-bin.BuildDate`

### Why

- `caddy version` should report the release tag (`vX.Y.Z`).
- `reverse-bin` startup logs should include version, commit, and build date.

### How

Use linker flags in the release build:

```bash
-ldflags "
  -X github.com/caddyserver/caddy/v2.CustomVersion={{.Version}}
  -X github.com/tarasglek/reverse-bin.Version={{.Version}}
  -X github.com/tarasglek/reverse-bin.Commit={{.Commit}}
  -X github.com/tarasglek/reverse-bin.BuildDate={{.Date}}
"
```

Expected outcome for tag `v1.2.0`:

- CLI: `caddy version` shows `v1.2.0`
- Logs: module startup log includes `version=v1.2.0`, commit SHA, and build date

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

## Dry Run (No Publish)

Use GoReleaser snapshot mode to validate the release process without creating a GitHub Release.

```bash
goreleaser release --snapshot --clean --skip=publish
```

What to verify after the command finishes:

1. `dist/` contains archives for all supported targets (`linux/amd64`, `linux/arm64`, `darwin/arm64`).
2. `dist/checksums.txt` exists.
3. Archive names and artifact layout match expectations.
4. Version metadata is embedded (CLI version output + startup log fields).

Notes:

- Snapshot builds intentionally use a snapshot version string (not a real release tag).
- No GitHub release is created and no assets are uploaded in this mode.

## Maintainer Checklist

1. Ensure tests/lint are green on `main`.
2. (Recommended) Run the dry run command successfully.
3. Merge release-ready changes.
4. Create and push SemVer tag.
5. Verify release workflow passed.
6. Verify all expected assets are attached.
7. Verify release notes are present and readable.
8. If draft mode is enabled, review and publish the draft release.

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
- Add Homebrew/Nix metadata generation.
