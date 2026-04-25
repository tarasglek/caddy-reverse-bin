# Discover App Explicit Command Fallback Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `discover-app` support opaque explicit commands without `LISTEN` or `SOCKET_PATH` by allocating a TCP fallback instead of failing on entrypoint detection.

**Architecture:** Split app resolution into independent command, transport, and compatibility-validation phases. Explicit `REVERSE_BIN_COMMAND` becomes authoritative for command selection, transport fallback becomes independent from entrypoint detection, and app-kind detection is only used when command inference or transport compatibility checks actually need it.

**Tech Stack:** Python 3.13 script (`uv run --script`), `python-dotenv`, unittest-based CLI/unit tests.

---

## Design

### Approved decisions

- [x] Opaque explicit command like `REVERSE_BIN_COMMAND=./cmd.sh` with no `LISTEN` or `SOCKET_PATH` must be valid.
- [x] Missing transport for explicit command must allocate TCP fallback.
- [x] Refactor shape must be split resolver, not catch-and-continue patch.
- [x] Do not add `PORT` in this change.
- [x] Preserve readiness override behavior.

### Problem statement

- [x] Current `resolve_app()` calls entrypoint detection before transport fallback resolution.
- [x] Explicit opaque commands like `./cmd.sh` have no detectable `main.py` or `main.ts`.
- [x] `detect_app()` raises `FileNotFoundError` before fallback TCP allocation runs.
- [x] Result: reverse-bin returns `503` even though fallback transport code exists.

### Target invariants

- [ ] Explicit `REVERSE_BIN_COMMAND` never requires detectable entrypoint just to get TCP fallback.
- [ ] Missing transport with explicit command resolves to `127.0.0.1:<allocated-port>`.
- [ ] Child env gets injected `LISTEN=<resolved-address>` only when fallback or blank `LISTEN=` resolution occurs.
- [ ] Missing entrypoint remains hard failure only when command inference is required.
- [ ] Explicit `SOCKET_PATH` compatibility checks still reject unsupported detected kinds like `main.ts`.
- [ ] Existing autodetected app behavior stays unchanged.

### Proposed structure

- [ ] Add command-resolution helper.
  - Inputs: `working_dir`, parsed `.env` config.
  - Behavior: return explicit command when `REVERSE_BIN_COMMAND` exists; otherwise detect app kind and infer command.
- [ ] Add transport-resolution helper.
  - Inputs: `working_dir`, parsed `.env` config.
  - Behavior: normalize explicit `LISTEN`, resolve explicit `SOCKET_PATH`, or allocate TCP fallback when both absent.
- [ ] Add compatibility-validation helper.
  - Inputs: `working_dir`, parsed config.
  - Behavior: only detect app kind when compatibility validation actually needs it, primarily for explicit `SOCKET_PATH`.
- [ ] Keep env assembly separate.
  - Inputs: original `.env`, transport overrides.
  - Behavior: preserve existing env passthrough, `PATH`, `HOME`, and `LISTEN` override semantics.
- [ ] Keep readiness fields flowing through final result unchanged.

### Files

- [ ] Modify `utils/discover-app/discover-app.py`
- [ ] Modify `utils/discover-app/test_discover_app.py`
- [ ] Optionally update `docs/plans/2026-04-13-discover-app-merged-discovery-design-and-plan.md` only if needed for cross-reference clarity; avoid if implementation can stay self-contained.

---

## Implementation plan

### Task 1: Add failing tests for opaque explicit command fallback

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`

- [ ] Step 1: Add unit or CLI test proving command-only `.env` with opaque command succeeds without detectable entrypoint.
  - Test setup: app dir with `.env` containing `REVERSE_BIN_COMMAND=./cmd.sh`
  - No `main.py`
  - No `main.ts`
  - Add executable `cmd.sh`
- [ ] Step 2: Assert exact invariants.
  - CLI exit code `0`
  - `payload["executable"] == ["./cmd.sh"]`
  - `payload["reverse_proxy_to"]` matches `^127\.0\.0\.1:\d+$`
  - `payload["envs"]` contains `LISTEN=<same reverse_proxy_to>`
- [ ] Step 3: Add second test proving readiness overrides still emit for same opaque explicit-command shape.
  - `.env` includes `READINESS_METHOD=GET`
  - `.env` includes `READINESS_PATH=/.well-known/openid-configuration`
  - Assert both JSON fields present and exact
- [ ] Step 4: Run targeted tests and confirm new test fails before code change.
  - Run: `python3 -m unittest utils.discover-app.test_discover_app -v`
  - Expected: failure showing command-only opaque app still errors on missing entrypoint
- [ ] Step 5: Commit failing tests.
  - Commit message: `test(discover-app): cover opaque command fallback`

### Task 2: Split resolver into command, transport, and compatibility phases

**Files:**
- Modify: `utils/discover-app/discover-app.py`

- [ ] Step 1: Add `resolve_command(...)` helper.
  - If `config["command"]` present, return it directly and do not require detection.
  - Else detect app and infer command exactly as today.
- [ ] Step 2: Add `resolve_transport(...)` helper.
  - Reuse current `resolve_proxy_target(...)` logic or rename it.
  - Preserve behavior for explicit `LISTEN`, blank `LISTEN=`, explicit `SOCKET_PATH`, and fallback TCP allocation.
- [ ] Step 3: Add `validate_transport_compatibility(...)` helper.
  - Only detect app kind when explicit transport choice needs compatibility checking.
  - Preserve failure for unsupported explicit unix-socket combinations.
- [ ] Step 4: Rewrite `resolve_app()` to call helpers in this order:
  - parse config
  - resolve command
  - resolve transport
  - validate compatibility
  - return `ResolvedApp`
- [ ] Step 5: Keep readiness fields unchanged in `ResolvedApp` and JSON result.
- [ ] Step 6: Run targeted tests.
  - Run: `python3 -m unittest utils.discover-app.test_discover_app -v`
  - Expected: new fallback tests pass; old tests stay green
- [ ] Step 7: Commit refactor.
  - Commit message: `refactor(discover-app): split command and transport resolution`

### Task 3: Verify no behavior regressions in key edge cases

**Files:**
- Modify: `utils/discover-app/test_discover_app.py` only if any coverage gap remains

- [ ] Step 1: Re-run focused cases already in suite for these invariants:
  - missing command plus missing detectable entrypoint still fails
  - explicit `SOCKET_PATH` with unsupported detected kind still fails
  - blank `LISTEN=` still resolves and injects concrete `LISTEN`
  - autodetected `main.py` and `main.ts` still work
- [ ] Step 2: If one invariant lacks direct coverage, add smallest missing test.
- [ ] Step 3: Run full discover-app test module again.
  - Run: `python3 -m unittest utils.discover-app.test_discover_app -v`
  - Expected: all pass
- [ ] Step 4: Commit any final test coverage changes.
  - Commit message: `test(discover-app): lock split resolver invariants`

### Task 4: Final verification

**Files:**
- No source changes required

- [ ] Step 1: Verify working tree only contains intended changes.
  - Run: `git status --short`
- [ ] Step 2: Review final diff.
  - Run: `git diff -- utils/discover-app/discover-app.py utils/discover-app/test_discover_app.py`
- [ ] Step 3: Reproduce target behavior locally with direct CLI fixture or test.
- [ ] Step 4: Commit final state if previous task did not already capture all intended changes.
  - Commit message: `fix(discover-app): allow fallback for opaque commands`

---

## Notes for implementation

- [ ] Do not add `PORT` in this change.
- [ ] Do not weaken missing-entrypoint failure for command inference path.
- [ ] Do not add retry loops to tests.
- [ ] Keep assertions exact and anti-fragile.
- [ ] Prefer smallest refactor that makes phase ownership obvious.
