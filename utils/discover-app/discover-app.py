#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "python-dotenv",
# ]
# ///

import argparse
import json
import os
import shlex
import socket
import sys
from pathlib import Path
from typing import TypedDict

from dotenv import dotenv_values


class EnvAppConfig(TypedDict):
    # argv-style command parsed from REVERSE_BIN_COMMAND.
    command: list[str]

    # Raw LISTEN value from .env. An empty string means allocate a port.
    listen: str | None

    # Raw SOCKET_PATH value from .env.
    socket_path: str | None


class DiscoverAppResult(TypedDict):
    # argv-style command used to launch the app.
    executable: list[str]

    # Upstream address that Caddy should proxy to after the app starts.
    reverse_proxy_to: str

    # Absolute path to the app directory that was inspected.
    working_directory: str

    # Environment variables passed to the launched process, encoded as KEY=value strings.
    envs: list[str]


def build_discovery_result(
    *,
    executable: list[str],
    reverse_proxy_to: str,
    working_directory: str,
    envs: list[str],
) -> DiscoverAppResult:
    return {
        "executable": executable,
        "reverse_proxy_to": reverse_proxy_to,
        "working_directory": working_directory,
        "envs": envs,
    }


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


def load_env_app_config(dot_env: dict[str, str]) -> EnvAppConfig | None:
    command = dot_env.get("REVERSE_BIN_COMMAND")
    if command is None:
        return None

    listen = dot_env.get("LISTEN")
    socket_path = dot_env.get("SOCKET_PATH")
    if (listen is None) == (socket_path is None):
        raise ValueError("Explicit config requires exactly one of LISTEN or SOCKET_PATH")

    argv = shlex.split(command)
    if not argv:
        raise ValueError("REVERSE_BIN_COMMAND must not be empty")

    return {
        "command": argv,
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


def build_explicit_app(
    working_dir: Path,
    *,
    dot_env: dict[str, str],
    config: EnvAppConfig,
) -> tuple[list[str], str, list[str]]:
    if config["listen"] is not None:
        listen_value = config["listen"] or str(find_free_port())
        reverse_proxy_to = normalize_listen_value(listen_value)
        overrides = {"LISTEN": reverse_proxy_to} if config["listen"] == "" else None
        return config["command"], reverse_proxy_to, build_app_envs(working_dir, dot_env, overrides)

    socket_path = config["socket_path"]
    assert socket_path is not None
    reverse_proxy_to = resolve_unix_socket_path(working_dir, socket_path)
    return config["command"], reverse_proxy_to, build_app_envs(working_dir, dot_env)


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


def detect_entrypoint(working_dir: Path, reverse_proxy_to: str) -> list[str]:
    if (working_dir / "main.ts").exists():
        port = reverse_proxy_to.rsplit(":", 1)[-1]
        return ["deno", "serve", "--watch", "--allow-all", "--host", "127.0.0.1", "--port", port, "main.ts"]

    path = working_dir / "main.py"
    if path.exists() and os.access(path, os.X_OK):
        return ["./main.py"]

    raise FileNotFoundError(f"No supported entry point (main.ts or executable main.py) found in {working_dir}")


def resolve_fallback_reverse_proxy_to(working_dir: Path, dot_env: dict[str, str]) -> str:
    reverse_proxy_to = dot_env.get("REVERSE_PROXY_TO") or os.environ.get("REVERSE_PROXY_TO")
    if not reverse_proxy_to:
        return f"127.0.0.1:{find_free_port()}"
    if reverse_proxy_to.startswith("unix/"):
        return resolve_unix_socket_path(working_dir, reverse_proxy_to.removeprefix("unix/"))
    return reverse_proxy_to


def discover_app_command(
    working_dir: Path,
    *,
    dot_env: dict[str, str],
    fallback_reverse_proxy_to: str,
) -> tuple[list[str], str]:
    config = load_env_app_config(dot_env)
    if config is not None:
        executable, reverse_proxy_to, _ = build_explicit_app(working_dir, dot_env=dot_env, config=config)
        return executable, reverse_proxy_to

    return detect_entrypoint(working_dir, fallback_reverse_proxy_to), fallback_reverse_proxy_to


def build_fallback_app(
    working_dir: Path,
    *,
    dot_env: dict[str, str],
    reverse_proxy_to: str,
) -> tuple[list[str], str, list[str]]:
    executable = detect_entrypoint(working_dir, reverse_proxy_to)

    overrides: dict[str, str] | None = None
    if executable == ["./main.py"]:
        if reverse_proxy_to.startswith("unix/"):
            overrides = {"SOCKET_PATH": reverse_proxy_to.removeprefix("unix/")}
        else:
            overrides = {"LISTEN": reverse_proxy_to}

    return executable, reverse_proxy_to, build_app_envs(working_dir, dot_env, overrides)


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
        config = load_env_app_config(dot_env)
        if config is not None:
            executable, reverse_proxy_to, envs = build_explicit_app(working_dir, dot_env=dot_env, config=config)
        else:
            reverse_proxy_to = resolve_fallback_reverse_proxy_to(working_dir, dot_env)
            executable, reverse_proxy_to, envs = build_fallback_app(
                working_dir,
                dot_env=dot_env,
                reverse_proxy_to=reverse_proxy_to,
            )
    except ValueError as error:
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

    result = build_discovery_result(
        executable=executable,
        reverse_proxy_to=reverse_proxy_to,
        working_directory=str(working_dir.resolve()),
        envs=envs,
    )
    print(json.dumps(result))


if __name__ == "__main__":
    main()
