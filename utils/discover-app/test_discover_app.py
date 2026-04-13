import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("discover-app.py")
spec = importlib.util.spec_from_file_location("discover_app", MODULE_PATH)
if spec is None or spec.loader is None:
    raise RuntimeError(f"Could not load module spec from {MODULE_PATH}")

discover_app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(discover_app)


class DiscoverAppResultTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.app_dir = Path(self.temp_dir.name)

    def run_cli(self) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [os.environ.get("PYTHON", "python"), str(MODULE_PATH), "--no-sandbox", str(self.app_dir)],
            capture_output=True,
            text=True,
        )

    def make_main_py(self) -> None:
        script = self.app_dir / "main.py"
        script.write_text("#!/usr/bin/env python3\n")
        script.chmod(0o755)

    def envs_as_map(self, envs: list[str]) -> dict[str, str]:
        return dict(env.split("=", 1) for env in envs)

    def test_build_discovery_result_returns_expected_json_shape(self) -> None:
        # Intent: verify the typed result helper returns the exact JSON object shape emitted by discover-app.
        result = discover_app.build_discovery_result(
            executable=["./main.py"],
            reverse_proxy_to="127.0.0.1:8080",
            working_directory="/tmp/example-app",
            envs=["LISTEN=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
        )

        self.assertEqual(
            result,
            {
                "executable": ["./main.py"],
                "reverse_proxy_to": "127.0.0.1:8080",
                "working_directory": "/tmp/example-app",
                "envs": ["LISTEN=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
            },
        )

    def test_load_env_app_config_reads_partial_listen_values_without_command(self) -> None:
        # Intent: verify .env LISTEN values are treated as partial config even when command inference is still needed.
        config = discover_app.load_env_app_config(
            {
                "LISTEN": "8080",
            }
        )

        self.assertEqual(
            config,
            {
                "command": None,
                "listen": "8080",
                "socket_path": None,
            },
        )

    def test_load_env_app_config_reads_partial_socket_path_values_without_command(self) -> None:
        # Intent: verify .env SOCKET_PATH values are treated as partial config even when command inference is still needed.
        config = discover_app.load_env_app_config(
            {
                "SOCKET_PATH": "run/app.sock",
            }
        )

        self.assertEqual(
            config,
            {
                "command": None,
                "listen": None,
                "socket_path": "run/app.sock",
            },
        )

    def test_load_env_app_config_reads_explicit_listen_values(self) -> None:
        # Intent: verify explicit .env command config is captured in the EnvAppConfig typed shape.
        config = discover_app.load_env_app_config(
            {
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "LISTEN": "8080",
            }
        )

        self.assertEqual(
            config,
            {
                "command": ["sh", "-c", "python3 server.py"],
                "listen": "8080",
                "socket_path": None,
            },
        )

    def test_load_env_app_config_rejects_listen_and_socket_path_together(self) -> None:
        # Intent: verify merged config still rejects ambiguous upstream declarations when both LISTEN and SOCKET_PATH are set.
        with self.assertRaisesRegex(ValueError, "both LISTEN and SOCKET_PATH"):
            discover_app.load_env_app_config(
                {
                    "LISTEN": "127.0.0.1:8080",
                    "SOCKET_PATH": "run/app.sock",
                }
            )

    def test_load_env_app_config_allows_missing_upstream_when_command_is_present(self) -> None:
        # Intent: verify merged config allows command-only .env values so upstream can be supplemented from detection.
        config = discover_app.load_env_app_config({"REVERSE_BIN_COMMAND": "python3 server.py"})

        self.assertEqual(
            config,
            {
                "command": ["sh", "-c", "python3 server.py"],
                "listen": None,
                "socket_path": None,
            },
        )

    def test_build_explicit_app_uses_explicit_listen_config(self) -> None:
        # Intent: verify explicit LISTEN config normalizes the proxy target while preserving the app env value.
        executable, reverse_proxy_to, envs = discover_app.build_explicit_app(
            self.app_dir,
            dot_env={
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "LISTEN": "8080",
                "CUSTOM": "1",
            },
            config={
                "command": ["sh", "-c", "python3 server.py"],
                "listen": "8080",
                "socket_path": None,
            },
        )

        self.assertEqual(executable, ["sh", "-c", "python3 server.py"])
        self.assertEqual(reverse_proxy_to, "127.0.0.1:8080")
        self.assertIn("LISTEN=8080", envs)
        self.assertIn("CUSTOM=1", envs)

    def test_build_explicit_app_allocates_port_for_blank_listen(self) -> None:
        # Intent: verify blank LISTEN values allocate a free port and pass the resolved value to the app.
        executable, reverse_proxy_to, envs = discover_app.build_explicit_app(
            self.app_dir,
            dot_env={
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "LISTEN": "",
            },
            config={
                "command": ["sh", "-c", "python3 server.py"],
                "listen": "",
                "socket_path": None,
            },
        )

        self.assertEqual(executable, ["sh", "-c", "python3 server.py"])
        self.assertRegex(reverse_proxy_to, r"^127\.0\.0\.1:\d+$")
        self.assertIn(f"LISTEN={reverse_proxy_to}", envs)

    def test_build_explicit_app_uses_socket_path_config(self) -> None:
        # Intent: verify explicit SOCKET_PATH config resolves the proxy target while passing the original env through.
        executable, reverse_proxy_to, envs = discover_app.build_explicit_app(
            self.app_dir,
            dot_env={
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "SOCKET_PATH": "run/app.sock",
                "CUSTOM": "1",
            },
            config={
                "command": ["sh", "-c", "python3 server.py"],
                "listen": None,
                "socket_path": "run/app.sock",
            },
        )

        self.assertEqual(executable, ["sh", "-c", "python3 server.py"])
        self.assertEqual(reverse_proxy_to, f"unix/{(self.app_dir / 'run/app.sock').resolve()}")
        self.assertIn("SOCKET_PATH=run/app.sock", envs)
        self.assertIn("CUSTOM=1", envs)

    def test_build_explicit_app_rejects_absolute_socket_path(self) -> None:
        # Intent: verify explicit config keeps SOCKET_PATH relative to the app directory.
        with self.assertRaisesRegex(ValueError, "Unix socket path must be relative"):
            discover_app.build_explicit_app(
                self.app_dir,
                dot_env={
                    "REVERSE_BIN_COMMAND": "python3 server.py",
                    "SOCKET_PATH": "/tmp/app.sock",
                },
                config={
                    "command": ["sh", "-c", "python3 server.py"],
                    "listen": None,
                    "socket_path": "/tmp/app.sock",
                },
            )

    def test_build_explicit_app_rejects_listen_without_parseable_port_suffix(self) -> None:
        # Intent: verify explicit config fails hard when LISTEN does not end in an integer port.
        with self.assertRaisesRegex(ValueError, "Invalid LISTEN port"):
            discover_app.build_explicit_app(
                self.app_dir,
                dot_env={
                    "REVERSE_BIN_COMMAND": "python3 server.py",
                    "LISTEN": "foo",
                },
                config={
                    "command": ["sh", "-c", "python3 server.py"],
                    "listen": "foo",
                    "socket_path": None,
                },
            )

    def test_build_app_envs_passes_through_dot_env_values(self) -> None:
        # Intent: verify child env generation is a passthrough of app envs instead of a translation layer.
        envs = discover_app.build_app_envs(
            self.app_dir,
            dot_env={"LISTEN": "8080", "CUSTOM": "1"},
        )

        self.assertIn("LISTEN=8080", envs)
        self.assertIn("CUSTOM=1", envs)
        self.assertFalse(any(env.startswith("REVERSE_PROXY_TO=") for env in envs))

    def test_build_app_envs_uses_loaded_env_map_without_rereading_dot_env_file(self) -> None:
        # Intent: verify child env assembly uses the already-loaded .env map even if the on-disk file later changes.
        (self.app_dir / ".env").write_text("LISTEN=9999\nCUSTOM=from-disk\n")

        envs = discover_app.build_app_envs(
            self.app_dir,
            dot_env={"LISTEN": "8080", "CUSTOM": "from-memory"},
        )

        env_map = self.envs_as_map(envs)
        self.assertEqual(env_map["LISTEN"], "8080")
        self.assertEqual(env_map["CUSTOM"], "from-memory")

    def test_build_app_envs_applies_overrides(self) -> None:
        # Intent: verify generated env values can override blank explicit config when a port is auto-assigned.
        envs = discover_app.build_app_envs(
            self.app_dir,
            dot_env={"LISTEN": "", "CUSTOM": "1"},
            overrides={"LISTEN": "127.0.0.1:8080"},
        )

        self.assertIn("LISTEN=127.0.0.1:8080", envs)
        self.assertIn("CUSTOM=1", envs)

    def test_discover_app_command_ignores_reverse_bin_app_json_during_fallback(self) -> None:
        # Intent: verify fallback autodetection ignores legacy JSON config files and selects supported entrypoints instead.
        (self.app_dir / "reverse-bin-app.json").write_text(
            json.dumps({"command": ["./custom-server"], "socket": 9000})
        )
        (self.app_dir / "main.ts").write_text("console.log('hello');\n")

        command, reverse_proxy_to = discover_app.discover_app_command(
            self.app_dir,
            dot_env={},
            fallback_reverse_proxy_to="127.0.0.1:8080",
        )

        self.assertEqual(
            command,
            ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", "8080", "main.ts"],
        )
        self.assertEqual(reverse_proxy_to, "127.0.0.1:8080")

    def test_detect_entrypoint_supports_main_ts_fallback(self) -> None:
        # Intent: verify automatic fallback still starts main.ts apps with the derived TCP port.
        (self.app_dir / "main.ts").write_text("console.log('hello');\n")

        self.assertEqual(
            discover_app.detect_entrypoint(self.app_dir, "127.0.0.1:8080"),
            ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", "8080", "main.ts"],
        )

    def test_detect_entrypoint_supports_main_py_fallback(self) -> None:
        # Intent: verify automatic fallback still supports executable Python entrypoints.
        script = self.app_dir / "main.py"
        script.write_text("#!/usr/bin/env python3\n")
        script.chmod(0o755)

        self.assertEqual(discover_app.detect_entrypoint(self.app_dir, "127.0.0.1:8080"), ["./main.py"])

    def test_detect_entrypoint_rejects_main_sh_autodetection(self) -> None:
        # Intent: verify shell scripts are no longer auto-detected as supported app entrypoints.
        script = self.app_dir / "main.sh"
        script.write_text("#!/bin/sh\nexit 0\n")
        script.chmod(0o755)

        with self.assertRaises(FileNotFoundError):
            discover_app.detect_entrypoint(self.app_dir, "127.0.0.1:8080")

    def test_main_emits_explicit_listen_config_without_sandbox(self) -> None:
        # Intent: verify the CLI keeps explicit LISTEN env values while normalizing the internal proxy target.
        (self.app_dir / ".env").write_text(
            'REVERSE_BIN_COMMAND="python3 server.py"\nLISTEN=8080\nCUSTOM=1\n'
        )

        completed = self.run_cli()
        self.assertEqual(completed.returncode, 0, completed.stderr)

        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["sh", "-c", "python3 server.py"])
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
        self.assertIn("LISTEN=8080", payload["envs"])
        self.assertIn("CUSTOM=1", payload["envs"])

    def test_resolve_app_preserves_explicit_listen_in_child_envs(self) -> None:
        # Intent: verify a valid explicit LISTEN value remains app-facing while reverse-bin keeps the normalized proxy target.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"LISTEN": "8080", "CUSTOM": "1"})
        env_map = self.envs_as_map(
            discover_app.build_app_envs(self.app_dir, {"LISTEN": "8080", "CUSTOM": "1"}, resolved.env_overrides)
        )

        self.assertEqual(resolved.executable, ["./main.py"])
        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:8080")
        self.assertEqual(env_map["LISTEN"], "8080")
        self.assertEqual(env_map["CUSTOM"], "1")

    def test_resolve_app_replaces_blank_listen_with_resolved_listener(self) -> None:
        # Intent: verify a blank LISTEN= entry is supplemented with the resolved listener address before launch.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"LISTEN": ""})
        env_map = self.envs_as_map(
            discover_app.build_app_envs(self.app_dir, {"LISTEN": ""}, resolved.env_overrides)
        )

        self.assertRegex(resolved.reverse_proxy_to, r"^127\.0\.0\.1:\d+$")
        self.assertEqual(env_map["LISTEN"], resolved.reverse_proxy_to)

    def test_resolve_app_infers_tcp_listener_for_main_py_child_envs(self) -> None:
        # Intent: verify autodetected Python apps receive LISTEN=<resolved address> when no upstream env was provided.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={})
        env_map = self.envs_as_map(discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides))

        self.assertEqual(resolved.executable, ["./main.py"])
        self.assertRegex(resolved.reverse_proxy_to, r"^127\.0\.0\.1:\d+$")
        self.assertEqual(env_map["LISTEN"], resolved.reverse_proxy_to)

    def test_resolve_app_preserves_explicit_socket_path_for_python_unix_app(self) -> None:
        # Intent: verify explicit SOCKET_PATH values stay app-facing for supported Python unix-socket apps.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"SOCKET_PATH": "data/app.sock", "CUSTOM": "1"})
        env_map = self.envs_as_map(
            discover_app.build_app_envs(self.app_dir, {"SOCKET_PATH": "data/app.sock", "CUSTOM": "1"}, resolved.env_overrides)
        )

        self.assertEqual(resolved.executable, ["./main.py"])
        self.assertEqual(resolved.reverse_proxy_to, f"unix/{(self.app_dir / 'data/app.sock').resolve()}")
        self.assertEqual(env_map["SOCKET_PATH"], "data/app.sock")
        self.assertEqual(env_map["CUSTOM"], "1")

    def test_resolve_app_never_injects_reverse_proxy_to_into_child_envs(self) -> None:
        # Intent: verify child env supplementation never invents the legacy REVERSE_PROXY_TO app-facing variable.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={})
        envs = discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides)

        self.assertFalse(any(env.startswith("REVERSE_PROXY_TO=") for env in envs))

    def test_main_inferrs_main_py_command_from_partial_listen_config(self) -> None:
        # Intent: verify a LISTEN-only .env still infers ./main.py instead of requiring REVERSE_BIN_COMMAND.
        self.make_main_py()
        (self.app_dir / ".env").write_text("LISTEN=8080\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["./main.py"])
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
        self.assertIn("LISTEN=8080", payload["envs"])

    def test_main_inferrs_main_py_command_from_partial_socket_path_config(self) -> None:
        # Intent: verify a SOCKET_PATH-only .env still infers ./main.py for supported unix-socket Python apps.
        self.make_main_py()
        (self.app_dir / ".env").write_text("SOCKET_PATH=data/app.sock\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["./main.py"])
        self.assertEqual(payload["reverse_proxy_to"], f"unix/{(self.app_dir / 'data/app.sock').resolve()}")
        self.assertIn("SOCKET_PATH=data/app.sock", payload["envs"])

    def test_main_inferrs_main_ts_command_from_partial_listen_config(self) -> None:
        # Intent: verify a LISTEN-only .env still infers the Deno entrypoint command for supported TypeScript apps.
        (self.app_dir / "main.ts").write_text("console.log('hello');\n")
        (self.app_dir / ".env").write_text("LISTEN=8080\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(
            payload["executable"],
            ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", "8080", "main.ts"],
        )
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
        self.assertIn("LISTEN=8080", payload["envs"])

    def test_main_supplements_missing_upstream_for_explicit_command(self) -> None:
        # Intent: verify a command-only .env gets a missing TCP listener from detection instead of failing upfront.
        self.make_main_py()
        (self.app_dir / ".env").write_text('REVERSE_BIN_COMMAND="python3 server.py"\nCUSTOM=1\n')

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["sh", "-c", "python3 server.py"])
        self.assertRegex(payload["reverse_proxy_to"], r"^127\.0\.0\.1:\d+$")
        self.assertIn(f"LISTEN={payload['reverse_proxy_to']}", payload["envs"])
        self.assertIn("CUSTOM=1", payload["envs"])

    def test_main_rejects_main_ts_with_explicit_socket_path(self) -> None:
        # Intent: verify an explicit unix socket choice fails fast when the inferred TypeScript runtime only supports TCP.
        (self.app_dir / "main.ts").write_text("console.log('hello');\n")
        (self.app_dir / ".env").write_text("SOCKET_PATH=data/app.sock\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 1)
        self.assertRegex(completed.stderr, r"main\.ts.*SOCKET_PATH")

    def test_main_emits_autodetected_listen_for_main_py_without_env(self) -> None:
        # Intent: verify main.py fallback allocates a TCP listener and passes it to the app as LISTEN.
        self.make_main_py()

        completed = self.run_cli()
        self.assertEqual(completed.returncode, 0, completed.stderr)

        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["./main.py"])
        self.assertRegex(payload["reverse_proxy_to"], r"^127\.0\.0\.1:\d+$")
        self.assertIn(f"LISTEN={payload['reverse_proxy_to']}", payload["envs"])


if __name__ == "__main__":
    unittest.main()
