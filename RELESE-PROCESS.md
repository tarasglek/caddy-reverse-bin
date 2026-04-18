# Release Process (caddy-reverse-bin)

This project now ships a Debian package in addition to binary release artifacts.

## Debian package validation

Before tagging a release:

```bash
make deb
dpkg-deb -c ../reverse-bin_*_*.deb
```

Verify the package contains:

- `/usr/bin/reverse-bin-caddy`
- `/etc/reverse-bin/Caddyfile`
- `/usr/lib/reverse-bin/`
- the systemd unit
- `/usr/share/doc/reverse-bin/examples/`
