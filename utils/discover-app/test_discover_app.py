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

    def test_detect_entrypoint_rejects_main_sh_autodetection(self) -> None:
        # Intent: verify shell scripts are no longer auto-detected as supported app entrypoints.
        script = self.app_dir / "main.sh"
        script.write_text("#!/bin/sh\nexit 0\n")
        script.chmod(0o755)

        with self.assertRaises(FileNotFoundError):
            discover_app.detect_entrypoint(self.app_dir, "127.0.0.1:8080")

    def test_main_emits_configured_result_without_sandbox(self) -> None:
        # Intent: verify CLI output uses reverse-bin-app.json command and normalized target when sandboxing is disabled.
        (self.app_dir / "reverse-bin-app.json").write_text(
            json.dumps({"command": ["python3", "server.py"], "socket": 8080})
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


if __name__ == "__main__":
    unittest.main()
