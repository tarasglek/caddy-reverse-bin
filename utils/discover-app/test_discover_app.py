import importlib.util
import json
import os
import subprocess
import sys
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

    def run_cli(self, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [os.environ.get("PYTHON", sys.executable), str(MODULE_PATH), "--no-sandbox", str(self.app_dir)],
            capture_output=True,
            text=True,
            env=env,
        )

    def make_main_py(self) -> None:
        script = self.app_dir / "main.py"
        script.write_text("#!/usr/bin/env python3\n")
        script.chmod(0o755)

    def make_cmd_sh(self) -> None:
        script = self.app_dir / "cmd.sh"
        script.write_text("#!/bin/sh\nexit 0\n")
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

    def test_build_discovery_result_includes_health_overrides_when_present(self) -> None:
        # Intent: verify the typed result helper can include health override fields for reverse-bin detector output.
        result = discover_app.build_discovery_result(
            executable=["./main.py"],
            reverse_proxy_to="127.0.0.1:8080",
            working_directory="/tmp/example-app",
            envs=["LISTEN=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
            health_method="GET",
            health_path="/health",
        )

        self.assertEqual(result["health_method"], "GET")
        self.assertEqual(result["health_path"], "/health")

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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
                "socket_path": None,
                "health_method": None,
                "health_path": None,
                "health_status": None,
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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
                "socket_path": "run/app.sock",
                "health_method": None,
                "health_path": None,
                "health_status": None,
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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
                "socket_path": None,
                "health_method": None,
                "health_path": None,
                "health_status": None,
            },
        )

    def test_load_env_app_config_reads_health_override_values(self) -> None:
        # Intent: verify .env health keys are parsed into detector override config for any discovered app.
        config = discover_app.load_env_app_config(
            {
                "LISTEN": "8080",
                "REVERSE_BIN_HEALTH_METHOD": "get",
                "REVERSE_BIN_HEALTH_PATH": "/health",
            }
        )

        self.assertEqual(
            config,
            {
                "command": None,
                "listen": "8080",
                "reverse_bin_host": None,
                "reverse_bin_port": None,
                "socket_path": None,
                "health_method": "GET",
                "health_path": "/health",
                "health_status": None,
            },
        )

    def test_load_env_app_config_rejects_listen_and_socket_path_together(self) -> None:
        # Intent: verify merged config still rejects ambiguous upstream declarations when both LISTEN and SOCKET_PATH are set.
        with self.assertRaisesRegex(ValueError, "both TCP listener config and SOCKET_PATH"):
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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
                "socket_path": None,
                "health_method": None,
                "health_path": None,
                "health_status": None,
            },
        )

    def test_load_env_app_config_rejects_health_method_without_path(self) -> None:
        # Intent: verify partial health config fails fast so reverse-bin never gets half-defined health overrides.
        with self.assertRaisesRegex(ValueError, "REVERSE_BIN_HEALTH_METHOD and REVERSE_BIN_HEALTH_PATH"):
            discover_app.load_env_app_config({"REVERSE_BIN_HEALTH_METHOD": "GET"})

    def test_load_env_app_config_rejects_health_path_without_method(self) -> None:
        # Intent: verify partial health config fails fast so reverse-bin never gets half-defined health overrides.
        with self.assertRaisesRegex(ValueError, "REVERSE_BIN_HEALTH_METHOD and REVERSE_BIN_HEALTH_PATH"):
            discover_app.load_env_app_config({"REVERSE_BIN_HEALTH_PATH": "/health"})

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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
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
                "reverse_bin_host": None,
                "reverse_bin_port": None,
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

    def test_resolve_app_sets_deno_no_update_check_for_main_ts(self) -> None:
        # Intent: avoid Deno update checks in reverse-bin managed Deno apps without requiring every app .env to set it.
        (self.app_dir / "main.ts").write_text("export default { fetch: () => new Response('ok') };\n")

        resolved = discover_app.resolve_app(self.app_dir, dot_env={})
        envs = discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides)

        self.assertIn("DENO_NO_UPDATE_CHECK=1", envs)

    def test_find_env_source_rejects_plaintext_and_encrypted_json_files(self) -> None:
        # Intent: verify app config has exactly one env source and rejects ambiguous plaintext plus encrypted JSON secrets.
        (self.app_dir / ".env").write_text("CUSTOM=plain\n")
        (self.app_dir / "secrets.enc.json").write_text('{"CUSTOM":"encrypted"}\n')

        with self.assertRaisesRegex(ValueError, "Cannot use both \\.env and encrypted env file secrets\\.enc\\.json"):
            discover_app.find_env_source(self.app_dir)

    def test_load_app_env_decrypts_sops_json_to_dotenv_without_writing_plaintext(self) -> None:
        # Intent: verify encrypted JSON content is decrypted to dotenv in memory, parsed, and never materialized beside secrets.enc.json.
        encrypted_path = self.app_dir / "secrets.enc.json"
        encrypted_path.write_text('{"sops":"metadata placeholder"}\n')
        calls: list[list[str]] = []

        def fake_runner(args: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
            calls.append(args)
            return subprocess.CompletedProcess(args, 0, stdout="SECRET=decrypted\nEMPTY=\nIGNORED\n", stderr="")

        env_map = discover_app.load_app_env(self.app_dir, runner=fake_runner)

        self.assertEqual(env_map["SECRET"], "decrypted")
        self.assertEqual(env_map["EMPTY"], "")
        self.assertNotIn("IGNORED", env_map)
        self.assertEqual(
            calls,
            [["sops", "--decrypt", "--input-type", "json", "--output-type", "dotenv", str(encrypted_path)]],
        )
        self.assertEqual(sorted(path.name for path in self.app_dir.iterdir()), ["secrets.enc.json"])

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

    def test_wrap_landrun_keeps_default_filesystem_policy_narrow(self) -> None:
        # Intent: verify the default landrun policy does not grant read-only access to the whole filesystem.
        command = discover_app.wrap_landrun(["./launch.sh"], include_path=False)

        command_text = " ".join(command)
        self.assertIn("/etc", command)
        self.assertIn("/proc", command_text)
        self.assertIn("/sys/fs/cgroup", command_text)
        self.assertNotIn("/,/etc", command_text)
        self.assertNotIn("--unrestricted-filesystem", command)

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
        self.assertNotIn("health_method", payload)
        self.assertNotIn("health_path", payload)
        self.assertIn("LISTEN=8080", payload["envs"])
        self.assertIn("CUSTOM=1", payload["envs"])

    def test_main_rejects_missing_command_and_missing_detectable_entrypoint(self) -> None:
        # Intent: verify the CLI still hard-fails when command inference is required but no supported entrypoint exists.
        completed = self.run_cli()

        self.assertEqual(completed.returncode, 1)
        self.assertRegex(
            completed.stderr,
            r"^Error: No supported entry point \(main\.ts, executable main\.py, index\.html, or dist/index\.html\) found in .+\n$",
        )

    def test_resolve_app_preserves_explicit_listen_in_child_envs(self) -> None:
        # Intent: verify a valid explicit LISTEN value remains app-facing while reverse-bin keeps the normalized proxy target.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"LISTEN": "8080", "CUSTOM": "1"})
        env_map = self.envs_as_map(
            discover_app.build_app_envs(self.app_dir, {"LISTEN": "8080", "CUSTOM": "1"}, resolved.env_overrides)
        )

        self.assertEqual(resolved.executable, ["./main.py"])
        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:8080")
        self.assertEqual(resolved.health_method, None)
        self.assertEqual(resolved.health_path, None)
        self.assertEqual(env_map["LISTEN"], "8080")
        self.assertEqual(env_map["CUSTOM"], "1")

    def test_resolve_app_carries_health_override_for_autodetected_app(self) -> None:
        # Intent: verify autodetected apps can override health without requiring REVERSE_BIN_COMMAND explicit mode.
        self.make_main_py()

        resolved = discover_app.resolve_app(
            self.app_dir,
            dot_env={"LISTEN": "8080", "REVERSE_BIN_HEALTH_METHOD": "GET", "REVERSE_BIN_HEALTH_PATH": "/health"},
        )

        self.assertEqual(resolved.executable, ["./main.py"])
        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:8080")
        self.assertEqual(resolved.health_method, "GET")
        self.assertEqual(resolved.health_path, "/health")

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
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "127.0.0.1")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], resolved.reverse_proxy_to.rsplit(":", 1)[1])

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

    def test_main_uses_sops_json_for_app_env(self) -> None:
        # Intent: verify the CLI decrypts secrets.enc.json through sops and feeds resulting dotenv keys to the app env.
        self.make_main_py()
        (self.app_dir / "secrets.enc.json").write_text('{"sops":"metadata placeholder"}\n')
        fake_bin = self.app_dir / "fake-bin"
        fake_bin.mkdir()
        fake_sops = fake_bin / "sops"
        fake_sops.write_text(
            "#!/bin/sh\n"
            "printf '%s\\n' 'REVERSE_BIN_PORT=9999' 'SECRET_KEY=from-sops'\n"
        )
        fake_sops.chmod(0o755)
        env = os.environ.copy()
        env["PATH"] = f"{fake_bin}{os.pathsep}{env.get('PATH', '')}"

        completed = self.run_cli(env=env)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        env_map = self.envs_as_map(payload["envs"])
        self.assertEqual(payload["executable"], ["./main.py"])
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:9999")
        self.assertEqual(env_map["SECRET_KEY"], "from-sops")

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
        self.assertIn(f"REVERSE_BIN_PORT={payload['reverse_proxy_to'].rsplit(':', 1)[1]}", payload["envs"])
        self.assertIn("CUSTOM=1", payload["envs"])

    def test_main_allocates_fallback_for_opaque_explicit_command_without_entrypoint(self) -> None:
        # Intent: verify an opaque explicit command with no detectable entrypoint still gets a TCP fallback listener.
        self.make_cmd_sh()
        (self.app_dir / ".env").write_text("REVERSE_BIN_COMMAND=./cmd.sh\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        env_map = self.envs_as_map(payload["envs"])
        self.assertEqual(payload["executable"], ["./cmd.sh"])
        self.assertRegex(payload["reverse_proxy_to"], r"^127\.0\.0\.1:\d+$")
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "127.0.0.1")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], payload["reverse_proxy_to"].rsplit(":", 1)[1])

    def test_main_emits_health_overrides_for_opaque_explicit_command_without_entrypoint(self) -> None:
        # Intent: verify opaque explicit commands still emit exact health override fields when fallback TCP is allocated.
        self.make_cmd_sh()
        (self.app_dir / ".env").write_text(
            "REVERSE_BIN_COMMAND=./cmd.sh\n"
            "REVERSE_BIN_HEALTH_METHOD=GET\n"
            "REVERSE_BIN_HEALTH_PATH=/.well-known/openid-configuration\n"
        )

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        env_map = self.envs_as_map(payload["envs"])
        self.assertEqual(payload["executable"], ["./cmd.sh"])
        self.assertRegex(payload["reverse_proxy_to"], r"^127\.0\.0\.1:\d+$")
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "127.0.0.1")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], payload["reverse_proxy_to"].rsplit(":", 1)[1])
        self.assertEqual(payload["health_method"], "GET")
        self.assertEqual(payload["health_path"], "/.well-known/openid-configuration")

    def test_main_emits_health_overrides_for_autodetected_app(self) -> None:
        # Intent: verify CLI emits health override fields from .env for autodetected apps, not only explicit command mode.
        self.make_main_py()
        (self.app_dir / ".env").write_text("LISTEN=8080\nREVERSE_BIN_HEALTH_METHOD=GET\nREVERSE_BIN_HEALTH_PATH=/health\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(payload["executable"], ["./main.py"])
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:8080")
        self.assertEqual(payload["health_method"], "GET")
        self.assertEqual(payload["health_path"], "/health")

    def test_main_rejects_partial_health_override(self) -> None:
        # Intent: verify CLI rejects half-defined health overrides before emitting detector JSON.
        self.make_main_py()
        (self.app_dir / ".env").write_text("LISTEN=8080\nREVERSE_BIN_HEALTH_METHOD=GET\n")

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 1)
        self.assertRegex(completed.stderr, r"REVERSE_BIN_HEALTH_METHOD and REVERSE_BIN_HEALTH_PATH")

    def test_load_env_app_config_reads_health_status(self) -> None:
        # Intent: verify REVERSE_BIN_HEALTH_STATUS is parsed as exact detector status override.
        config = discover_app.load_env_app_config(
            {
                "REVERSE_BIN_PORT": "8080",
                "REVERSE_BIN_HEALTH_METHOD": "get",
                "REVERSE_BIN_HEALTH_PATH": "/v2/",
                "REVERSE_BIN_HEALTH_STATUS": "401",
            }
        )

        self.assertEqual(config["health_method"], "GET")
        self.assertEqual(config["health_path"], "/v2/")
        self.assertEqual(config["health_status"], 401)

    def test_load_env_app_config_rejects_health_status_without_method_path(self) -> None:
        # Intent: verify exact health status never emits without a complete health probe definition.
        with self.assertRaisesRegex(ValueError, "REVERSE_BIN_HEALTH_STATUS requires"):
            discover_app.load_env_app_config({"REVERSE_BIN_HEALTH_STATUS": "401"})

    def test_load_env_app_config_rejects_health_status_outside_http_range(self) -> None:
        # Intent: verify exact health status is constrained to real HTTP status codes.
        with self.assertRaisesRegex(ValueError, "100 through 599"):
            discover_app.load_env_app_config(
                {
                    "REVERSE_BIN_HEALTH_METHOD": "GET",
                    "REVERSE_BIN_HEALTH_PATH": "/v2/",
                    "REVERSE_BIN_HEALTH_STATUS": "600",
                }
            )

    def test_resolve_app_allocates_reverse_bin_port_for_blank_port(self) -> None:
        # Intent: verify blank REVERSE_BIN_PORT allocates a TCP target and injects the resolved port into child envs.
        self.make_cmd_sh()

        resolved = discover_app.resolve_app(
            self.app_dir,
            dot_env={"REVERSE_BIN_COMMAND": "./cmd.sh", "REVERSE_BIN_HOST": "127.0.0.1", "REVERSE_BIN_PORT": ""},
        )
        env_map = self.envs_as_map(discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides))

        self.assertRegex(resolved.reverse_proxy_to, r"^127\.0\.0\.1:\d+$")
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "127.0.0.1")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], resolved.reverse_proxy_to.rsplit(":", 1)[1])

    def test_resolve_app_uses_fixed_reverse_bin_port(self) -> None:
        # Intent: verify fixed REVERSE_BIN_PORT forms reverse_proxy_to and is preserved in child envs.
        self.make_cmd_sh()

        resolved = discover_app.resolve_app(
            self.app_dir,
            dot_env={"REVERSE_BIN_COMMAND": "./cmd.sh", "REVERSE_BIN_PORT": "9999"},
        )
        env_map = self.envs_as_map(discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides))

        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:9999")
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "127.0.0.1")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], "9999")

    def test_resolve_app_uses_reverse_bin_host(self) -> None:
        # Intent: verify REVERSE_BIN_HOST participates in reverse_proxy_to and child envs for TCP apps.
        self.make_cmd_sh()

        resolved = discover_app.resolve_app(
            self.app_dir,
            dot_env={"REVERSE_BIN_COMMAND": "./cmd.sh", "REVERSE_BIN_HOST": "0.0.0.0", "REVERSE_BIN_PORT": "9999"},
        )
        env_map = self.envs_as_map(discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides))

        self.assertEqual(resolved.reverse_proxy_to, "0.0.0.0:9999")
        self.assertEqual(env_map["REVERSE_BIN_HOST"], "0.0.0.0")
        self.assertEqual(env_map["REVERSE_BIN_PORT"], "9999")

    def test_resolve_app_unix_socket_does_not_inject_reverse_bin_tcp_envs(self) -> None:
        # Intent: verify Unix socket apps do not receive TCP bind envs.
        self.make_main_py()

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"SOCKET_PATH": "data/app.sock"})
        env_map = self.envs_as_map(discover_app.build_app_envs(self.app_dir, {}, resolved.env_overrides))

        self.assertEqual(resolved.reverse_proxy_to, f"unix/{(self.app_dir / 'data/app.sock').resolve()}")
        self.assertNotIn("REVERSE_BIN_HOST", env_map)
        self.assertNotIn("REVERSE_BIN_PORT", env_map)

    def test_main_emits_reverse_bin_health_status(self) -> None:
        # Intent: verify CLI emits exact health status for auth-protected health endpoints.
        self.make_cmd_sh()
        (self.app_dir / ".env").write_text(
            "REVERSE_BIN_COMMAND=./cmd.sh\n"
            "REVERSE_BIN_PORT=9999\n"
            "REVERSE_BIN_HEALTH_METHOD=GET\n"
            "REVERSE_BIN_HEALTH_PATH=/v2/\n"
            "REVERSE_BIN_HEALTH_STATUS=401\n"
        )

        completed = self.run_cli()

        self.assertEqual(completed.returncode, 0, completed.stderr)
        payload = json.loads(completed.stdout)
        self.assertEqual(payload["reverse_proxy_to"], "127.0.0.1:9999")
        self.assertEqual(payload["health_method"], "GET")
        self.assertEqual(payload["health_path"], "/v2/")
        self.assertEqual(payload["health_status"], 401)

    def test_resolve_app_detects_root_index_html_after_runtime_entrypoints(self) -> None:
        # Intent: verify static root index.html is served by Caddy only after Python/Deno entrypoints are absent.
        (self.app_dir / "index.html").write_text("<h1>static</h1>\n")

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"REVERSE_BIN_PORT": "9999"})

        self.assertEqual(
            resolved.executable,
            ["reverse-bin-caddy", "file-server", "--listen", "127.0.0.1:9999", "--root", "."],
        )
        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:9999")

    def test_resolve_app_detects_dist_index_html_after_root_index_html(self) -> None:
        # Intent: verify dist/index.html is served by Caddy when no runtime entrypoint or root index.html exists.
        dist_dir = self.app_dir / "dist"
        dist_dir.mkdir()
        (dist_dir / "index.html").write_text("<h1>static dist</h1>\n")

        resolved = discover_app.resolve_app(self.app_dir, dot_env={"REVERSE_BIN_PORT": "9999"})

        self.assertEqual(
            resolved.executable,
            ["reverse-bin-caddy", "file-server", "--listen", "127.0.0.1:9999", "--root", "dist"],
        )
        self.assertEqual(resolved.reverse_proxy_to, "127.0.0.1:9999")

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
        self.assertIn(f"REVERSE_BIN_PORT={payload['reverse_proxy_to'].rsplit(':', 1)[1]}", payload["envs"])


if __name__ == "__main__":
    unittest.main()
