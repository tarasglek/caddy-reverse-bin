#!/bin/sh
set -eu

notes=${1:-}

if [ -z "$notes" ] || [ ! -f "$notes" ]; then
    echo "error: release notes are required; add release-notes/<tag>.md" >&2
    exit 1
fi

require_section() {
    section=$1
    if ! grep -Fxq "## $section" "$notes"; then
        echo "error: release notes must include '## $section'" >&2
        exit 1
    fi

    if ! awk -v heading="## $section" '
        $0 == heading { in_section = 1; next }
        in_section && /^## / { exit }
        in_section && $0 !~ /^[[:space:]]*$/ { found = 1 }
        END { exit !found }
    ' "$notes"; then
        echo "error: release notes section '## $section' must not be empty" >&2
        exit 1
    fi
}

if ! head -n 1 "$notes" | grep -Eq '^# Caddy Reverse Bin v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "error: release notes must start with '# Caddy Reverse Bin vX.Y.Z'" >&2
    exit 1
fi

require_section "Highlights"
require_section "Breaking changes"
require_section "Full list of changes"

if ! awk '
    $0 == "## Highlights" { in_section = 1; next }
    in_section && /^## / { exit }
    in_section && /^- / { found = 1 }
    END { exit !found }
' "$notes"; then
    echo "error: '## Highlights' must contain at least one bullet" >&2
    exit 1
fi

if ! awk '
    $0 == "## Full list of changes" { in_section = 1; next }
    in_section && /^## / { exit }
    in_section && /^- / { found = 1 }
    END { exit !found }
' "$notes"; then
    echo "error: '## Full list of changes' must contain at least one bullet" >&2
    exit 1
fi
