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
    # Use PORT environment variable, default to 8001
    port = int(os.environ.get("PORT", 8001))

    server_address = ('127.0.0.1', port)
    httpd = http.server.HTTPServer(server_address, EchoHandler)
    
    # Signal readiness to Caddy by printing the address to stdout
    print(f"127.0.0.1:{port}")
    sys.stdout.flush()
    
    httpd.serve_forever()
