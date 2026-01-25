#!/usr/bin/env python3
import os
import sys
import json

def discover():
    if len(sys.argv) < 2:
        print("Usage: discover-app.py <app_dir>")
        sys.exit(1)

    app_dir = sys.argv[1]
    main_py = os.path.join(app_dir, "main.py")

    if not os.path.isfile(main_py):
        print(f"Error: {main_py} not found")
        sys.exit(1)

    # Configuration for the reverse-bin module
    # We assume the app will listen on a port provided by the module or a default
    config = {
        "handler": "reverse-bin",
        "mode": "proxy",
        "executable": main_py,
        "reverse_proxy_to": ":8001",
        "readiness_check": {
            "method": "HEAD",
            "path": "/"
        }
    }

    output_file = os.path.join(app_dir, "reverse-bin-caddy.json")
    try:
        with open(output_file, "w") as f:
            json.dump(config, f, indent=4)
        print(f"Generated {output_file}")
    except Exception as e:
        print(f"Error writing config: {e}")
        sys.exit(1)

if __name__ == "__main__":
    discover()
