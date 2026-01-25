import http.server
import os
import sys

class EchoHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-type', 'text/plain')
        self.end_headers()
        
        response = f"Request Headers:\n{self.headers}"
        self.wfile.write(response.encode('utf-8'))

    def do_HEAD(self):
        self.send_response(200)
        self.end_headers()

if __name__ == "__main__":
    # Default to port 8001 if not specified
    port = 8001
    if len(sys.argv) > 1:
        try:
            port = int(sys.argv[1])
        except ValueError:
            pass

    server_address = ('127.0.0.1', port)
    httpd = http.server.HTTPServer(server_address, EchoHandler)
    
    # Signal readiness to Caddy by printing the address to stdout
    print(f"127.0.0.1:{port}")
    sys.stdout.flush()
    
    httpd.serve_forever()
