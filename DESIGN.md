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

#### Auto-Discovery Mode
A new `auto-discovery` mode allows for dynamic application provisioning in catch-all scenarios (e.g., wildcard domains).

- **Trigger**: When a request hits a catch-all handler, the module checks if a directory corresponding to the hostname exists within a configured `appDir`.
- **Validation**: If the directory does not exist, the handler returns a 404.
- **Provisioning**: If the directory exists, the module executes a `discoveryBin`. This binary is responsible for generating a `reverse-bin-caddy.json` configuration file within the application directory.
- **Integration**: Once generated, the module dynamically injects this configuration into the running Caddy instance.

#### Managed Process Lifecycle
- **Startup**: Triggered by the first incoming request if no process is currently running.
- **Persistence**: The process remains running as long as it is handling at least one active request.
- **Shutdown**: A 30-second idle timer is started when the active request count drops to zero. If a new request arrives before the timer expires, the timer is cancelled. Otherwise, the process is terminated.

### 2. Communication & Discovery
The module and the managed process communicate via environment variables and standard output for initialization.

- **LISTEN_HOST**: Caddy passes the configured address (e.g., `127.0.0.1:8001`) to the process via the `LISTEN_HOST` environment variable.
- **Port Specification**: Users must specify a fixed port in the configuration.
- **Address Discovery**: Not used. The proxy target is static based on the configuration.
- **Readiness**: The process must write a line to `stdout` containing the listening address (e.g., `127.0.0.1:8001`) to signal that it is ready.
- **Logging**: Subsequent output to `stdout` and all output to `stderr` is streamed directly to Caddy's logs.

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
        - **Dynamic Startup**: If `process` is nil, call `startProcess()`. This involves spawning the process.
        - **Concurrency Tracking**: Increment `activeRequests` before proxying and decrement after.
        - **Idle Management**: Stop the `idleTimer` when a request starts. If `activeRequests` reaches zero after a request, start the `idleTimer` for 30 seconds.
        - **Routing**: Use the pre-provisioned `reverseProxy` to forward the request.
    - Else: Execute traditional CGI logic using `net/http/cgi`.

- **`startProcess()`**:
    - **Environment Setup**: Pass `LISTEN_HOST` based on configured port.
    - **Process Spawning**: Use `os/exec` to start the configured executable with arguments.
    - **Readiness**: Wait for a line of output on `stdout` containing the expected address to confirm the process is ready.
    - **Logging**: Start goroutines to continuously read `stdout` (after readiness signal) and `stderr` and pipe them to Caddy's logger.
    - **Cleanup**: Ensure that if the process exits unexpectedly, the state is cleaned up.

### Configuration
New Caddyfile subdirective:
```caddyfile
reverse-bin /path* ./binary {
    mode proxy
    port 8001
}

# Auto-discovery example
reverse-bin * {
    mode auto-discovery
    app_dir ./apps
    discovery_bin /usr/local/bin/discover
}
```
