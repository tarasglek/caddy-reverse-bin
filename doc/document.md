# reverse-bin for Caddy

`reverse-bin` is a Caddy HTTP handler that launches an executable on demand and reverse proxies requests to it.

## Debian deployment

The Debian package installs:

- `/usr/bin/reverse-bin-caddy`
- `/etc/reverse-bin/Caddyfile`
- `/etc/default/reverse-bin`
- `/usr/lib/reverse-bin/discover-app.py`
- `/usr/lib/reverse-bin/allow-domain.py`
- `/usr/lib/reverse-bin/landrun`
- `/var/lib/reverse-bin/apps/`
- `/usr/share/doc/reverse-bin/examples/`

The systemd unit runs as the `reverse-bin` system user with working directory `/var/lib/reverse-bin/home`.

## Building

```bash
make deb
```

## Installing apps

Copy an example app or your own app into `/var/lib/reverse-bin/apps/<app-name>/` and ensure the tree is owned by `reverse-bin:reverse-bin`.

## Service management

The package installs but does not auto-enable the service.

Edit `/etc/default/reverse-bin` to set deployment-specific values such as:

```sh
OPS_EMAIL=admin@overthinker.dev
DOMAIN_SUFFIX=overthinker.dev
```

Then enable and restart the service:

```bash
sudo systemctl enable reverse-bin.service
sudo systemctl restart reverse-bin.service
```
