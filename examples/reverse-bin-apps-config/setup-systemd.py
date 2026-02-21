#!/usr/bin/env python3
from pathlib import Path
import subprocess
import sys


if len(sys.argv) != 2:
    print("usage: setup-systemd.py <username>", file=sys.stderr)
    raise SystemExit(1)

username = sys.argv[1]

root = str(Path(__file__).resolve().parent.parent)
service_name = "reverse-bin.service"
service_path = f"/etc/systemd/system/{service_name}"

unit = f"""[Unit]
Description=reverse-bin Caddy proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User={username}
Group={username}
WorkingDirectory={root}
Environment=OPS_EMAIL=ops@example.com
Environment=DOMAIN_SUFFIX=example.com
ExecStart={root}/.bin/run.sh
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
"""

subprocess.run(["sudo", "tee", service_path], input=unit, text=True, check=True, stdout=subprocess.DEVNULL)
subprocess.run(["sudo", "systemctl", "daemon-reload"], check=True)
subprocess.run(["sudo", "systemctl", "enable", "--now", service_name], check=True)
subprocess.run(["sudo", "systemctl", "status", "--no-pager", service_name], check=True)

print(f"set capability once if missing:\n  sudo setcap 'cap_net_bind_service=+ep' {root}/.bin/caddy")
