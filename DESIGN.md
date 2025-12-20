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

- **Startup**: Triggered by the first incoming request if no process is currently running.
- **Persistence**: The process remains running as long as it is handling at least one active request.
- **Shutdown**: A 30-second idle timer is started when the active request count drops to zero. If a new request arrives before the timer expires, the timer is cancelled. Otherwise, the process is terminated.

### 2. Communication & Discovery
The module and the managed process communicate via environment variables and standard output for initialization.

- **LISTEN_HOST**: Caddy generates a local address (e.g., `127.0.0.1:0` to let the OS pick a port) and passes it to the process via the `LISTEN_HOST` environment variable.
- **Port Specification**: Users can specify a fixed port.
- **Address Discovery**: Upon startup, the process must write its actual listening address (e.g., `127.0.0.1:45678`) to its `stdout`. Caddy reads this first line to determine the proxy target.
- **Stderr**: All subsequent output to `stderr` is streamed directly to Caddy's logs.

### 3. Request Handling
Once the process is ready and the address is discovered:

- **Proxying**: Caddy uses a reverse proxy handler to forward incoming HTTP requests to the discovered address.
- **Connection Tracking**: The module increments an internal counter for every request entering the proxy and decrements it upon completion.

## Implementation Plan

### Struct Updates (`CGI` in `module.go`)
- `mode`: A new field to toggle between `cgi` (default) and `proxy` modes.
- `port`: A string field to store the specified port (e.g., `8001`).
- `process`: Reference to the running `*os.Process`.
- `proxyAddr`: The discovered address of the backend.
- `activeRequests`: Atomic counter for tracking concurrency.
- `idleTimer`: A `*time.Timer` for managing the 30s shutdown.
- `mu`: A `sync.Mutex` to protect process state transitions.
- `reverseProxy`: A `*httputil.ReverseProxy` instance to handle the actual proxying.

### Logic Updates (`cgi.go`)
- **`ServeHTTP`**:
    - If `mode == proxy`:
        - **State Tracking**: Use `mu` to safely check and update process state.
        - **Dynamic Startup**: If `process` is nil, call `startProcess()`. This involves spawning the process and reading the first line of `stdout` to get the `proxyAddr`.
        - **Concurrency Tracking**: Increment `activeRequests` before proxying and decrement after.
        - **Idle Management**: Stop the `idleTimer` when a request starts. If `activeRequests` reaches zero after a request, start the `idleTimer` for 30 seconds.
        - **Routing**: Use the `reverseProxy` to forward the request to `proxyAddr`.
    - Else: Execute traditional CGI logic using `net/http/cgi`.

- **`startProcess()`**:
    - **Environment Setup**: Generate `LISTEN_HOST` (e.g., `127.0.0.1:0`).
    - **Process Spawning**: Use `os/exec` to start the configured executable with arguments.
    - **Address Discovery**: Read the first line from the process's `stdout`. This line must contain the address the process is listening on.
    - **Logging**: Start a goroutine to continuously read `stderr` and pipe it to Caddy's logger.
    - **Cleanup**: Ensure that if the process exits unexpectedly, the state is cleaned up.

### Configuration
New Caddyfile subdirective:
```caddyfile
cgi /path* ./binary {
    mode proxy
    port 8001
}
```
