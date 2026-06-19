# Python3 Unix Socket Echo Server

A simple HTTP echo server that listens on a Unix domain socket.

## Configuration

The server reads its socket path from the `SOCKET_PATH` environment variable.

```bash
SOCKET_PATH=/tmp/reverse-bin-example.sock
```

## Running

```bash
export SOCKET_PATH=/tmp/reverse-bin-example.sock
uv run --script ./main.py
```

The plugin integration tests also use this fixture with per-test temporary socket paths.
