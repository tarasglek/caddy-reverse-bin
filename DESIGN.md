# Design: Managed Reverse Proxy for CGI

## Overview
This document outlines the transition of the Caddy CGI module from a per-request execution model to a managed, long-running reverse proxy model. This approach improves performance by avoiding process startup overhead for every request while maintaining the "on-demand" nature of CGI.

## Architecture

### Traditional CGI Lifecycle (Current)
In the current model, every HTTP request triggers a full process lifecycle.

```text
HTTP Request
     |
     v
[ Caddy Handler ]
     |
     +--- fork/exec ---> [ Subprocess (e.g., hello.sh) ]
     |                          |
     | <--- Stdout (Response) --+
     |                          |
     | <--- Stderr (Logs) ------+
     |                          |
[ Response Sent ]             [ Exit ]
```

---

### Managed Reverse Proxy Lifecycle (Proposed)
In the new model, the subprocess is long-running. Caddy manages the process state, tracks active connections, and handles idle timeouts.

```text
HTTP Request
     |
     v
[ Caddy Handler ] <-------+
     |                    |
     | (Lock Mutex)       |
     |                    |
     +-- [ Process Running? ] -- No --> [ Start Subprocess ]
     |          |                          |
     |         Yes <--- (Read Stdout) -----+ (Get Proxy Address)
     |          |
     +-- [ Increment Active Count ]
     |          |
     +-- [ Stop/Reset Idle Timer ]
     |          |
     | (Unlock Mutex)
     |          |
     +-- [ Reverse Proxy Request ] ----> [ Persistent Subprocess ]
     |          |                               |
     | <------- [ Receive Response ] <----------+
     |          |
     | (Lock Mutex)
     |          |
     +-- [ Decrement Active Count ]
     |          |
     +-- [ Count == 0? ] -- Yes --> [ Start 30s Idle Timer ]
     |                              |
     | (Unlock Mutex)               +--- (On Expiry) ---> [ Kill Process ]
     v
[ Response Sent ]
```

### 1. Process Lifecycle
Instead of spawning a process for every HTTP request, the module manages a single persistent process that acts as an HTTP server.

#### Managed Process Lifecycle
- **Startup**: Triggered by the first incoming request if no process is currently running.
- **Persistence**: The process remains running as long as it is handling at least one active request.
- **Shutdown**: A 30-second idle timer is started when the active request count drops to zero. If a new request arrives before the timer expires, the timer is cancelled. Otherwise, the process is terminated.

### 2. Communication & Discovery
The module and the managed process communicate via standard output or HTTP polling for initialization.

- **Address Specification**: Users must specify a target address in the configuration using the `reverse_proxy_to` subdirective (e.g., `:9000` or `http://localhost:9000`).
- **Address Discovery**: By default, the module waits for the process to write a line to `stdout` containing the listening address to signal readiness.
- **Readiness Check**: Alternatively, a `readiness_check` can be configured to poll the backend via HTTP (e.g., `readiness_check HEAD /`).
- **Logging**: Subsequent output to `stdout` (after readiness) and all output to `stderr` is streamed directly to Caddy's logs.

### 3. Request Handling
Once the process is ready and the address is discovered:

- **Proxying**: Caddy uses a reverse proxy handler to forward incoming HTTP requests to the discovered address.
- **Connection Tracking**: The module increments an internal counter for every request entering the proxy and decrements it upon completion.

### Configuration
New Caddyfile subdirective:
```caddyfile
reverse-bin /path* ./binary {
    mode proxy
    reverse_proxy_to :8001
    readiness_check HEAD /
}

```
