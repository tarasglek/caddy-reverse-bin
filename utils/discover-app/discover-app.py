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
import socket
import sys
from pathlib import Path

from dotenv import dotenv_values


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
        return ["deno", "serve", "--allow-all", "--host", "127.0.0.1", "--port", port, "main.ts"]

    for script in ("main.py", "main.sh"):
        path = working_dir / script
        if path.exists() and os.access(path, os.X_OK):
            return [f"./{script}"]

    raise FileNotFoundError(
        f"No supported entry point (main.ts, executable main.py, or executable main.sh) found in {working_dir}"
    )


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

    reverse_proxy_to = dot_env.get("REVERSE_PROXY_TO") or os.environ.get("REVERSE_PROXY_TO")
    if not reverse_proxy_to:
        reverse_proxy_to = f"127.0.0.1:{find_free_port()}"

    if reverse_proxy_to.startswith("unix/"):
        socket_rel = reverse_proxy_to.removeprefix("unix/")
        if Path(socket_rel).is_absolute():
            print(f"Error: Unix socket path in REVERSE_PROXY_TO must be relative: {socket_rel}", file=sys.stderr)
            raise SystemExit(1)

    envs = [f"REVERSE_PROXY_TO={reverse_proxy_to}"]
    envs += [f"{k}={v}" for k, v in dot_env.items() if k != "REVERSE_PROXY_TO"]
    if path := os.environ.get("PATH"):
        envs.append(f"PATH={path}")

    rw_paths: list[str] = []
    if (data_dir := working_dir / "data").is_dir():
        resolved = str(data_dir.resolve())
        rw_paths.append(resolved)
        envs.append(f"HOME={resolved}")

    bind_tcp: list[int] = []
    if not reverse_proxy_to.startswith("unix/"):
        try:
            bind_tcp.append(int(reverse_proxy_to.rsplit(":", 1)[-1]))
        except ValueError:
            pass

    executable = detect_entrypoint(working_dir, reverse_proxy_to)
    if not args.no_sandbox:
        executable = wrap_landrun(
            executable,
            rox=[str(working_dir.resolve())],
            rw=rw_paths,
            bind_tcp=bind_tcp,
            envs=envs,
        )

    final_reverse_proxy_to = reverse_proxy_to
    if reverse_proxy_to.startswith("unix/"):
        socket_rel = reverse_proxy_to.removeprefix("unix/")
        final_reverse_proxy_to = f"unix/{(working_dir / socket_rel).resolve()}"

    print(
        json.dumps(
            {
                "executable": executable,
                "reverse_proxy_to": final_reverse_proxy_to,
                "working_directory": str(working_dir.resolve()),
                "envs": envs,
            }
        )
    )


if __name__ == "__main__":
    main()
