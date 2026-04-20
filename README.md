# caddy-reverse-bin

`reverse-bin` is a Caddy-based on-demand app launcher packaged for Debian systems.

## Debian package layout

The package installs these primary paths:

- binary: `/usr/bin/reverse-bin-caddy`
- config: `/etc/reverse-bin/Caddyfile`
- defaults: `/etc/default/reverse-bin`
- helper scripts: `/usr/lib/reverse-bin/`
- writable app root: `/var/lib/reverse-bin/apps/`
- service home: `/var/lib/reverse-bin/home`
- packaged examples: `/usr/share/doc/reverse-bin/examples/`

## What it does

1. Runs a custom Caddy binary with the `reverse-bin` handler.
2. Uses `discover-app.py` to detect app entrypoints and proxy targets.
3. Uses `landrun` and helper scripts installed under `/usr/lib/reverse-bin/`.

## Build the Debian package

```bash
make deb
```

This produces a `.deb` in the parent directory.

## Runtime model

- Caddy runs from the packaged systemd unit.
- The service reads `/etc/reverse-bin/Caddyfile`.
- The service loads deployment-specific variables from `/etc/default/reverse-bin`.
- App directories live under `/var/lib/reverse-bin/apps/`.
- Example apps ship under `/usr/share/doc/reverse-bin/examples/` and can be copied into the app root.

## Example deployment flow

```bash
sudo editor /etc/default/reverse-bin
sudo install -d -o reverse-bin -g reverse-bin /var/lib/reverse-bin/apps
sudo cp -a /usr/share/doc/reverse-bin/examples/python3-unix-echo /var/lib/reverse-bin/apps/
sudo chown -R reverse-bin:reverse-bin /var/lib/reverse-bin/apps/python3-unix-echo
sudo systemctl enable reverse-bin.service
sudo systemctl restart reverse-bin.service
```

Set these values in `/etc/default/reverse-bin` before restarting:

```sh
OPS_EMAIL=admin@overthinker.dev
DOMAIN_SUFFIX=overthinker.dev
```
