# Direct Release Binaries Design

## Goal

Publish platform-specific executable binaries directly as GitHub Release assets instead of tar archives.

## Approach

Change GoReleaser archive output from `tar.gz` archives to `binary` artifacts. Keep the existing build matrix, binary name, release target, and checksum publishing unchanged.

## Resulting assets

Expected release assets:

- `caddy-reverse-bin_linux_amd64`
- `caddy-reverse-bin_linux_arm64`
- `caddy-reverse-bin_darwin_arm64`
- `checksums.txt`

## Trade-offs

- Direct assets are easier to download and execute in scripts.
- Assets no longer include bundled `LICENSE` or `README.md`; those remain available in the repository and release page.
- Checksums continue to cover published artifacts.

## Verification

Run a GoReleaser snapshot with the current GoReleaser v2 binary and inspect `dist/` to confirm no `.tar.gz` assets are produced.
