#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "python-dotenv",
# ]
# ///
import json
import sys
import socket
import os
from dotenv import dotenv_values
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
    include_PATH: bool = False,
) -> list[str]:
    """Wraps a command with landrun for sandboxing."""
    wrapper = ["landrun"]
    rox = rox or []
    envs = envs or []

    if include_std:
        # Standard system paths required for most binaries and scripts to run.
        # /bin, /usr, /lib, /lib64 are needed for the loader, shared libs, and core utils.
        # /etc is needed for system configuration like DNS (resolv.conf) and users.
        wrapper.extend(["--rox", "/bin,/usr,/lib,/lib64"])
        wrapper.extend(["--ro", "/etc"])
        wrapper.extend(["--rw", "/dev"])

    if include_PATH and "PATH" in os.environ:
        path_val = os.environ["PATH"]
        envs.append(f"PATH={path_val}")
        for p in path_val.split(os.pathsep):
            if p and os.path.isdir(p):
                rox.append(p)

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

def detect_dir_and_port(working_dir: Path, reverse_proxy_to: str) -> tuple[list[str], list[str]]:
    """Detects the application type and returns the command and envs."""
    envs = [f"REVERSE_PROXY_TO={reverse_proxy_to}"]

    if (working_dir / "main.ts").exists():
        port = reverse_proxy_to.split(':')[-1]
        return ["deno", "serve", "--allow-all", "--host", "127.0.0.1", "--port", port, "main.ts"], envs

    for script in ["main.py", "main.sh"]:
        path = working_dir / script
        if path.exists() and os.access(path, os.X_OK):
            return [f"./{script}"], envs

    raise FileNotFoundError(f"No supported entry point (main.ts, executable main.py, or executable main.sh) found in {working_dir}")

def main() -> None:
    working_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(".")
    if not working_dir.is_dir():
        print(f"Error: directory {working_dir} does not exist", file=sys.stderr)
        sys.exit(1)

    env_file = working_dir / ".env"
    dot_env_vars = {}
    try:
        dot_env_vars = dotenv_values(dotenv_path=env_file)
    except Exception:
        pass

    # Resolve address with precedence: .env > process env > free TCP port
    reverse_proxy_to = dot_env_vars.get("REVERSE_PROXY_TO") or os.environ.get("REVERSE_PROXY_TO")
    if reverse_proxy_to and reverse_proxy_to.startswith("unix/"):
        socket_path = reverse_proxy_to[5:]
        if os.path.isabs(socket_path):
            print(f"Error: Unix socket path in REVERSE_PROXY_TO must be relative: {socket_path}", file=sys.stderr)
            sys.exit(1)
    if not reverse_proxy_to:
        reverse_proxy_to = f"127.0.0.1:{find_free_port()}"

    executable, envs = detect_dir_and_port(working_dir, reverse_proxy_to)
    
    # Add variables from .env to the environment list
    for k, v in dot_env_vars.items():
        if v is not None:
            if k == "REVERSE_PROXY_TO":
                # Ensure the envs list uses the value from .env
                envs = [f"{k}={v}" if e.startswith("REVERSE_PROXY_TO=") else e for e in envs]
            else:
                envs.append(f"{k}={v}")

    # Wrap the executable with landrun for sandboxing
    data_dir = working_dir / "data"
    rw_paths = []
    if data_dir.is_dir():
        resolved_data = str(data_dir.resolve())
        rw_paths.append(resolved_data)
        envs.append(f"HOME={resolved_data}")

    bind_tcp = []
    if not reverse_proxy_to.startswith("unix/"):
        try:
            bind_tcp.append(int(reverse_proxy_to.split(':')[-1]))
        except (ValueError, IndexError):
            pass

    executable = wrap_landrun(
        executable,
        rox=[str(working_dir.resolve())],
        rw=rw_paths,
        bind_tcp=bind_tcp,
        unrestricted_network=True,
        envs=envs,
        include_std=True,
        include_PATH=True
    )

    # Ensure reverse_proxy_to is absolute if it's a unix socket
    final_reverse_proxy_to = reverse_proxy_to
    if reverse_proxy_to.startswith("unix/"):
        socket_path = reverse_proxy_to[5:]
        abs_socket_path = (working_dir / socket_path).resolve()
        final_reverse_proxy_to = f"unix/{abs_socket_path}"

    result: dict[str, Any] = {
        "executable": executable,
        "reverse_proxy_to": final_reverse_proxy_to,
        "working_directory": str(working_dir.resolve()),
    }

    print(json.dumps(result))

if __name__ == "__main__":
    main()
