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

    def test_build_discovery_result_returns_expected_json_shape(self) -> None:
        # Intent: verify the typed result helper returns the exact JSON object shape emitted by discover-app.
        result = discover_app.build_discovery_result(
            executable=["./main.py"],
            reverse_proxy_to="127.0.0.1:8080",
            working_directory="/tmp/example-app",
            envs=["REVERSE_PROXY_TO=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
        )

        self.assertEqual(
            result,
            {
                "executable": ["./main.py"],
                "reverse_proxy_to": "127.0.0.1:8080",
                "working_directory": "/tmp/example-app",
                "envs": ["REVERSE_PROXY_TO=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
            },
        )

    def test_discover_app_command_uses_explicit_listen_config(self) -> None:
        # Intent: verify explicit .env LISTEN config takes precedence and normalizes bare ports to localhost.
        (self.app_dir / ".env").write_text(
            'REVERSE_BIN_COMMAND="python3 server.py"\nLISTEN=8080\n'
        )

        command, reverse_proxy_to = discover_app.discover_app_command(
            self.app_dir,
            dot_env={
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "LISTEN": "8080",
            },
            fallback_reverse_proxy_to="127.0.0.1:9999",
        )

        self.assertEqual(command, ["python3", "server.py"])
        self.assertEqual(reverse_proxy_to, "127.0.0.1:8080")

    def test_discover_app_command_uses_explicit_socket_path_config(self) -> None:
        # Intent: verify explicit .env SOCKET_PATH config resolves to an absolute unix upstream target.
        command, reverse_proxy_to = discover_app.discover_app_command(
            self.app_dir,
            dot_env={
                "REVERSE_BIN_COMMAND": "python3 server.py",
                "SOCKET_PATH": "run/app.sock",
            },
            fallback_reverse_proxy_to="127.0.0.1:9999",
        )

        self.assertEqual(command, ["python3", "server.py"])
        self.assertEqual(reverse_proxy_to, f"unix/{(self.app_dir / 'run/app.sock').resolve()}")

    def test_discover_app_command_rejects_listen_and_socket_path_together(self) -> None:
        # Intent: verify explicit config rejects ambiguous upstream declarations when both LISTEN and SOCKET_PATH are set.
        with self.assertRaisesRegex(ValueError, "exactly one of LISTEN or SOCKET_PATH"):
            discover_app.discover_app_command(
                self.app_dir,
                dot_env={
                    "REVERSE_BIN_COMMAND": "python3 server.py",
                    "LISTEN": "127.0.0.1:8080",
                    "SOCKET_PATH": "run/app.sock",
                },
                fallback_reverse_proxy_to="127.0.0.1:9999",
            )

    def test_discover_app_command_rejects_missing_listen_and_socket_path(self) -> None:
        # Intent: verify explicit config rejects incomplete settings when command is present but no upstream is configured.
        with self.assertRaisesRegex(ValueError, "exactly one of LISTEN or SOCKET_PATH"):
            discover_app.discover_app_command(
                self.app_dir,
                dot_env={"REVERSE_BIN_COMMAND": "python3 server.py"},
                fallback_reverse_proxy_to="127.0.0.1:9999",
            )

    def test_discover_app_command_rejects_absolute_socket_path(self) -> None:
        # Intent: verify explicit config keeps SOCKET_PATH relative to the app directory.
        with self.assertRaisesRegex(ValueError, "Unix socket path must be relative"):
            discover_app.discover_app_command(
                self.app_dir,
                dot_env={
                    "REVERSE_BIN_COMMAND": "python3 server.py",
                    "SOCKET_PATH": "/tmp/app.sock",
                },
                fallback_reverse_proxy_to="127.0.0.1:9999",
            )

    def test_discover_app_command_rejects_listen_without_parseable_port_suffix(self) -> None:
        # Intent: verify explicit config fails hard when LISTEN does not end in an integer port.
        with self.assertRaisesRegex(ValueError, "Invalid LISTEN port"):
            discover_app.discover_app_command(
                self.app_dir,
                dot_env={
                    "REVERSE_BIN_COMMAND": "python3 server.py",
                    "LISTEN": "foo",
                },
                fallback_reverse_proxy_to="127.0.0.1:9999",
            )

    def test_build_child_envs_for_listen_only_sets_listen(self) -> None:
        # Intent: verify TCP child envs expose LISTEN only and do not leak legacy reverse-proxy variables.
        envs = discover_app.build_child_envs(
            dot_env={"CUSTOM": "1"},
            reverse_proxy_to="127.0.0.1:8080",
            working_dir=self.app_dir,
        )

        self.assertIn("LISTEN=127.0.0.1:8080", envs)
        self.assertIn("CUSTOM=1", envs)
        self.assertNotIn("REVERSE_PROXY_TO=127.0.0.1:8080", envs)
        self.assertFalse(any(env.startswith("PORT=") for env in envs))

    def test_build_child_envs_for_socket_only_sets_socket_path(self) -> None:
        # Intent: verify unix-socket child envs expose an absolute SOCKET_PATH and omit legacy proxy variables.
        reverse_proxy_to = f"unix/{(self.app_dir / 'run/app.sock').resolve()}"
        envs = discover_app.build_child_envs(
            dot_env={"CUSTOM": "1"},
            reverse_proxy_to=reverse_proxy_to,
            working_dir=self.app_dir,
        )

        self.assertIn(f"SOCKET_PATH={(self.app_dir / 'run/app.sock').resolve()}", envs)
        self.assertIn("CUSTOM=1", envs)
        self.assertNotIn(f"REVERSE_PROXY_TO={reverse_proxy_to}", envs)
        self.assertFalse(any(env.startswith("PORT=") for env in envs))

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
        # Intent: verify the CLI emits explicit LISTEN-based config and app envs without sandbox wrapping.
        (self.app_dir / ".env").write_text(
            'REVERSE_BIN_COMMAND="python3 server.py"\nLISTEN=8080\nCUSTOM=1\n'
        )

        completed = subprocess.run(
            [os.environ.get("PYTHON", "python"), str(MODULE_PATH), "--no-sandbox", str(self.app_dir)],
            check=True,
            capture_output=True,
            text=True,
        )

        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["python3", "server.py"])
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
        self.assertIn("LISTEN=127.0.0.1:8080", payload["envs"])
        self.assertIn("CUSTOM=1", payload["envs"])
        self.assertNotIn("REVERSE_PROXY_TO=127.0.0.1:8080", payload["envs"])


if __name__ == "__main__":
    unittest.main()
