#!/usr/bin/env python3
"""Allow checker for Caddy on-demand TLS over a Unix socket.

Returns:
- 200 when domain is allowed
- 403 when domain is denied
- 404 for unknown paths
"""

from __future__ import annotations

import argparse
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler
from pathlib import Path
from socketserver import UnixStreamServer
from urllib.parse import parse_qs, urlparse

ALLOWED_SUFFIX = ""
APP_ROOT = Path()


def normalize_suffix(value: str) -> str:
    suffix = value.strip().lower().rstrip(".")
    if not suffix:
        raise ValueError("allowed suffix cannot be empty")
    if not suffix.startswith("."):
        suffix = f".{suffix}"
    return suffix


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run allow-domain checker over Unix socket")
    parser.add_argument("socket_path", help="Unix socket path to bind")
    parser.add_argument("app_root", help="Root directory containing app subdirectories")
    parser.add_argument(
        "--allowed-suffix",
        required=True,
        help="Allowed domain suffix used to validate the domain query",
    )
    return parser.parse_args()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path == "/health":
            self._reply(HTTPStatus.OK, b"ok")
            return

        if parsed.path != "/allow-domain":
            self._reply(HTTPStatus.NOT_FOUND)
            return

        domain = parse_qs(parsed.query).get("domain", [""])[0].strip().lower().rstrip(".")

        if not domain.endswith(ALLOWED_SUFFIX):
            self._deny()
            return

        app = domain[: -len(ALLOWED_SUFFIX)]
        if not app or "." in app or app.startswith("."):
            self._deny()
            return

        if (APP_ROOT / app).is_dir():
            self._reply(HTTPStatus.OK, b"ok")
            return

        self._deny()

    def _deny(self) -> None:
        self._reply(HTTPStatus.FORBIDDEN, b"forbidden")

    def _reply(self, status: HTTPStatus, body: bytes = b"") -> None:
        self.send_response(status)
        self.end_headers()
        if body:
            self.wfile.write(body)

    def log_message(self, *_args) -> None:
        return


if __name__ == "__main__":
    args = parse_args()
    socket_path = Path(args.socket_path).expanduser().resolve()
    APP_ROOT = Path(args.app_root).expanduser().resolve()
    ALLOWED_SUFFIX = normalize_suffix(args.allowed_suffix)

    socket_path.parent.mkdir(parents=True, exist_ok=True)
    if socket_path.exists():
        socket_path.unlink()

    server = UnixStreamServer(str(socket_path), Handler)
    try:
        server.serve_forever()
    finally:
        server.server_close()
        if socket_path.exists():
            socket_path.unlink()
