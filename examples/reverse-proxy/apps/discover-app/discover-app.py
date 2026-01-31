#!/usr/bin/env python3
import json
import sys
import os

working_dir = sys.argv[1] if len(sys.argv) > 1 else "."
os.makedirs(working_dir, exist_ok=True)

print(
    json.dumps(
        {
            "executable": ["python3", "-m", "http.server", "23232"],
            "reverse_proxy_to": ":23232",
            "working_directory": working_dir,
        }
    )
)
