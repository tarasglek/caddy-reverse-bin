# Contributing

This repository contains the `caddy-reverse-bin` Caddy plugin and its tests.

## Development workflow

1. Format Go code before committing:

   ```bash
   gofmt -w <changed-go-files>
   ```

2. Run the plugin test suite:

   ```bash
   go test ./...
   ```

3. Build a local Caddy binary when changing module registration or Caddyfile behavior:

   ```bash
   make build
   ./caddy list-modules | grep http.handlers.reverse-bin
   ```

## Test expectations

- Keep tests focused on plugin behavior.
- Comments should state the intent of each test.
- HTTP assertions should be specific and stable.
- Do not add retry loops to mask flakes; fix the underlying cause.

## Release process

This repo has two Go modules:

- `caddy-reverse-bin`: `github.com/tarasglek/caddy-reverse-bin`
- `detectorschema`: `github.com/tarasglek/caddy-reverse-bin/detectorschema`

If `caddy-reverse-bin` requires a new `detectorschema` version, tag `detectorschema` first:

```bash
git tag -a detectorschema/vX.Y.Z -m "detectorschema vX.Y.Z"
git push origin detectorschema/vX.Y.Z
```

Then update `caddy-reverse-bin` `go.mod` to require that `detectorschema` version. Do not leave a local `replace` for `detectorschema` in a release commit.

`caddy-reverse-bin` releases use normal `v*` tags. Before tagging `vX.Y.Z`, add `release-notes/vX.Y.Z.md` with this structure:

- `# Caddy Reverse Bin vX.Y.Z`
- `## Highlights` with at least one bullet
- `## Breaking changes` with text such as `None.` when there are no breaking changes
- `## Full list of changes` with at least one bullet

Validate the authored notes and release workflow locally:

```bash
./scripts/check-release-notes.sh release-notes/vX.Y.Z.md
./scripts/check-release-gate.sh
```

Pushing the tag runs `.github/workflows/release.yml`, which validates the matching file before GoReleaser publishes it as the GitHub release body:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin main vX.Y.Z
```

## Hosting package

Debian packaging, systemd deployment, bundled helper runtimes, app discovery policy, and hosted-app compatibility workflows live in the sibling `reverse-bin-hosting` repository.
