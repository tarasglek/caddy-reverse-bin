#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# ///
import http.server
import os
import sys

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
    # Use LISTEN environment variable
    addr_str = os.environ.get("LISTEN")
    if not addr_str:
        print("Error: LISTEN environment variable is not set", file=sys.stderr)
        sys.exit(1)

    host, port_str = addr_str.split(':')
    port = int(port_str)
    server_address = (host, port)
    httpd = http.server.HTTPServer(server_address, EchoHandler)
    print(f"127.0.0.1:{port}")

    # Signal readiness to Caddy by printing the address to stdout
    sys.stdout.flush()
    
    httpd.serve_forever()
