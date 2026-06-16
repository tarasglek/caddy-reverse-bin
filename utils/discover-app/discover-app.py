#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "python-dotenv",
# ]
# ///

"""
Discover reverse-bin app launch config from an app directory.

Reads these special keys from `.env`:
- `REVERSE_BIN_COMMAND`: explicit command to launch for the app
- `REVERSE_BIN_HOST`: app-facing TCP bind host, defaults to `127.0.0.1`
- `REVERSE_BIN_PORT`: app-facing TCP bind port; blank allocates a free port
- `SOCKET_PATH`: app-facing relative unix socket path, e.g. `data/app.sock`
- `REVERSE_BIN_HEALTH_METHOD`: optional reverse-bin health probe method override, e.g. `GET`
- `REVERSE_BIN_HEALTH_PATH`: optional reverse-bin health probe path override, e.g. `/health`
- `REVERSE_BIN_HEALTH_STATUS`: optional exact reverse-bin health probe status, e.g. `401`
Passes all `.env` keys through to child process, and may also set:
- `REVERSE_BIN_HOST` and `REVERSE_BIN_PORT`: when TCP listener values must be resolved automatically
- `PATH`: copied from parent process when not already set in `.env`
- `HOME`: set to `<working_dir>/data` when that directory exists

`REVERSE_BIN_HEALTH_METHOD` and `REVERSE_BIN_HEALTH_PATH` affect detector JSON overrides for reverse-bin.
They are not passed to child process unless already present in `.env`.

Returns Caddy dynamic proxy config JSON.
"""

import argparse
import json
import os
import socket
import subprocess
import sys
from dataclasses import dataclass
from io import StringIO
from pathlib import Path
from typing import Callable, TypedDict

from dotenv import dotenv_values


class EnvAppConfig(TypedDict):
    command: list[str] | None       # e.g. ["uv", "run", "main.py"]
    listen: str | None              # legacy, e.g. "8080" or "127.0.0.1:8080"
    reverse_bin_host: str | None    # e.g. "127.0.0.1"
    reverse_bin_port: str | None    # e.g. "8080" or "" for auto-allocation
    socket_path: str | None         # e.g. "app.sock"
    health_method: str | None
    health_path: str | None
    health_status: int | None


class DiscoverAppResult(TypedDict, total=False):
    executable: list[str]      # e.g. ["landrun", "--env", "LISTEN=127.0.0.1:8080", "./main.py"]
    reverse_proxy_to: str      # e.g. "127.0.0.1:8080" or "unix/app.sock"
    working_directory: str     # e.g. "/var/www/app"
    envs: list[str]            # e.g. ["LISTEN=127.0.0.1:8080", "PATH=/usr/bin"]
    health_method: str
    health_path: str
    health_status: int


@dataclass(frozen=True)
class DetectedApp:
    kind: str                  # e.g. "main.ts", "main.py", "index.html", or "dist/index.html"
    supports_unix_socket: bool


@dataclass(frozen=True)
class EnvSource:
    path: Path
    encrypted: bool


@dataclass(frozen=True)
class ResolvedApp:
    executable: list[str]          # e.g. ["deno", "serve", "main.ts"]
    reverse_proxy_to: str          # e.g. "127.0.0.1:8080" or "unix/app.sock"
    env_overrides: dict[str, str]  # e.g. {"REVERSE_BIN_HOST": "127.0.0.1", "REVERSE_BIN_PORT": "8080"}
    health_method: str | None
    health_path: str | None
    health_status: int | None


@dataclass(frozen=True)
class CommandResolution:
    explicit_command: list[str] | None
    detection: DetectedApp | None


def find_env_source(working_dir: Path) -> EnvSource | None:
    plaintext = working_dir / ".env"
    encrypted = working_dir / "secrets.enc.json"
    if plaintext.exists() and encrypted.exists():
        raise ValueError("Cannot use both .env and encrypted env file secrets.enc.json")
    if encrypted.exists():
        return EnvSource(path=encrypted, encrypted=True)
    if plaintext.exists():
        return EnvSource(path=plaintext, encrypted=False)
    return None


def decrypt_sops_json_to_dotenv(
    path: Path,
    runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> str:
    args = ["sops", "--decrypt", "--input-type", "json", "--output-type", "dotenv", str(path)]
    completed = runner(args, capture_output=True, text=True)
    if completed.returncode != 0:
        raise ValueError(f"failed to decrypt {path}: {completed.stderr.strip()}")
    return completed.stdout


def load_app_env(
    working_dir: Path,
    runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> dict[str, str]:
    source = find_env_source(working_dir)
    if source is None:
        return {}
    if source.encrypted:
        values = dotenv_values(stream=StringIO(decrypt_sops_json_to_dotenv(source.path, runner=runner)))
    else:
        values = dotenv_values(source.path)
    return {k: v for k, v in values.items() if v is not None}


def resolve_unix_socket_path(working_dir: Path, socket_path: str) -> str:
    if Path(socket_path).is_absolute():
        raise ValueError(f"Unix socket path must be relative: {socket_path}")
    return f"unix/{(working_dir / socket_path).resolve()}"


def extract_port(address: str) -> str:
    port_str = address.rsplit(":", 1)[-1]
    try:
        int(port_str)
    except ValueError as error:
        raise ValueError(f"Invalid port in address: {address}") from error
    return port_str


def normalize_listen_value(listen_value: str) -> str:
    normalized = f"127.0.0.1:{listen_value}" if listen_value.isdigit() else listen_value

    try:
        extract_port(normalized)
    except ValueError as error:
        raise ValueError(f"Invalid LISTEN port: {listen_value}") from error

    return normalized


def load_env_app_config(dot_env: dict[str, str]) -> EnvAppConfig:
    # Legacy support for existing examples; new explicit app configs should use REVERSE_BIN_HOST/PORT.
    listen = dot_env.get("LISTEN")
    reverse_bin_host = dot_env.get("REVERSE_BIN_HOST")
    reverse_bin_port = dot_env.get("REVERSE_BIN_PORT")

    # e.g. SOCKET_PATH="app.sock"
    socket_path = dot_env.get("SOCKET_PATH")

    has_tcp_config = listen is not None or reverse_bin_host is not None or reverse_bin_port is not None
    if has_tcp_config and socket_path is not None:
        raise ValueError("Cannot set both TCP listener config and SOCKET_PATH")
    if listen is not None and (reverse_bin_host is not None or reverse_bin_port is not None):
        raise ValueError("Cannot mix LISTEN with REVERSE_BIN_HOST or REVERSE_BIN_PORT")

    if reverse_bin_host is not None:
        reverse_bin_host = reverse_bin_host.strip()
        if not reverse_bin_host:
            raise ValueError("REVERSE_BIN_HOST must not be empty")
    if reverse_bin_port is not None:
        reverse_bin_port = reverse_bin_port.strip()
        if reverse_bin_port:
            try:
                int(reverse_bin_port)
            except ValueError as error:
                raise ValueError(f"Invalid REVERSE_BIN_PORT: {reverse_bin_port}") from error

    health_method = dot_env.get("REVERSE_BIN_HEALTH_METHOD")
    health_path = dot_env.get("REVERSE_BIN_HEALTH_PATH")
    health_status_value = dot_env.get("REVERSE_BIN_HEALTH_STATUS")
    if (health_method is None) != (health_path is None):
        raise ValueError("REVERSE_BIN_HEALTH_METHOD and REVERSE_BIN_HEALTH_PATH must be set together")
    if health_status_value is not None and (health_method is None or health_path is None):
        raise ValueError("REVERSE_BIN_HEALTH_STATUS requires REVERSE_BIN_HEALTH_METHOD and REVERSE_BIN_HEALTH_PATH")
    if health_method is not None:
        health_method = health_method.strip().upper()
        if not health_method:
            raise ValueError("REVERSE_BIN_HEALTH_METHOD must not be empty")
    if health_path is not None:
        health_path = health_path.strip()
        if not health_path:
            raise ValueError("REVERSE_BIN_HEALTH_PATH must not be empty")
    health_status: int | None = None
    if health_status_value is not None:
        try:
            health_status = int(health_status_value.strip())
        except ValueError as error:
            raise ValueError("REVERSE_BIN_HEALTH_STATUS must be an integer from 100 through 599") from error
        if health_status < 100 or health_status > 599:
            raise ValueError("REVERSE_BIN_HEALTH_STATUS must be an integer from 100 through 599")

    # e.g. REVERSE_BIN_COMMAND="uv run main.py"
    command_value = dot_env.get("REVERSE_BIN_COMMAND")
    command: list[str] | None = None
    if command_value is not None:
        command_value = command_value.strip()
        if not command_value:
            raise ValueError("REVERSE_BIN_COMMAND must not be empty")
        if " " in command_value:
            command = ["sh", "-c", command_value]
        else:
            command = [command_value]

    return {
        "command": command,
        "listen": listen,
        "reverse_bin_host": reverse_bin_host,
        "reverse_bin_port": reverse_bin_port,
        "socket_path": socket_path,
        "health_method": health_method,
        "health_path": health_path,
        "health_status": health_status,
    }


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def build_discovery_result(
    executable: list[str],
    reverse_proxy_to: str,
    working_directory: str,
    envs: list[str],
    health_method: str | None = None,
    health_path: str | None = None,
    health_status: int | None = None,
) -> DiscoverAppResult:
    result: DiscoverAppResult = {
        "executable": executable,
        "reverse_proxy_to": reverse_proxy_to,
        "working_directory": working_directory,
        "envs": envs,
    }
    if health_method is not None:
        result["health_method"] = health_method
    if health_path is not None:
        result["health_path"] = health_path
    if health_status is not None:
        result["health_status"] = health_status
    return result


def resolve_transport(
    working_dir: Path, config: EnvAppConfig, allow_fallback: bool = True
) -> tuple[str, dict[str, str]]:
    if config.get("reverse_bin_port") is not None or config.get("reverse_bin_host") is not None:
        host = config.get("reverse_bin_host") or "127.0.0.1"
        port = config.get("reverse_bin_port") or str(find_free_port())
        reverse_proxy_to = f"{host}:{port}"
        env_overrides = {"REVERSE_BIN_HOST": host, "REVERSE_BIN_PORT": port}
    elif config["listen"] is not None:
        listen_value = config["listen"] or str(find_free_port())
        reverse_proxy_to = normalize_listen_value(listen_value)
        env_overrides = {"LISTEN": reverse_proxy_to} if config["listen"] == "" else {}
    elif config["socket_path"] is not None:
        reverse_proxy_to = resolve_unix_socket_path(working_dir, config["socket_path"])
        env_overrides = {}
    elif allow_fallback:
        host = "127.0.0.1"
        port = str(find_free_port())
        reverse_proxy_to = f"{host}:{port}"
        env_overrides = {"REVERSE_BIN_HOST": host, "REVERSE_BIN_PORT": port}
    else:
        raise ValueError("Explicit app configuration requires either REVERSE_BIN_PORT, LISTEN, or SOCKET_PATH")

    return reverse_proxy_to, env_overrides


def build_explicit_app(
    working_dir: Path,
    dot_env: dict[str, str],
    config: EnvAppConfig,
) -> tuple[list[str], str, list[str]]:
    reverse_proxy_to, env_overrides = resolve_transport(working_dir, config, allow_fallback=False)
    envs = build_app_envs(working_dir, dot_env, env_overrides)
    return config["command"], reverse_proxy_to, envs


def discover_app_command(
    working_dir: Path,
    dot_env: dict[str, str],
    fallback_reverse_proxy_to: str,
) -> tuple[list[str], str]:
    detection = detect_app(working_dir)
    assert detection is not None
    return build_detected_command(detection, fallback_reverse_proxy_to), fallback_reverse_proxy_to


def detect_entrypoint(working_dir: Path, fallback_reverse_proxy_to: str) -> list[str]:
    command, _ = discover_app_command(working_dir, {}, fallback_reverse_proxy_to)
    return command


def build_app_envs(
    working_dir: Path,
    dot_env: dict[str, str],
    overrides: dict[str, str] | None = None,
) -> list[str]:
    env_map = dict(dot_env)
    if overrides:
        env_map.update(overrides)

    if "PATH" not in env_map and (path := os.environ.get("PATH")):
        env_map["PATH"] = path

    if (data_dir := working_dir / "data").is_dir():
        env_map["HOME"] = str(data_dir.resolve())

    return [f"{key}={value}" for key, value in env_map.items()]


def detect_app(working_dir: Path) -> DetectedApp:
    # e.g. Deno app
    if (working_dir / "main.ts").exists():
        return DetectedApp(kind="main.ts", supports_unix_socket=False)

    # e.g. Python app
    path = working_dir / "main.py"
    if path.exists() and os.access(path, os.X_OK):
        return DetectedApp(kind="main.py", supports_unix_socket=True)

    # e.g. static HTML app
    if (working_dir / "index.html").is_file():
        return DetectedApp(kind="index.html", supports_unix_socket=False)
    if (working_dir / "dist/index.html").is_file():
        return DetectedApp(kind="dist/index.html", supports_unix_socket=False)

    raise FileNotFoundError(
        f"No supported entry point (main.ts, executable main.py, index.html, or dist/index.html) found in {working_dir}"
    )


def resolve_command(working_dir: Path, config: EnvAppConfig) -> CommandResolution:
    if config["command"] is not None:
        return CommandResolution(explicit_command=config["command"], detection=None)

    return CommandResolution(explicit_command=None, detection=detect_app(working_dir))


def validate_transport_compatibility(working_dir: Path, config: EnvAppConfig) -> None:
    if config["socket_path"] is None:
        return

    try:
        detection = detect_app(working_dir)
    except FileNotFoundError:
        return

    if not detection.supports_unix_socket:
        raise ValueError(f"{detection.kind} does not support SOCKET_PATH")


def build_detected_command(detection: DetectedApp, reverse_proxy_to: str) -> list[str]:
    if detection.kind == "main.ts":
        port = extract_port(reverse_proxy_to)
        return ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", port, "main.ts"]

    if detection.kind == "main.py":
        return ["./main.py"]

    if detection.kind in {"index.html", "dist/index.html"}:
        port = extract_port(reverse_proxy_to)
        root = "." if detection.kind == "index.html" else "dist"
        return ["reverse-bin-caddy", "file-server", "--listen", f"127.0.0.1:{port}", "--root", root]

    raise ValueError(f"Unsupported detected app kind: {detection.kind}")


def is_deno_command(executable: list[str]) -> bool:
    return bool(executable) and Path(executable[0]).name == "deno"


def resolve_app(working_dir: Path, *, dot_env: dict[str, str]) -> ResolvedApp:
    config = load_env_app_config(dot_env)
    command = resolve_command(working_dir, config)
    reverse_proxy_to, env_overrides = resolve_transport(working_dir, config)
    validate_transport_compatibility(working_dir, config)

    if command.explicit_command is not None:
        executable = command.explicit_command
    else:
        assert command.detection is not None
        executable = build_detected_command(command.detection, reverse_proxy_to)

    if is_deno_command(executable) and "DENO_NO_UPDATE_CHECK" not in dot_env:
        env_overrides = {**env_overrides, "DENO_NO_UPDATE_CHECK": "1"}

    return ResolvedApp(
        executable=executable,
        reverse_proxy_to=reverse_proxy_to,
        env_overrides=env_overrides,
        health_method=config["health_method"],
        health_path=config["health_path"],
        health_status=config["health_status"],
    )


def wrap_landrun(
    cmd: list[str],
    *,
    rox: list[str] | None = None,
    rw: list[str] | None = None,
    bind_tcp: list[int] | None = None,
    envs: list[str] | None = None,
    unrestricted_network: bool = True,
    include_std: bool = True,
    include_path: bool = True,
) -> list[str]:
    rox = list(rox or [])
    rw = list(rw or [])
    bind_tcp = list(bind_tcp or [])
    envs = list(envs or [])

    wrapper = ["landrun"]

    if include_std:
        wrapper += ["--rox", "/bin,/usr,/lib,/lib64,/proc,/sys/fs/cgroup", "--ro", "/etc", "--rw", "/dev"]

    if include_path and (path := os.environ.get("PATH")):
        envs.append(f"PATH={path}")
        rox += [p for p in path.split(os.pathsep) if p and os.path.isdir(p)]

    for env in envs:
        wrapper += ["--env", env]

    if unrestricted_network:
        wrapper.append("--unrestricted-network")
    if rw:
        wrapper += ["--rw", ",".join(rw)]
    if rox:
        wrapper += ["--rox", ",".join(rox)]
    if bind_tcp:
        wrapper += ["--bind-tcp", ",".join(map(str, bind_tcp))]

    return wrapper + cmd


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Detect app entrypoint and emit reverse-bin dynamic detector JSON."
    )
    parser.add_argument("working_dir", nargs="?", default=".", help="App directory to inspect (default: current directory)")
    parser.add_argument("--no-sandbox", action="store_true", help="Return raw executable command without landrun wrapping")
    args = parser.parse_args()

    working_dir = Path(args.working_dir)
    if not working_dir.is_dir():
        print(f"Error: directory {working_dir} does not exist", file=sys.stderr)
        raise SystemExit(1)

    try:
        dot_env = load_app_env(working_dir)
        resolved = resolve_app(working_dir, dot_env=dot_env)
        envs = build_app_envs(working_dir, dot_env, resolved.env_overrides)
        executable = resolved.executable
        reverse_proxy_to = resolved.reverse_proxy_to
    except (FileNotFoundError, ValueError) as error:
        print(f"Error: {error}", file=sys.stderr)
        raise SystemExit(1) from error

    rw_paths: list[str] = []
    if (data_dir := working_dir / "data").is_dir():
        rw_paths.append(str(data_dir.resolve()))

    bind_tcp: list[int] = []
    if not reverse_proxy_to.startswith("unix/"):
        bind_tcp.append(int(extract_port(reverse_proxy_to)))

    if not args.no_sandbox:
        executable = wrap_landrun(
            executable,
            rox=[str(working_dir.resolve())],
            rw=rw_paths,
            bind_tcp=bind_tcp,
            envs=envs,
        )

    result = build_discovery_result(
        executable=executable,
        reverse_proxy_to=reverse_proxy_to,
        working_directory=str(working_dir.resolve()),
        envs=envs,
        health_method=resolved.health_method,
        health_path=resolved.health_path,
        health_status=resolved.health_status,
    )
    print(json.dumps(result))


if __name__ == "__main__":
    main()
