#!/usr/bin/env python3
import os
import pwd
import shlex
from pathlib import Path
import subprocess
import sys


def run(cmd: list[str]) -> None:
    print(f"+ {shlex.join(cmd)}", file=sys.stderr)
    subprocess.run(cmd, check=True)


if len(sys.argv) != 4:
    print("usage: setup-systemd.py <username> <ops_email> <domain_suffix>", file=sys.stderr)
    raise SystemExit(1)

username = sys.argv[1]
ops_email = sys.argv[2]
domain_suffix = sys.argv[3]

try:
    pwd.getpwnam(username)
except KeyError:
    print(f"error: user not found: {username}", file=sys.stderr)
    raise SystemExit(1)

if os.geteuid() != 0:
    print("error: run as root", file=sys.stderr)
    raise SystemExit(1)

root = Path(__file__).resolve().parent.parent
service_name = "reverse-bin.service"
service_path = Path("/etc/systemd/system") / service_name
caddy_path = root / ".bin" / "caddy"
uv_path = root / ".bin" / "uv"
discover_path = root / ".bin" / "discover-app.py"

if not caddy_path.exists():
    print(f"error: caddy binary not found: {caddy_path}", file=sys.stderr)
    raise SystemExit(1)
if not uv_path.exists():
    print(f"error: uv not found: {uv_path}", file=sys.stderr)
    raise SystemExit(1)
if not discover_path.exists():
    print(f"error: discover-app not found: {discover_path}", file=sys.stderr)
    raise SystemExit(1)

unit = f"""[Unit]
Description=reverse-bin Caddy proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User={username}
Group={username}
WorkingDirectory={root}
Environment=OPS_EMAIL={ops_email}
Environment=DOMAIN_SUFFIX={domain_suffix}
ExecStart={root}/.bin/run.sh
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
"""

print(f"writing {service_path}")
service_path.write_text(unit)

run(["setcap", "cap_net_bind_service=+ep", str(caddy_path)])
run(["getcap", str(caddy_path)])
# Prime uv cache as target user so first detector execution is fast.
run(["sudo", "-H", "-u", username, "env", f"PATH={root}/.bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", str(discover_path), "--help"])
run(["systemctl", "daemon-reload"])
run(["systemctl", "enable", "--now", service_name])
run(["systemctl", "restart", service_name])
run(["systemctl", "status", "--no-pager", service_name])
