# Design: Managed Reverse Proxy for CGI

## Overview
This document outlines the transition of the Caddy CGI module from a per-request execution model to a managed, long-running reverse proxy model. This approach improves performance by avoiding process startup overhead for every request while maintaining the "on-demand" nature of CGI.

## Architecture

### 1. Process Lifecycle
Instead of spawning a process for every HTTP request, the module manages a single persistent process that acts as an HTTP server.

- **Startup**: Triggered by the first incoming request if no process is currently running.
- **Persistence**: The process remains running as long as it is handling at least one active request.
- **Shutdown**: A 30-second idle timer is started when the active request count drops to zero. If a new request arrives before the timer expires, the timer is cancelled. Otherwise, the process is terminated.

### 2. Communication & Discovery
The module and the managed process communicate via environment variables and standard output for initialization.

- **LISTEN_HOST**: Caddy generates a local address (e.g., `127.0.0.1:0` to let the OS pick a port) and passes it to the process via the `LISTEN_HOST` environment variable.
- **Address Discovery**: Upon startup, the process must write its actual listening address (e.g., `127.0.0.1:45678`) to its `stdout`. Caddy reads this first line to determine the proxy target.
- **Stderr**: All subsequent output to `stderr` is streamed directly to Caddy's logs.

### 3. Request Handling
Once the process is ready and the address is discovered:

- **Proxying**: Caddy uses a reverse proxy handler to forward incoming HTTP requests to the discovered address.
- **Connection Tracking**: The module increments an internal counter for every request entering the proxy and decrements it upon completion.

## Implementation Plan

### Struct Updates (`CGI` in `module.go`)
- `mode`: A new field to toggle between `cgi` (default) and `proxy` modes.
- `process`: Reference to the running `*os.Process`.
- `proxyAddr`: The discovered address of the backend.
- `activeRequests`: Atomic counter for tracking concurrency.
- `idleTimer`: A `*time.Timer` for managing the 30s shutdown.
- `mu`: A `sync.Mutex` to protect process state transitions.

### Logic Updates (`cgi.go`)
- **`ServeHTTP`**:
    - If `mode == proxy`:
        - Lock `mu`.
        - If `process` is nil, call `startProcess()`.
        - Increment `activeRequests`.
        - Stop `idleTimer`.
        - Unlock `mu`.
        - Proxy the request.
        - Lock `mu`.
        - Decrement `activeRequests`.
        - If `activeRequests == 0`, start `idleTimer` for 30s.
        - Unlock `mu`.
    - Else: Execute traditional CGI logic.

- **`startProcess()`**:
    - Generate `LISTEN_HOST`.
    - Spawn process with `os/exec`.
    - Read first line of `stdout` for `proxyAddr`.
    - Start a goroutine to pipe `stderr` to Caddy logs.

### Configuration
New Caddyfile subdirective:
```caddyfile
cgi /path* ./binary {
    mode proxy
}
```
