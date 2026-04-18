# Python3 Unix Socket Echo Server

This example app is shipped with the Debian package under:

- `/usr/share/doc/reverse-bin/examples/python3-unix-echo/`

To deploy it on a package-installed system:

1. Copy the example into `/var/lib/reverse-bin/apps/python3-unix-echo/`
2. Ensure the app tree is owned by `reverse-bin:reverse-bin`
3. Start `reverse-bin.service`

The app listens on the Unix socket configured in `.env`:

```bash
SOCKET_PATH=data/echo.sock
```
