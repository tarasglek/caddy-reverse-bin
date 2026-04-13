# Discover App JSON Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add `reverse-bin-app.json` support to `utils/discover-app/discover-app.py`, keep autodetection as a fallback, and remove `main.sh` support.

**Architecture:** Refactor `discover-app.py` into small helpers for config loading, socket normalization, autodetection, and result assembly. Keep the output JSON shape and sandboxing behavior unchanged while preferring config-driven app definitions when the config file is present.

**Tech Stack:** Python 3.13, stdlib `json` and `typing`, `python-dotenv`, `unittest`

---

### Task 1: Add failing tests for config loading and socket normalization

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing tests**

```python
def test_load_app_definition_reads_command_and_port_socket(self) -> None:
    app_dir = Path(self.temp_dir.name)
    (app_dir / "reverse-bin-app.json").write_text(
        json.dumps({"command": ["python3", "server.py"], "socket": 8080})
    )

    definition = discover_app.load_app_definition(app_dir)

    self.assertEqual(definition["command"], ["python3", "server.py"])
    self.assertEqual(definition["socket"], 8080)


def test_normalize_socket_value_resolves_relative_unix_socket(self) -> None:
    app_dir = Path(self.temp_dir.name)

    result = discover_app.normalize_reverse_proxy_target(app_dir, "run/app.sock")

    self.assertEqual(result, f"unix/{(app_dir / 'run/app.sock').resolve()}")
```

**Step 2: Run tests to verify they fail**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL with missing helper errors

**Step 3: Write minimal implementation**

Add:
- app-definition typed dicts
- `load_app_definition()`
- `normalize_reverse_proxy_target()`

**Step 4: Run tests to verify they pass**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS for the new tests

**Step 5: Commit**

```bash
git add utils/discover-app/test_discover_app.py utils/discover-app/discover-app.py
git commit -m "feat(discover-app): add app json config support"
```

### Task 2: Add failing tests for autodetection fallback and main.sh removal

**Files:**
- Modify: `utils/discover-app/test_discover_app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing tests**

```python
def test_detect_entrypoint_prefers_reverse_bin_app_json_when_present(self) -> None:
    app_dir = Path(self.temp_dir.name)
    (app_dir / "reverse-bin-app.json").write_text(
        json.dumps({"command": ["./custom-server"], "socket": 9000})
    )
    (app_dir / "main.ts").write_text("console.log('ignored');")

    command, reverse_proxy_to = discover_app.discover_app_command(app_dir, "127.0.0.1:1111")

    self.assertEqual(command, ["./custom-server"])
    self.assertEqual(reverse_proxy_to, "127.0.0.1:9000")


def test_detect_entrypoint_rejects_main_sh_autodetection(self) -> None:
    app_dir = Path(self.temp_dir.name)
    script = app_dir / "main.sh"
    script.write_text("#!/bin/sh\nexit 0\n")
    script.chmod(0o755)

    with self.assertRaises(FileNotFoundError):
        discover_app.detect_entrypoint(app_dir, "127.0.0.1:8080")
```

**Step 2: Run tests to verify they fail**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL for missing config-aware discovery and stale `main.sh` behavior

**Step 3: Write minimal implementation**

Add a helper such as `discover_app_command()` that:
- uses config when present
- otherwise falls back to autodetection
- removes `main.sh` from autodetection logic

**Step 4: Run tests to verify they pass**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS for all tests

**Step 5: Commit**

```bash
git add utils/discover-app/test_discover_app.py utils/discover-app/discover-app.py
git commit -m "refactor(discover-app): simplify discovery flow"
```

### Task 3: Wire config-aware discovery into main and verify CLI behavior

**Files:**
- Modify: `utils/discover-app/discover-app.py`
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing test**

```python
def test_main_emits_configured_result_without_sandbox(self) -> None:
    app_dir = Path(self.temp_dir.name)
    (app_dir / "reverse-bin-app.json").write_text(
        json.dumps({"command": ["python3", "server.py"], "socket": 8080})
    )

    completed = subprocess.run(
        ["python", str(MODULE_PATH), "--no-sandbox", str(app_dir)],
        check=True,
        capture_output=True,
        text=True,
    )

    payload = json.loads(completed.stdout)
    self.assertEqual(payload["executable"], ["python3", "server.py"])
    self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
```

**Step 2: Run test to verify it fails**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: FAIL until `main()` uses config-aware discovery

**Step 3: Write minimal implementation**

Update `main()` to:
- derive the initial target as today for env compatibility
- let config override the target when present
- keep final result assembly and sandbox wrapping intact

**Step 4: Run tests to verify they pass**

Run: `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
Expected: PASS

**Step 5: Commit**

```bash
git add utils/discover-app/discover-app.py utils/discover-app/test_discover_app.py
git commit -m "refactor(discover-app): emit config-driven targets"
```

### Task 4: Run final verification and document the behavior change

**Files:**
- Modify: `README.md`
- Modify: `utils/discover-app/discover-app.py` comments if needed
- Test: `utils/discover-app/test_discover_app.py`

**Step 1: Write the failing documentation test equivalent**

Re-read current README wording and identify the stale parts that still describe only automatic launch detection.

**Step 2: Run verification commands first on code-only changes**

Run:
- `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
- `uv run --with python-dotenv python utils/discover-app/discover-app.py --help`

Expected: both succeed

**Step 3: Update docs minimally**

Document that the detector now prefers `reverse-bin-app.json` and falls back to auto-detection for `main.ts` and executable `main.py`.

**Step 4: Run final verification again**

Run:
- `uv run --with python-dotenv python -m unittest utils/discover-app/test_discover_app.py`
- `uv run --with python-dotenv python utils/discover-app/discover-app.py --help`

Expected: both succeed

**Step 5: Commit**

```bash
git add README.md utils/discover-app/discover-app.py utils/discover-app/test_discover_app.py
git commit -m "docs(discover-app): document app json config"
```
