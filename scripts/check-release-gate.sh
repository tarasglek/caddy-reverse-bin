#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORKFLOW="$ROOT/.github/workflows/release.yml"

fail() {
    echo "error: $*" >&2
    exit 1
}

[ -f "$WORKFLOW" ] || fail "release workflow missing"

# Tagged releases must validate the authored file before publishing.
grep -q 'Validate release notes' "$WORKFLOW" || fail "tag releases must validate authored release notes"
grep -q 'check-release-notes.sh "release-notes/$GITHUB_REF_NAME.md"' "$WORKFLOW" || fail "tag releases must validate notes for the pushed tag"

# GoReleaser must publish that same authored file instead of generated notes.
grep -q -- '--release-notes=release-notes/${{ github.ref_name }}.md' "$WORKFLOW" || fail "GoReleaser must use authored release notes"

validate_line=$(grep -n 'check-release-notes.sh' "$WORKFLOW" | head -n 1 | cut -d: -f1)
publish_line=$(grep -n 'goreleaser/goreleaser-action' "$WORKFLOW" | head -n 1 | cut -d: -f1)
[ "$validate_line" -lt "$publish_line" ] || fail "release notes validation must run before GoReleaser"

# Releases must ship without reachable vulnerabilities and on latest deps.
for target in vulncheck deps-latest; do
    target_line=$(grep -n "run: make $target\$" "$WORKFLOW" | head -n 1 | cut -d: -f1)
    [ -n "$target_line" ] || fail "tag releases must run make $target"
    [ "$target_line" -lt "$publish_line" ] || fail "make $target must run before GoReleaser"
done

echo "release gate checks passed"
