# Python3 Unix Socket Echo Server

A simple HTTP echo server that listens on a Unix domain socket.

## Configuration

The server reads its socket path from the `REVERSE_PROXY_TO` environment variable.

### Important: Socket Path Format

For **apps**, the `REVERSE_PROXY_TO` must use a **relative path** with the `unix/` prefix:

```bash
# In .env file (relative path - socket created relative to app directory)
REVERSE_PROXY_TO=unix/data/echo.sock
```

The app strips the `unix/` prefix and uses the remaining path (`data/echo.sock`) as the socket location.

### For Discovery Scripts

When using `dynamic_proxy_detector`, the discovery script must return an **absolute path**:

```json
{
  "reverse_proxy_to": "unix//absolute/path/to/data/echo.sock"
}
```

The `discover-app.py` utility handles this automatically - it resolves relative paths from `.env` to absolute paths.

## Running

```bash
# Set the environment variable
export REVERSE_PROXY_TO=unix/data/echo.sock

# Run the server
./main.py
```

The socket will be created at `data/echo.sock` relative to the current directory.
