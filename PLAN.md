# CI Failure Fix Plan

## Status legend
- [ ] pending
- [x] done
- [~] in progress / attempted but blocked

## Current failures

None currently. Latest PR runs are green for both workflows:
- `Lint Go` ✅
- `Test Go` ✅

---

## Execution checklist

### A) Dependency upgrades
- [x] Upgrade dependencies to latest feasible versions (`go get -u ./...` + tidy).
- [x] Keep dependency graph build-stable after upgrades.

### B) Local verification before push
- [x] Re-run local tests in tmux after upgrade attempt.
- [x] Achieve green local `go test ./...`.
  - Fixed race by detecting dead backend process before proxying and forcing restart.

### C) CI fixes
- [x] Ensure CI installs all runtime dependencies required by integration tests.
  - Uses pinned `eget` to install `landrun` reliably in CI.
- [x] Keep tests strict (no skip-on-missing-tool fallback) once installs are in place.
- [x] Re-run CI and confirm `Test Go` passes.
  - Also fixed CI-only unix socket path length issue in `TestDynamicDiscovery`.

### D) Vulnerability cleanup
- [x] Re-run `govulncheck ./...` after dependency stabilization.
- [x] Resolve/mitigate remaining reachable vulnerabilities.
  - Mitigated third-party reachable issues via dependency updates.
  - Remaining local findings were stdlib-only; CI Go pinned to `1.25.7`.
- [x] Re-run CI and confirm `Lint Go` passes.

### E) Refactor: unify backend startup/restart path (remove duplicated startup logic)
- [x] First priority: refactor runtime process/restart path so dependency bump to `github.com/smallstep/certificates@v0.29.0` remains stable (eliminate transient post-crash 502 race).
- [x] Introduce a single helper for "ensure process running + ready + upstream resolved".
- [x] Route both initial startup and restart-on-dead-process through this helper.
- [x] Keep lock boundaries explicit (`ps.mu`) and avoid side effects in multiple call sites.
- [x] Add/adjust tests for:
  - [x] first request startup
  - [x] crash/restart path
  - [x] readiness timeout/failure path
- [x] Verify no behavior regressions (`go test ./...` local + CI).
  - [x] local `go test ./...` in tmux is green
  - [x] CI rerun green for both workflows

---

## Next immediate step
1. Merge PR after review.
2. Optionally clean up non-fatal cache restore warning noise in Actions.
3. Start next feature/fix work.
