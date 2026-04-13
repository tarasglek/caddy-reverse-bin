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


class DiscoverAppResult(TypedDict):
    # argv-style command used to launch the app.
    # Potential values include a config-defined command like ["python3", "server.py"],
    # an auto-detected entrypoint like ["./main.py"], or a sandboxed landrun-wrapped
    # command like ["landrun", "--env", "REVERSE_PROXY_TO=127.0.0.1:8080", ..., "./main.py"].
    executable: list[str]

    # Upstream address that Caddy should proxy to after the app starts.
    # Potential values include a TCP address like "127.0.0.1:8080" or a unix socket
    # address like "unix//abs/path/to/app.sock".
    reverse_proxy_to: str

    # Absolute path to the app directory that was inspected.
    # Sample value: "/home/taras/Documents/caddy-reverse-bin/examples/reverse-proxy/apps/python3-unix-echo".
    working_directory: str

    # Environment variables passed through to the launched process, encoded as KEY=value strings.
    # Potential values include ["REVERSE_PROXY_TO=127.0.0.1:8080", "PATH=/usr/bin:/bin"]
    # and may also include app-provided values from .env plus "HOME=/abs/path/to/data".
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


def load_explicit_app_config(working_dir: Path, dot_env: dict[str, str]) -> tuple[list[str], str] | None:
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

    if listen is not None:
        return argv, normalize_listen_value(listen)

    assert socket_path is not None
    return argv, resolve_unix_socket_path(working_dir, socket_path)


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


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
    rox = rox or []
    rw = rw or []
    bind_tcp = bind_tcp or []
    envs = envs or []

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



def build_child_envs(dot_env: dict[str, str], reverse_proxy_to: str, working_dir: Path) -> list[str]:
    envs = [
        f"{k}={v}"
        for k, v in dot_env.items()
        if k not in {"REVERSE_BIN_COMMAND", "REVERSE_PROXY_TO", "PORT", "LISTEN", "SOCKET_PATH"}
    ]

    if reverse_proxy_to.startswith("unix/"):
        envs.insert(0, f"SOCKET_PATH={reverse_proxy_to.removeprefix('unix/')}")
    else:
        envs.insert(0, f"LISTEN={reverse_proxy_to}")

    if path := os.environ.get("PATH"):
        envs.append(f"PATH={path}")

    if (data_dir := working_dir / "data").is_dir():
        envs.append(f"HOME={data_dir.resolve()}")

    return envs



def discover_app_command(
    working_dir: Path,
    *,
    dot_env: dict[str, str],
    fallback_reverse_proxy_to: str,
) -> tuple[list[str], str]:
    explicit_config = load_explicit_app_config(working_dir, dot_env)
    if explicit_config is not None:
        return explicit_config

    return detect_entrypoint(working_dir, fallback_reverse_proxy_to), fallback_reverse_proxy_to


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
        fallback_reverse_proxy_to = resolve_fallback_reverse_proxy_to(working_dir, dot_env)
        executable, reverse_proxy_to = discover_app_command(
            working_dir,
            dot_env=dot_env,
            fallback_reverse_proxy_to=fallback_reverse_proxy_to,
        )
    except ValueError as error:
        print(f"Error: {error}", file=sys.stderr)
        raise SystemExit(1) from error

    envs = build_child_envs(dot_env, reverse_proxy_to, working_dir)

    rw_paths: list[str] = []
    if (data_dir := working_dir / "data").is_dir():
        rw_paths.append(str(data_dir.resolve()))

    bind_tcp: list[int] = []
    if not reverse_proxy_to.startswith("unix/"):
        try:
            bind_tcp.append(int(reverse_proxy_to.rsplit(":", 1)[-1]))
        except ValueError:
            pass

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
