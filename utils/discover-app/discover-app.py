#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "python-dotenv",
# ]
# ///

"""
- reads app config from .env vars
- if any envs are missing, infers them
- returns caddy dynamic proxy config
"""

import argparse
import json
import os
import shlex
import socket
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import TypedDict

from dotenv import dotenv_values


class EnvAppConfig(TypedDict):
    command: list[str] | None  # e.g. ["uv", "run", "main.py"]
    listen: str | None         # e.g. "8080" or "127.0.0.1:8080"
    socket_path: str | None    # e.g. "app.sock"


class DiscoverAppResult(TypedDict):
    executable: list[str]      # e.g. ["landrun", "--env", "LISTEN=127.0.0.1:8080", "./main.py"]
    reverse_proxy_to: str      # e.g. "127.0.0.1:8080" or "unix/app.sock"
    working_directory: str     # e.g. "/var/www/app"
    envs: list[str]            # e.g. ["LISTEN=127.0.0.1:8080", "PATH=/usr/bin"]


@dataclass(frozen=True)
class DetectedApp:
    kind: str                  # e.g. "main.ts" or "main.py"
    supports_unix_socket: bool


@dataclass(frozen=True)
class ResolvedApp:
    executable: list[str]          # e.g. ["deno", "serve", "main.ts"]
    reverse_proxy_to: str          # e.g. "127.0.0.1:8080" or "unix/app.sock"
    env_overrides: dict[str, str]  # e.g. {"LISTEN": "127.0.0.1:8080"}


def resolve_unix_socket_path(working_dir: Path, socket_path: str) -> str:
    if Path(socket_path).is_absolute():
        raise ValueError(f"Unix socket path must be relative: {socket_path}")
    return f"unix/{(working_dir / socket_path).resolve()}"


def normalize_listen_value(listen_value: str) -> str:
    normalized = f"127.0.0.1:{listen_value}" if listen_value.isdigit() else listen_value

    try:
        int(normalized.rsplit(":", 1)[-1])
    except ValueError as error:
        raise ValueError(f"Invalid LISTEN port: {listen_value}") from error

    return normalized


def load_env_app_config(dot_env: dict[str, str]) -> EnvAppConfig:
    # e.g. LISTEN="8080" or LISTEN="127.0.0.1:8080"
    listen = dot_env.get("LISTEN")
    
    # e.g. SOCKET_PATH="app.sock"
    socket_path = dot_env.get("SOCKET_PATH")
    
    if listen is not None and socket_path is not None:
        raise ValueError("Cannot set both LISTEN and SOCKET_PATH")

    # e.g. REVERSE_BIN_COMMAND="uv run main.py"
    command_value = dot_env.get("REVERSE_BIN_COMMAND")
    command: list[str] | None = None
    if command_value is not None:
        command = shlex.split(command_value)
        if not command:
            raise ValueError("REVERSE_BIN_COMMAND must not be empty")

    return {
        "command": command,
        "listen": listen,
        "socket_path": socket_path,
    }


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


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


def detect_app(working_dir: Path) -> DetectedApp | None:
    # e.g. Deno app
    if (working_dir / "main.ts").exists():
        return DetectedApp(kind="main.ts", supports_unix_socket=False)

    # e.g. Python app
    path = working_dir / "main.py"
    if path.exists() and os.access(path, os.X_OK):
        return DetectedApp(kind="main.py", supports_unix_socket=True)

    return None


def build_detected_command(detection: DetectedApp, reverse_proxy_to: str) -> list[str]:
    if detection.kind == "main.ts":
        port = reverse_proxy_to.rsplit(":", 1)[-1]
        return ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", port, "main.ts"]

    if detection.kind == "main.py":
        return ["./main.py"]

    raise ValueError(f"Unsupported detected app kind: {detection.kind}")


def resolve_app(working_dir: Path, *, dot_env: dict[str, str]) -> ResolvedApp:
    config = load_env_app_config(dot_env)
    
    needs_detection = config["command"] is None or (config["listen"] is None and config["socket_path"] is None)
    detection = detect_app(working_dir) if needs_detection else None
    
    if needs_detection and detection is None:
        raise FileNotFoundError(f"No supported entry point (main.ts or executable main.py) found in {working_dir}")

    if config["socket_path"] is not None and detection is not None and not detection.supports_unix_socket:
        raise ValueError(f"{detection.kind} does not support SOCKET_PATH")

    if config["listen"] is not None:
        listen_value = config["listen"] or str(find_free_port())
        reverse_proxy_to = normalize_listen_value(listen_value)
        env_overrides = {"LISTEN": reverse_proxy_to} if config["listen"] == "" else {}
    elif config["socket_path"] is not None:
        reverse_proxy_to = resolve_unix_socket_path(working_dir, config["socket_path"])
        env_overrides = {}
    else:
        reverse_proxy_to = f"127.0.0.1:{find_free_port()}"
        env_overrides = {"LISTEN": reverse_proxy_to}

    if config["command"] is not None:
        executable = config["command"]
    else:
        assert detection is not None
        executable = build_detected_command(detection, reverse_proxy_to)

    return ResolvedApp(
        executable=executable,
        reverse_proxy_to=reverse_proxy_to,
        env_overrides=env_overrides,
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
        wrapper += ["--rox", "/bin,/usr,/lib,/lib64", "--ro", "/etc", "--rw", "/dev"]

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

    env_file = working_dir / ".env"
    dot_env = {k: v for k, v in dotenv_values(env_file).items() if v is not None}

    try:
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
        bind_tcp.append(int(reverse_proxy_to.rsplit(":", 1)[-1]))

    if not args.no_sandbox:
        executable = wrap_landrun(
            executable,
            rox=[str(working_dir.resolve())],
            rw=rw_paths,
            bind_tcp=bind_tcp,
            envs=envs,
        )

    result: DiscoverAppResult = {
        "executable": executable,
        "reverse_proxy_to": reverse_proxy_to,
        "working_directory": str(working_dir.resolve()),
        "envs": envs,
    }
    print(json.dumps(result))


if __name__ == "__main__":
    main()
