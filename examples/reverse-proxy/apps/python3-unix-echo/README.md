# Python3 Unix Socket Echo Server

A simple HTTP echo server that listens on a Unix domain socket.

## Configuration

The server reads its socket path from the `SOCKET_PATH` environment variable.

For apps launched by `discover-app.py`, use a relative `.env` value:

```bash
SOCKET_PATH=data/echo.sock
```

`discover-app.py` resolves that relative app config into an absolute detector payload:

```json
{
  "reverse_proxy_to": "unix//absolute/path/to/data/echo.sock"
}
```

## Running

```bash
export SOCKET_PATH=data/echo.sock
./main.py
```

The socket will be created at `data/echo.sock` relative to the current directory.
