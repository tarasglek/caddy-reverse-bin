#!/usr/bin/env python3
from __future__ import annotations

import argparse
import pwd
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from textwrap import dedent


@dataclass(frozen=True, slots=True)
class ServiceConfig:
    username: str
    home: Path
    service_name: str = "reverse-bin.service"

    @property
    def reverse_bin_root(self) -> Path:
        return self.home / "reverse-bin"

    @property
    def service_path(self) -> Path:
        return Path("/etc/systemd/system") / self.service_name

    @property
    def caddy_path(self) -> Path:
        return self.reverse_bin_root / ".bin" / "caddy"

    def unit_text(self) -> str:
        return dedent(
            f"""\
            [Unit]
            Description=reverse-bin Caddy proxy
            After=network-online.target
            Wants=network-online.target

            [Service]
            Type=simple
            User={self.username}
            Group={self.username}
            WorkingDirectory={self.reverse_bin_root}
            Environment=OPS_EMAIL=ops@example.com
            Environment=DOMAIN_SUFFIX=example.com
            ExecStart={self.reverse_bin_root}/.bin/run.sh
            Restart=on-failure
            RestartSec=2

            [Install]
            WantedBy=multi-user.target
            """
        )


def run(cmd: list[str], **kwargs: object) -> None:
    subprocess.run(cmd, check=True, **kwargs)


def resolve_user(username: str, service_name: str) -> ServiceConfig:
    try:
        user = pwd.getpwnam(username)
    except KeyError as exc:
        raise ValueError(f"user not found: {username}") from exc
    return ServiceConfig(username=username, home=Path(user.pw_dir), service_name=service_name)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Install/update reverse-bin systemd unit using sudo tee"
    )
    parser.add_argument("username", help="Linux user that owns $HOME/reverse-bin")
    parser.add_argument(
        "--service-name",
        default="reverse-bin.service",
        help="systemd service file name (default: reverse-bin.service)",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    try:
        cfg = resolve_user(args.username, args.service_name)
    except ValueError as err:
        print(f"error: {err}", file=sys.stderr)
        return 1

    print(f"installing/updating {cfg.service_path} via sudo tee")
    run(["sudo", "tee", str(cfg.service_path)], input=cfg.unit_text(), text=True, stdout=subprocess.DEVNULL)

    print("reloading systemd daemon")
    run(["sudo", "systemctl", "daemon-reload"])

    print(f"enabling + starting {cfg.service_name}")
    run(["sudo", "systemctl", "enable", "--now", cfg.service_name])

    print("current status:")
    run(["sudo", "systemctl", "status", "--no-pager", cfg.service_name])

    print("\nimportant: ensure caddy has bind capability:")
    print(f"  sudo setcap 'cap_net_bind_service=+ep' {cfg.caddy_path}")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
