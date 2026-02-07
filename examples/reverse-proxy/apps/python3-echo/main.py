#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# ///
import http.server
import os
import sys
import socket

class UnixHTTPServer(http.server.HTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)

class EchoHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-type', 'text/plain')
        self.end_headers()
        
        response = f"Request Headers:\n{self.headers}\nLocation: {self.path}"
        self.wfile.write(response.encode('utf-8'))

    def do_HEAD(self):
        self.send_response(200)
        self.end_headers()

if __name__ == "__main__":
    # Use REVERSE_PROXY_TO environment variable
    addr_str = os.environ.get("REVERSE_PROXY_TO")
    if not addr_str:
        print("Error: REVERSE_PROXY_TO environment variable is not set", file=sys.stderr)
        sys.exit(1)
    if addr_str.startswith("unix/"):
        socket_path = addr_str[5:]
        if os.path.exists(socket_path):
            os.remove(socket_path)
        server_address = socket_path
        httpd = UnixHTTPServer(server_address, EchoHandler)
        print(addr_str)
    else:
        host, port_str = addr_str.split(':')
        port = int(port_str)
        server_address = (host, port)
        httpd = http.server.HTTPServer(server_address, EchoHandler)
        print(f"127.0.0.1:{port}")

    # Signal readiness to Caddy by printing the address to stdout
    sys.stdout.flush()
    
    try:
        httpd.serve_forever()
    finally:
        if addr_str.startswith("unix/"):
            os.remove(addr_str[5:])
