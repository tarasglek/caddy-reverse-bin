# Discover App Merged Discovery Design and Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Update `utils/discover-app/discover-app.py` so it loads `.env` once, merges explicit config with autodetection, fills only missing command/upstream values from detection, and migrates example apps under `examples/reverse-proxy/apps/*` away from legacy env usage.

**Architecture:** Treat `.env` as a single partial config source instead of a separate explicit-only mode. Parse `.env` once into a partial typed config, detect the supported app kind once, then resolve a final launch config by combining explicit values with inferred defaults. Explicit values remain authoritative when valid, but conflicting explicit transport choices must fail fast.

**Tech Stack:** Python 3.13, `python-dotenv`, stdlib `unittest`, repo example apps under `examples/reverse-proxy/apps/*`

---

## Design

### Problem Statement

The current `discover-app.py` flow still thinks in terms of two separate modes:

- explicit `.env` mode requires `REVERSE_BIN_COMMAND` plus exactly one of `LISTEN` or `SOCKET_PATH`
- autodetection mode infers everything

The requested behavior is different:

- `.env` should be loaded once and used as a partial config source
- if `REVERSE_BIN_COMMAND` is missing, infer the command from supported entrypoints
- if upstream config is missing, infer `LISTEN` or `SOCKET_PATH` as appropriate
- if the user provided an incompatible transport choice, error instead of silently changing it
- old in-repo examples should use `.env` in the new style instead of relying on legacy env names

### Final Discovery Contract

`discover-app.py` should resolve a final app launch config using these rules:

1. Load `.env` exactly once.
2. Parse a partial config from `.env`.
3. Detect supported app kind from files in the working directory.
4. Merge explicit config with detection to produce one resolved config.
5. Build child envs from the already-loaded env map plus only the missing values that had to be inferred.
6. Emit the existing detector JSON shape.

### TypedDict Expectations

Both typed dicts should include comments with concrete example values so the expected shape is obvious at the call sites.

#### `EnvAppConfig`

This should become a partial config shape instead of an explicit-mode-only shape.

Expected fields:

- `command: list[str] | None`
- `listen: str | None`
- `socket_path: str | None`

Example values to document inline in comments:

- `command=["python3", "server.py"]`
- `listen="8080"` or `listen="127.0.0.1:8080"`
- `socket_path="data/app.sock"`

#### `DiscoverAppResult`

Keep the existing JSON payload shape, but add inline example comments.

Expected fields:

- `executable: list[str]`
- `reverse_proxy_to: str`
- `working_directory: str`
- `envs: list[str]`

Example values to document inline in comments:

- `executable=["./main.py"]`
- `reverse_proxy_to="127.0.0.1:8080"` or `"unix//abs/path/data/app.sock"`
- `working_directory="/abs/path/to/app"`
- `envs=["LISTEN=127.0.0.1:8080", "PATH=/usr/bin:/bin"]`

### Supported Detection Matrix

Supported inferred app kinds:

- `main.ts`
  - inferred command: `deno serve ... main.ts`
  - supported transport: TCP only
- executable `main.py`
  - inferred command: `./main.py`
  - supported transport: TCP or unix socket

Unsupported for autodetection:

- `main.sh`
- anything else not already supported

If `REVERSE_BIN_COMMAND` is explicitly set, keep honoring it even when autodetection would not know how to infer a command. Detection is only needed to fill missing values or validate transport compatibility when command inference is required.

### Merge Rules

#### Command resolution

- If `REVERSE_BIN_COMMAND` is present, parse it with `shlex.split()` and use it.
- If it is absent, infer the command from the detected app kind.
- If command inference is required but no supported entrypoint exists, fail.

#### Upstream resolution

- If both `LISTEN` and `SOCKET_PATH` are set, fail.
- If `LISTEN` is set, use TCP.
- If `SOCKET_PATH` is set, use unix socket.
- If neither is set, infer a transport from detected app kind:
  - `main.ts` => allocate TCP listener
  - executable `main.py` => allocate TCP listener by default
- Blank `LISTEN=` still means allocate a free TCP port.

#### Compatibility validation

Fail fast on these conflicts:

- `main.ts` with explicit `SOCKET_PATH`
- absolute `SOCKET_PATH`
- invalid `LISTEN` with no parseable port suffix
- missing inferable command when `REVERSE_BIN_COMMAND` is absent
- any explicit transport choice that the inferred app kind cannot satisfy

### Child Environment Rules

Build envs from the single loaded `.env` map, then supplement only missing values:

- preserve unrelated `.env` values as-is
- preserve `PATH` passthrough behavior
- preserve `HOME=data/...` behavior when `data/` exists
- if TCP was inferred or blank `LISTEN=` was resolved, inject `LISTEN=<resolved address>`
- if unix socket was inferred or normalized, inject `SOCKET_PATH=<app-facing value>`
- do not invent legacy `REVERSE_PROXY_TO` for child apps

The detector JSON should still contain `reverse_proxy_to`, because reverse-bin consumes that field internally.

### Example App Migration

Update only the in-repo apps under `examples/reverse-proxy/apps/*`.

Required migration:

- `examples/reverse-proxy/apps/python3-unix-echo/.env`
  - replace legacy `REVERSE_PROXY_TO=unix/data/echo.sock`
  - with `SOCKET_PATH=data/echo.sock`

Review and update any example README text that still describes legacy app-facing env behavior.

---

## Implementation Checklist

### Task 1: Add failing tests for merged partial-config discovery

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

- [ ] Add a test proving `load_env_app_config()` returns partial config when only `LISTEN` is present
- [ ] Add a test proving `load_env_app_config()` returns partial config when only `SOCKET_PATH` is present
- [ ] Add a test proving missing `REVERSE_BIN_COMMAND` plus executable `main.py` infers `./main.py`
- [ ] Add a test proving missing `REVERSE_BIN_COMMAND` plus `main.ts` infers the Deno command
- [ ] Add a test proving missing upstream plus explicit `REVERSE_BIN_COMMAND` gets supplemented from detection
- [ ] Add a test proving `main.ts` plus explicit `SOCKET_PATH` fails with a specific incompatibility error
- [ ] Add a test proving both `LISTEN` and `SOCKET_PATH` still fail
- [ ] Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify the new tests fail for the expected reasons

### Task 2: Refactor discover-app into a single merge pipeline

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Test: `utils/discover-app/test_discover_app.py`

- [ ] Change `EnvAppConfig` to represent partial config with optional command and upstream fields
- [ ] Add example values in comments for both `EnvAppConfig` and `DiscoverAppResult`
- [ ] Introduce or refactor helpers so `.env` is loaded once and passed through the resolution flow
- [ ] Add a helper that detects app kind and transport capability separately from final command construction
- [ ] Add a helper that merges explicit config with detected defaults into one resolved config
- [ ] Keep `LISTEN` normalization and relative unix socket handling strict
- [ ] Keep `main.sh` unsupported in autodetection
- [ ] Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify the tests pass

### Task 3: Verify env supplementation behavior

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

- [ ] Add a test proving child envs are built from the already-loaded env map, not by re-reading `.env`
- [ ] Add a test proving explicit `LISTEN` is preserved when valid
- [ ] Add a test proving blank `LISTEN=` is replaced with the resolved listener value
- [ ] Add a test proving inferred TCP for `main.py` injects `LISTEN=<resolved address>`
- [ ] Add a test proving explicit `SOCKET_PATH` is preserved for a python unix app
- [ ] Add a test proving no child env contains `REVERSE_PROXY_TO=`
- [ ] Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify pass

### Task 4: Update CLI-path coverage

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Modify: `utils/discover-app/discover-app.py`
- Test: `utils/discover-app/test_discover_app.py`

- [ ] Add a CLI test for no-command `.env` plus executable `main.py` with `SOCKET_PATH=data/app.sock`
- [ ] Add a CLI test for no-command `.env` plus `LISTEN=8080` and `main.ts`
- [ ] Add a CLI test for explicit `REVERSE_BIN_COMMAND` with missing upstream that gets supplemented
- [ ] Add a CLI test for incompatible `main.ts` + `SOCKET_PATH` returning exit code 1 with a specific stderr message
- [ ] Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py` and verify pass

### Task 5: Migrate example apps under `examples/reverse-proxy/apps/*`

**Files:**
- Modify: `examples/reverse-proxy/apps/python3-unix-echo/.env`
- Modify: `examples/reverse-proxy/apps/python3-unix-echo/README.md`
- Search: `examples/reverse-proxy/apps/*`

- [ ] Replace legacy `REVERSE_PROXY_TO` usage in the unix echo example with `.env` keys that match the new merged discovery rules
- [ ] Update README wording so it describes `SOCKET_PATH` as the app-facing env var
- [ ] Search for remaining legacy app-facing references with `rg -n "REVERSE_PROXY_TO" examples/reverse-proxy/apps -S`
- [ ] Verify only intentional internal references remain, if any

### Task 6: Final verification

**Files:**
- Verify: `utils/discover-app/discover-app.py`
- Verify: `utils/discover-app/test_discover_app.py`
- Verify: `examples/reverse-proxy/apps/python3-unix-echo/.env`
- Verify: `examples/reverse-proxy/apps/python3-unix-echo/README.md`

- [ ] Run `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
- [ ] Run `uv run --with python-dotenv python utils/discover-app/discover-app.py --help`
- [ ] Run `rg -n "REVERSE_PROXY_TO" examples/reverse-proxy/apps -S`
- [ ] Review the diff for `discover-app.py`, tests, and migrated examples
- [ ] Commit with a concise conventional commit message
