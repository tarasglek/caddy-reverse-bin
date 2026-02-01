#!/usr/bin/env python3
import json
import sys
import socket
import os
from pathlib import Path
from typing import Any

"""
- `--ro <path>`: Allow read-only access to specified path (can be specified multiple times or as comma-separated values)
- `--rox <path>`: Allow read-only access with execution to specified path (can be specified multiple times or as comma-separated values)
- `--rw <path>`: Allow read-write access to specified path (can be specified multiple times or as comma-separated values)
- `--rwx <path>`: Allow read-write access with execution to specified path (can be specified multiple times or as comma-separated values)
- `--bind-tcp <port>`: Allow binding to specified TCP port (can be specified multiple times or as comma-separated values)
- `--connect-tcp <port>`: Allow connecting to specified TCP port (can be specified multiple times or as comma-separated values)
- `--env <var>`: Environment variable to pass to the sandboxed command (format: KEY=VALUE or just KEY to pass current value)
- `--best-effort`: Use best effort mode, falling back to less restrictive sandbox if necessary [default: disabled]
- `--log-level <level>`: Set logging level (error, info, debug) [default: "error"]
- `--unrestricted-network`: Allows unrestricted network access (disables all network restrictions)
- `--unrestricted-filesystem`: Allows unrestricted filesystem access (disables all filesystem restrictions)
- `--add-exec`: Automatically adds the executing binary to --rox
- `--ldd`: Automatically adds required libraries to --rox
"""
def wrap_landrun(
    cmd: list[str],
    rwx: list[str] | None = None,
    rw: list[str] | None = None,
    ro: list[str] | None = None,
    rox: list[str] | None = None,
    bind_tcp: list[int] | None = None,
    connect_tcp: list[int] | None = None,
    unrestricted_network: bool = False,
    envs: list[str] | None = None,
    include_std: bool = False,
) -> list[str]:
    """Wraps a command with landrun for sandboxing."""
    wrapper = ["landrun"]

    if include_std:
        # Standard system paths required for most binaries and scripts to run.
        # /bin, /usr, /lib, /lib64 are needed for the loader, shared libs, and core utils.
        # /etc is needed for system configuration like DNS (resolv.conf) and users.
        wrapper.extend(["--rox", "/bin,/usr,/lib,/lib64"])
        wrapper.extend(["--ro", "/etc"])
        wrapper.extend(["--rw", "/dev"])

    if envs:
        for env in envs:
            wrapper.extend(["--env", env])
    if unrestricted_network:
        wrapper.append("--unrestricted-network")
    if rwx:
        wrapper.extend(["--rwx", ",".join(rwx)])
    if rw:
        wrapper.extend(["--rw", ",".join(rw)])
    if ro:
        wrapper.extend(["--ro", ",".join(ro)])
    if rox:
        wrapper.extend(["--rox", ",".join(rox)])
    if bind_tcp:
        wrapper.extend(["--bind-tcp", ",".join(map(str, bind_tcp))])
    if connect_tcp:
        wrapper.extend(["--connect-tcp", ",".join(map(str, connect_tcp))])

    return wrapper + cmd

def find_free_port() -> int:
    """Finds a free TCP port by binding to port 0."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]

def detect_dir_and_port(working_dir: Path) -> tuple[list[str], int, list[str]]:
    """Detects the application type and returns the command, port, and envs."""
    port = find_free_port()
    envs = [f"PORT={port}"]

    if (working_dir / "main.ts").exists():
        return ["deno", "serve", "--allow-all", "--host", "127.0.0.1", "--port", str(port), "main.ts"], port, envs

    for script in ["main.py", "main.sh"]:
        path = working_dir / script
        if path.exists() and os.access(path, os.X_OK):
            return [f"./{script}"], port, envs

    raise FileNotFoundError(f"No supported entry point (main.ts, executable main.py, or executable main.sh) found in {working_dir}")

def main() -> None:
    working_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(".")
    if not working_dir.is_dir():
        print(f"Error: directory {working_dir} does not exist", file=sys.stderr)
        sys.exit(1)

    executable, port, envs = detect_dir_and_port(working_dir)

    # Wrap the executable with landrun for sandboxing
    data_dir = working_dir / "data"
    rw_paths = [str(data_dir.resolve()) for p in [data_dir] if p.is_dir()]

    if "PATH" in os.environ:
        envs.append(f"PATH={os.environ['PATH']}")

    executable = wrap_landrun(
        executable,
        rox=[str(working_dir.resolve())],
        rw=rw_paths,
        bind_tcp=[port],
        unrestricted_network=True,
        envs=envs,
        include_std=True
        # add include_PATH which would add env and also split it and do allow-rox for every component AI!
    )

    result: dict[str, Any] = {
        "executable": executable,
        "reverse_proxy_to": f":{port}",
        "working_directory": str(working_dir.resolve()),
    }

    print(json.dumps(result))

if __name__ == "__main__":
    main()
