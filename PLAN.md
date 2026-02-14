# CI Failure Fix Plan

## Status legend
- [ ] pending
- [x] done
- [~] in progress / attempted but blocked

## Current failures

### 1) `Lint Go` workflow fails at `govulncheck`
`govulncheck` is correctly running and failing the build because reachable vulns are reported through Caddy dependency paths.

Key items reported in logs include:
- `golang.org/x/crypto` (GO-2025-4116)
- `github.com/quic-go/quic-go` (GO-2025-4017, GO-2025-3735)
- `github.com/go-jose/go-jose/v3` (GO-2025-3485)
- `github.com/golang/glog` (GO-2025-3372)

### 2) `Test Go` workflow fails in integration tests
`TestDynamicDiscovery` fails in CI because required runtime binary is missing:
- `exec: "landrun": executable file not found in $PATH`

---

## Execution checklist

### A) Dependency upgrades
- [x] Upgrade dependencies to latest feasible versions (`go get -u ./...` + tidy).
- [~] Keep dependency graph build-stable after upgrades.
  - Current blocker: local tests now fail in `TestProcessCrashAndRestart` with a transient `502` after backend crash/restart.

### B) Local verification before push
- [x] Re-run local tests in tmux after upgrade attempt.
- [~] Achieve green local `go test ./...`.
  - Current result: fails in `cmd/caddy` integration suite (`TestProcessCrashAndRestart`).

### C) CI fixes
- [ ] Make dynamic discovery tests deterministic in CI (`landrun` present or tests skip when unavailable).
- [ ] Re-run CI and confirm `Test Go` passes.

### D) Vulnerability cleanup
- [ ] Re-run `govulncheck ./...` after dependency stabilization.
- [ ] Resolve/mitigate remaining reachable vulnerabilities.
- [ ] Re-run CI and confirm `Lint Go` passes.

---

## Next immediate step
1. Fix `TestProcessCrashAndRestart` regression introduced by dependency upgrades (likely restart readiness/race behavior change).
2. After local green, push and evaluate CI status.
