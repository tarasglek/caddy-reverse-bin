#!/usr/bin/env python3
import sys
import http.server
import urllib.request
import json
import os

class DiscoveryHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        print(f"Request: {self.command} {self.path}")
        
        # Update Caddy API to include subdomain path
        app_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "subdomain"))
        subdomain_config = {
            "handler": "reverse-bin",
            "mode": "proxy",
            "workingDirectory": app_root,
            "executable": "./main.py",
            "args": ["--port", "8002"],
            "reverse_proxy_to": ":8002"
        }
        
        try:
            req = urllib.request.Request(
                "http://localhost:2019/config/apps/http/servers/srv0/routes",
                data=json.dumps(subdomain_config).encode(),
                method='POST',
                headers={'Content-Type': 'application/json'}
            )
            with urllib.request.urlopen(req) as f:
                print(f"Caddy API response: {f.status}")
            
            # Issue redirect after successful update
            self.send_response(302)
            self.send_header('Location', self.path)
            self.end_headers()
            return
        except Exception as e:
            print(f"Failed to update Caddy: {e}")
            self.send_response(500)
            self.end_headers()
            self.wfile.write(f"Failed to update Caddy: {e}".encode())

def run():
    if len(sys.argv) < 2:
        print("Usage: discover-app.py :<port>")
        sys.exit(1)
    
    port_str = sys.argv[1].replace(':', '')
    port = int(port_str)
    
    server_address = ('', port)
    httpd = http.server.HTTPServer(server_address, DiscoveryHandler)
    print(f"Starting discovery server on port {port}...")
    httpd.serve_forever()

if __name__ == "__main__":
    run()
