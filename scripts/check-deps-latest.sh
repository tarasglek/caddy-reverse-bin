#!/bin/sh
# Fails when any go.mod requirement has a newer release. Only the listed
# requirements are checked: "go list -m -u all" also reports modules
# pruned from the build, which no upgrade can move, so it never passes.
set -eu
cd "$(dirname -- "$0")/.."

status=0
for module in . detectorschema examples/reverse-proxy; do
    # shellcheck disable=SC2046 # one argument per module path
    behind=$(cd "$module" && go list -m -u -f '{{if .Update}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' \
        $(go mod edit -json | jq -r '.Require[]?.Path'))
    if [ -n "$behind" ]; then
        echo "$behind"
        echo "error: $module: behind latest; run: cd $module && go get -u -t ./... && go mod tidy" >&2
        status=1
    fi
done
exit "$status"
