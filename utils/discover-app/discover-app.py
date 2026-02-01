#!/usr/bin/env python3
import json
import sys
from pathlib import Path
from typing import Any

def wrap_landrun(
    cmd: list[str],
    rwx: list[str] | None = None,
    rw: list[str] | None = None,
    ro: list[str] | None = None,
    rox: list[str] | None = None,
    bind_tcp: list[int] | None = None,
    connect_tcp: list[int] | None = None,
) -> list[str]:
    """Wraps a command with landrun for sandboxing."""
    wrapper = ["landrun"]

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

def detect_dir(dir) #write a func that detects main.py and returns uv run to run it..and main.ts and uses deno serve..also write a function to find free port bind to AI! 
def main() -> None:
    working_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(".")
    if not working_dir.is_dir():
        print(f"Error: directory {working_dir} does not exist", file=sys.stderr)
        sys.exit(1)

    result: dict[str, Any] = {
        "executable": ["python3", "-m", "http.server", "23232"],
        "reverse_proxy_to": ":23232",
        "working_directory": str(working_dir.resolve()),
    }

    print(json.dumps(result))

if __name__ == "__main__":
    main()
