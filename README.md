# caddy-reverse-bin

Turn caddy into an ondemand app launcher. 

There are 3 components:

1) Caddy plugin that can reverse-proxy to an ondemand server that then gets shut down Lambda-style.

2) An app detector script ([`utils/discover-app/discover-app.py`](utils/discover-app/discover-app.py)) can dynamically figure out how to launch certain apps and how to sandbox them. Explicit app config now lives in `.env` via `REVERSE_BIN_COMMAND` plus either `LISTEN` or `SOCKET_PATH`.

3) Linux Landlock provides the sandbox via [`landrun`](https://github.com/Zouuup/landrun) (see also the example launcher in [`examples/reverse-proxy/apps/python3-unix-echo/run.py`](examples/reverse-proxy/apps/python3-unix-echo/run.py)).

inspired by [smallweb.run](https://smallweb.run).

