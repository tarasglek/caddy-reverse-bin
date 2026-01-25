#!/usr/bin/env python3
import sys
import http.server
import http.client
import json
import os
import socket
import random

class UnixHTTPConnection(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect("/tmp/caddy.sock")

class DiscoveryHandler(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        self.send_response(200)
        self.end_headers()

    def do_GET(self):
        with open('/tmp/access', 'a') as f:
            f.write(f"Request: {self.command} {self.path}\nHeaders:\n{self.headers}\n")
        
        # Find an available port
        port = None
        tried_ports = set()
        while len(tried_ports) < 10:
            p = random.randint(10000, 60000)
            if p in tried_ports:
                continue
            tried_ports.add(p)
            try:
                s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                s.bind(('', p))
                port = s.getsockname()[1]
                s.close()
                break
            except socket.error:
                continue
        
        if port is None:
            self.send_response(500)
            self.end_headers()
            self.wfile.write(b"Could not find available port")
            return

        # Update Caddy API to include subdomain path
        forwarded_host = self.headers.get('X-Forwarded-Host', self.headers.get('Host', ''))
        host_only = forwarded_host.split(':')[0]
        parts = host_only.split('.')
        
        # If host is 'sub.domain.tld', subdomain is 'sub'
        # If host is 'localhost', there is no subdomain
        subdomain = parts[0] if len(parts) > 1 else ""
        
        if not subdomain:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b"Could not determine subdomain from host")
            return


        app_root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", subdomain))
        if not os.path.isdir(app_root):
            self.send_response(404)
            self.end_headers()
            self.wfile.write(f"Application directory not found: {subdomain}".encode())
            return

        subdomain_config = {
            "match": [{"host": [host_only]}],
            "handle": [{
                "handler": "subroute",
                "routes": [{
                    "handle": [{
                        "handler": "reverse-bin",
                        "mode": "proxy",
                        "workingDirectory": app_root,
                        "executable": "./main.py",
                        "args": [f"{port}"],
                        "reverse_proxy_to": f":{port}"
                    }]
                }]
            }],
            "terminal": True
        }
        
        try:
            conn = UnixHTTPConnection('localhost')
            conn.request(
                "PUT",
                "/config/apps/http/servers/srv0/routes/0",
                body=json.dumps(subdomain_config).encode(),
                headers={
                    'Content-Type': 'application/json',
                    'Origin': 'http://localhost'
                }
            )
            resp = conn.getresponse()
            if resp.status >= 300:
                raise Exception(f"Caddy returned {resp.status} {resp.reason}")
            conn.close()
            
            # Issue redirect after successful update
            self.send_response(302)
            self.send_header('Location', self.path)
            self.end_headers()
            return
        except Exception as e:
            self.send_response(500)
            self.end_headers()
            self.wfile.write(f"Failed to update Caddy: {e}".encode())

def run():
    if len(sys.argv) < 2:
        sys.exit(1)
    
    port_str = sys.argv[1].replace(':', '')
    port = int(port_str)
    
    server_address = ('', port)
    httpd = http.server.HTTPServer(server_address, DiscoveryHandler)
    httpd.serve_forever()

if __name__ == "__main__":
    run()
