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

    def test_load_app_definition_reads_command_and_port_socket(self) -> None:
        # Intent: verify reverse-bin-app.json is parsed into the configured command and socket fields.
        (self.app_dir / "reverse-bin-app.json").write_text(
            json.dumps({"command": ["python3", "server.py"], "socket": 8080})
        )

        definition = discover_app.load_app_definition(self.app_dir)

        self.assertEqual(definition["command"], ["python3", "server.py"])
        self.assertEqual(definition["socket"], 8080)

    def test_normalize_reverse_proxy_target_resolves_relative_unix_socket(self) -> None:
        # Intent: verify relative socket paths in config resolve to absolute unix proxy targets under the app dir.
        result = discover_app.normalize_reverse_proxy_target(self.app_dir, "run/app.sock")

        self.assertEqual(result, f"unix/{(self.app_dir / 'run/app.sock').resolve()}")

    def test_discover_app_command_prefers_reverse_bin_app_json_when_present(self) -> None:
        # Intent: verify config-driven app definitions override automatic main.ts detection when both are present.
        (self.app_dir / "reverse-bin-app.json").write_text(
            json.dumps({"command": ["./custom-server"], "socket": 9000})
        )
        (self.app_dir / "main.ts").write_text("console.log('ignored');\n")

        command, reverse_proxy_to = discover_app.discover_app_command(self.app_dir, "127.0.0.1:1111")

        self.assertEqual(command, ["./custom-server"])
        self.assertEqual(reverse_proxy_to, "127.0.0.1:9000")

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
