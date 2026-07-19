#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CHECK="$ROOT/scripts/check-release-notes.sh"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() {
    echo "error: $*" >&2
    exit 1
}

run_invalid() {
    expected=$1
    notes=$2
    if output=$($CHECK "$notes" 2>&1); then
        fail "invalid release notes passed validation: $notes"
    fi
    [ "$output" = "$expected" ] || fail "unexpected validation error: $output"
}

# A complete authored release note must pass validation.
cat > "$TMP/valid.md" <<'EOF'
# Caddy Reverse Bin v1.2.3

## Highlights

- Adds a user-facing improvement.

## Breaking changes

None.

## Full list of changes

- Implements the improvement.
EOF
$CHECK "$TMP/valid.md"

# A missing tag-specific file must fail with an actionable convention reminder.
run_invalid \
    "error: release notes are required; add release-notes/<tag>.md" \
    "$TMP/missing.md"

# The release title must use the exact project name and semantic version shape.
sed '1s/Caddy Reverse Bin/Caddy reverse-bin/' "$TMP/valid.md" > "$TMP/bad-title.md"
run_invalid \
    "error: release notes must start with '# Caddy Reverse Bin vX.Y.Z'" \
    "$TMP/bad-title.md"

# Every required section, including Breaking changes, must contain authored text.
awk '$0 == "None." { next } { print }' "$TMP/valid.md" > "$TMP/empty-breaking.md"
run_invalid \
    "error: release notes section '## Breaking changes' must not be empty" \
    "$TMP/empty-breaking.md"

# Highlights must contain a bullet rather than prose alone.
sed 's/^- Adds a user-facing improvement\.$/Adds a user-facing improvement./' "$TMP/valid.md" > "$TMP/no-highlight-bullet.md"
run_invalid \
    "error: '## Highlights' must contain at least one bullet" \
    "$TMP/no-highlight-bullet.md"

# The full change list must contain a bullet rather than prose alone.
sed 's/^- Implements the improvement\.$/Implements the improvement./' "$TMP/valid.md" > "$TMP/no-change-bullet.md"
run_invalid \
    "error: '## Full list of changes' must contain at least one bullet" \
    "$TMP/no-change-bullet.md"

echo "release notes checks passed"
