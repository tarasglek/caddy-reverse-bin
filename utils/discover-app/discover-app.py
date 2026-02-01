#!/usr/bin/env python3
import json
import sys
import os
from typing import Dict, Any

def main() -> None:
    working_dir: str = sys.argv[1] if len(sys.argv) > 1 else "."
    if not os.path.isdir(working_dir):
        print(f"Error: directory {working_dir} does not exist", file=sys.stderr)
        sys.exit(1)

    result: Dict[str, Any] = {
        "executable": ["python3", "-m", "http.server", "23232"],
        "reverse_proxy_to": ":23232",
        "working_directory": working_dir,
    }

    print(json.dumps(result))

if __name__ == "__main__":
    main()
