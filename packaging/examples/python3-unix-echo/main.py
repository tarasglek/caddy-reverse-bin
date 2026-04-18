#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# ///
import http.server
import json
import os
import socket
import sys

LAST_HEALTH_METHOD = None


class UnixHTTPServer(http.server.HTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)


class EchoHandler(http.server.BaseHTTPRequestHandler):
    def address_string(self):
        if isinstance(self.client_address, (str, bytes)):
            return str(self.client_address) or "unix"
        if not self.client_address or not hasattr(self.client_address, "__getitem__"):
            return "unix"
        try:
            return super().address_string()
        except (IndexError, TypeError):
            return "unix"

    def _send_json(self, payload, status=200):
        self.send_response(status)
        self.send_header("Content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(payload).encode("utf-8"))

    def _base_payload(self):
        return {
            "backend": "echo-backend",
            "pid": os.getpid(),
            "headers": dict(self.headers.items()),
            "path": self.path,
        }

    def do_GET(self):
        global LAST_HEALTH_METHOD

        if self.path == "/health":
            LAST_HEALTH_METHOD = "GET"
            self._send_json({"status": "ok", "method": "GET"})
            return

        if self.path == "/health-last":
            self._send_json({"last_health_method": LAST_HEALTH_METHOD})
            return

        self._send_json(self._base_payload())

    def do_HEAD(self):
        global LAST_HEALTH_METHOD
        if self.path == "/health":
            LAST_HEALTH_METHOD = "HEAD"
        self.send_response(200)
        self.end_headers()


if __name__ == "__main__":
    socket_path = os.environ.get("SOCKET_PATH")
    if not socket_path:
        print("Error: SOCKET_PATH must be set", file=sys.stderr)
        sys.exit(1)

    if os.path.exists(socket_path):
        os.remove(socket_path)

    httpd = UnixHTTPServer(socket_path, EchoHandler)
    print(socket_path)
    sys.stdout.flush()

    try:
        httpd.serve_forever()
    finally:
        if os.path.exists(socket_path):
            os.remove(socket_path)
