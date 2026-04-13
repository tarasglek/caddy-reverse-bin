# Discover App Env Config Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace JSON-based discover-app configuration with `.env`-based explicit config using `REVERSE_BIN_COMMAND`, `LISTEN`, and `SOCKET_PATH`, while updating existing tests/examples to the new env conventions.

**Architecture:** Refactor `utils/discover-app/discover-app.py` so explicit config is loaded from `.env`, normalized into the existing `reverse_proxy_to` output field, and converted into child env vars using `LISTEN` or `SOCKET_PATH`. Keep autodetection as a fallback for `main.ts` and executable `main.py`, and remove `main.sh` support.

**Tech Stack:** Python 3.13, stdlib `shlex/json/typing`, `python-dotenv`, `unittest`, Go integration tests

---

### Task 1: Replace JSON-config tests with failing `.env` explicit-config tests

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing tests**

Add focused tests for:

```python
def test_discover_app_command_uses_explicit_listen_config(self) -> None:
    (self.app_dir / ".env").write_text(
        'REVERSE_BIN_COMMAND="python3 server.py"\nLISTEN=8080\n'
    )

    command, reverse_proxy_to = discover_app.discover_app_command(self.app_dir, dot_env={
        "REVERSE_BIN_COMMAND": "python3 server.py",
        "LISTEN": "8080",
    }, fallback_reverse_proxy_to="127.0.0.1:9999")

    self.assertEqual(command, ["python3", "server.py"])
    self.assertEqual(reverse_proxy_to, "127.0.0.1:8080")


def test_discover_app_command_uses_explicit_socket_path_config(self) -> None:
    command, reverse_proxy_to = discover_app.discover_app_command(self.app_dir, dot_env={
        "REVERSE_BIN_COMMAND": "python3 server.py",
        "SOCKET_PATH": "run/app.sock",
    }, fallback_reverse_proxy_to="127.0.0.1:9999")

    self.assertEqual(command, ["python3", "server.py"])
    self.assertEqual(reverse_proxy_to, f"unix/{(self.app_dir / 'run/app.sock').resolve()}")
```

Add invalid-config tests for:
- both `LISTEN` and `SOCKET_PATH`
- neither set
- absolute `SOCKET_PATH`
- `LISTEN=foo` causing hard error

**Step 2: Run test to verify it fails**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL because the JSON-based helpers no longer match the new contract

**Step 3: Write minimal implementation**

Add helpers for:
- parsing explicit config from `.env`
- splitting `REVERSE_BIN_COMMAND` with `shlex.split`
- normalizing `LISTEN` and `SOCKET_PATH`

**Step 4: Run test to verify it passes**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS for the new explicit-config tests

**Step 5: Commit**

```bash
git add utils/discover-app/test_discover_app.py utils/discover-app/discover-app.py
git commit -m "feat(discover-app): load explicit config from env"
```

### Task 2: Replace child env generation and remove old app env conventions

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing tests**

Add tests for child env generation:

```python
def test_build_child_envs_for_listen_only_sets_listen(self) -> None:
    envs = discover_app.build_child_envs(
        dot_env={"CUSTOM": "1"},
        reverse_proxy_to="127.0.0.1:8080",
        working_dir=self.app_dir,
    )

    self.assertIn("LISTEN=127.0.0.1:8080", envs)
    self.assertNotIn("REVERSE_PROXY_TO=127.0.0.1:8080", envs)
    self.assertFalse(any(env.startswith("PORT=") for env in envs))


def test_build_child_envs_for_socket_only_sets_socket_path(self) -> None:
    reverse_proxy_to = f"unix/{(self.app_dir / 'run/app.sock').resolve()}"
    envs = discover_app.build_child_envs(
        dot_env={"CUSTOM": "1"},
        reverse_proxy_to=reverse_proxy_to,
        working_dir=self.app_dir,
    )

    self.assertIn(f"SOCKET_PATH={(self.app_dir / 'run/app.sock').resolve()}", envs)
    self.assertNotIn(f"REVERSE_PROXY_TO={reverse_proxy_to}", envs)
```

**Step 2: Run test to verify it fails**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL because child env generation still uses `REVERSE_PROXY_TO`

**Step 3: Write minimal implementation**

Implement `build_child_envs()` and update `main()` to use it.
Preserve passthrough of unrelated `.env` values and `PATH`, and keep `HOME` handling for `data/`.

**Step 4: Run test to verify it passes**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS

**Step 5: Commit**

```bash
git add utils/discover-app/discover-app.py utils/discover-app/test_discover_app.py
git commit -m "refactor(discover-app): use listen and socket envs"
```

### Task 3: Update CLI-path tests and autodetection fallback coverage

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Modify: `utils/discover-app/discover-app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing tests**

Add tests for:

```python
def test_main_emits_explicit_listen_config_without_sandbox(self) -> None:
    (self.app_dir / ".env").write_text(
        'REVERSE_BIN_COMMAND="python3 server.py"\nLISTEN=8080\n'
    )

    completed = subprocess.run(...)
    payload = json.loads(completed.stdout)

    self.assertEqual(payload["executable"], ["python3", "server.py"])
    self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
    self.assertIn("LISTEN=127.0.0.1:8080", payload["envs"])


def test_detect_entrypoint_supports_main_py_fallback(self) -> None:
    script = self.app_dir / "main.py"
    script.write_text("#!/usr/bin/env python3\n")
    script.chmod(0o755)
    self.assertEqual(discover_app.detect_entrypoint(self.app_dir, "127.0.0.1:8080"), ["./main.py"])
```

Keep or update the `main.sh` rejection test.

**Step 2: Run test to verify it fails**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL until CLI output and env payload use the new conventions

**Step 3: Write minimal implementation**

Update CLI flow and helper signatures to work off `.env` explicit mode plus fallback autodetection.
Remove JSON-config helpers that are no longer needed.

**Step 4: Run test to verify it passes**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS

**Step 5: Commit**

```bash
git add utils/discover-app/discover-app.py utils/discover-app/test_discover_app.py
git commit -m "refactor(discover-app): simplify env-based discovery"
```

### Task 4: Update example apps and docs from `REVERSE_PROXY_TO` to `LISTEN` / `SOCKET_PATH`

**Files:**
- Modify: `examples/reverse-proxy/apps/python3-echo/main.py`
- Modify: `examples/reverse-proxy/apps/python3-unix-echo/main.py`
- Modify: `examples/reverse-proxy/apps/python3-unix-echo/README.md`
- Modify: `README.md`
- Search: `examples/`, `README.md`, `cmd/caddy/integration_test.go`

**Step 1: Write the failing expectations**

Identify current files/tests that refer to `REVERSE_PROXY_TO` as the app-facing env var and note the exact assertions/messages that need updating.

**Step 2: Run targeted verification to show current mismatch**

Run:
- `rg -n "REVERSE_PROXY_TO|PORT=" examples README.md cmd/caddy/integration_test.go -S`
Expected: matches in examples/docs/tests that need updating

**Step 3: Write minimal implementation**

Update example apps to read:
- `LISTEN` for TCP apps
- `SOCKET_PATH` for unix apps

Update docs to describe the new contract.
Keep reverse-bin/Caddy references to `reverse_proxy_to` only where discussing detector JSON internals.

**Step 4: Run verification to confirm references were updated**

Run:
- `rg -n "REVERSE_PROXY_TO|PORT=" examples README.md -S`
Expected: no app-facing references remain except intentional internal reverse-bin details

**Step 5: Commit**

```bash
git add examples/reverse-proxy/apps/python3-echo/main.py \
  examples/reverse-proxy/apps/python3-unix-echo/main.py \
  examples/reverse-proxy/apps/python3-unix-echo/README.md \
  README.md

git commit -m "docs(examples): adopt listen and socket env conventions"
```

### Task 5: Update integration tests and run final verification

**Files:**
- Modify: `cmd/caddy/integration_test.go`
- Test: `cmd/caddy/integration_test.go`
- Verify: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing test updates**

Update integration tests that currently depend on `REVERSE_PROXY_TO` in launched app behavior or detector payloads so they assert the new app env conventions instead.
Keep assertions specific and anti-fragile, with clear comments per repo guidance.

**Step 2: Run tests to verify they fail appropriately**

Run:
- `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
- `go test ./cmd/caddy -run 'TestDynamicDiscovery|TestReverseProxy'`
Expected: failures where tests still encode old env behavior

**Step 3: Write minimal implementation**

Adjust integration fixtures, inline detector payloads, and assertions to match:
- child apps consume `LISTEN` / `SOCKET_PATH`
- detector JSON still contains `reverse_proxy_to`

**Step 4: Run final verification**

Run:
- `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
- `uv run --with python-dotenv python utils/discover-app/discover-app.py --help`
- `go test ./cmd/caddy -run 'TestDynamicDiscovery|TestReverseProxy'`

Expected: all pass

**Step 5: Commit**

```bash
git add utils/discover-app/discover-app.py \
  utils/discover-app/test_discover_app.py \
  cmd/caddy/integration_test.go \
  examples/reverse-proxy/apps/python3-echo/main.py \
  examples/reverse-proxy/apps/python3-unix-echo/main.py \
  examples/reverse-proxy/apps/python3-unix-echo/README.md \
  README.md

git commit -m "feat(discover-app): switch apps to env-based config"
```
